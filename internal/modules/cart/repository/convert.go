package repository

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/repository/cartdb"
)

// This file is the ONE place for pgtype <-> domain model conversions and for
// driver error classification.
//
// The boundary sitting here is deliberate: driver-specific types
// (pgtype.Timestamptz, []byte for jsonb, *pgconn.PgError) DO NOT LEAVE the
// repository. The service and the API layer see time.Time, map[string]any and
// core/errors typed errors.

// Error codes. The calling side can inspect them with errors.CodeOf; the API
// layer passes the same codes on to the client.
const (
	codeCartNotFound        = "cart_not_found"
	codeLineItemNotFound    = "cart_line_item_not_found"
	codeShippingNotFound    = "cart_shipping_method_not_found"
	codeCartCompleted       = "cart_completed"
	codeLineItemExists      = "cart_line_item_exists"
	codeAddressExists       = "cart_address_exists"
	codeShippingOptionTaken = "cart_shipping_option_already_added"
	codeTotalsInconsistent  = "cart_totals_inconsistent"
	codeAmountOutOfRange    = "cart_amount_out_of_range"
	codeMetadataInvalid     = "cart_metadata_invalid"
	codeTxRequired          = "cart_tx_required"
	codeQueryFailed         = "cart_query_failed"
	codeConcurrentUpdate    = "cart_concurrent_update"
)

// Constraint names; they are used to turn a driver error into a meaningful typed
// error. The names are EXACTLY the ones in the migration.
const (
	constraintLineVariantUniq = "cart_line_items_cart_variant_uniq"
	constraintAddressTypeUniq = "cart_addresses_cart_type_uniq"
	constraintShippingOptUniq = "cart_shipping_methods_cart_option_uniq"
	constraintCartTotals      = "carts_totals_consistent"
	constraintLineTotals      = "cart_line_items_totals_consistent"
	constraintTotalsRevRange  = "carts_totals_revision_range"
	constraintLineQtyPositive = "cart_line_items_quantity_positive"
	constraintCartLineItemsFK = "cart_line_items_cart_id_fkey"
	constraintCartAddressesFK = "cart_addresses_cart_id_fkey"
	constraintCartShippingFK  = "cart_shipping_methods_cart_id_fkey"
	// constraintNonnegSuffix is the common suffix of every CHECK constraint that
	// bans negative money; instead of being listed one by one they are recognized
	// by the suffix.
	constraintNonnegSuffix = "_nonneg"
)

// PostgreSQL SQLSTATE codes.
const (
	sqlStateUniqueViolation     = "23505"
	sqlStateForeignKeyViolation = "23503"
	sqlStateCheckViolation      = "23514"
	sqlStateDeadlockDetected    = "40P01"
)

// classify turns a driver error into a typed error.
//
// Uniqueness, foreign key and CHECK violations are situations the client (or the
// workflow) can fix; unclassified, they would all appear as a 500 and the real
// reason would stay in the log alone. A deadlock is handled separately for the
// same reason: there is nothing wrong with the operation itself, it CAN BE
// RETRIED.
func classify(err error, code, format string, a ...any) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return errors.Wrap(err, errors.KindInternal, code, format, a...)
	}

	switch pgErr.Code {
	case sqlStateUniqueViolation:
		switch pgErr.ConstraintName {
		case constraintLineVariantUniq:
			return errors.Wrap(err, errors.KindConflict, codeLineItemExists,
				"this variant is already in the cart; its quantity should be raised")
		case constraintAddressTypeUniq:
			return errors.Wrap(err, errors.KindConflict, codeAddressExists,
				"the cart already has a record of this type")
		case constraintShippingOptUniq:
			return errors.Wrap(err, errors.KindConflict, codeShippingOptionTaken,
				"this shipping option has already been added to the cart")
		}
	case sqlStateForeignKeyViolation:
		// A line item, an address or a shipping method cannot be attached to a
		// cart that DOES NOT EXIST.
		switch pgErr.ConstraintName {
		case constraintCartLineItemsFK, constraintCartAddressesFK, constraintCartShippingFK:
			return errors.Wrap(err, errors.KindNotFound, codeCartNotFound, "cart not found")
		}
	case sqlStateCheckViolation:
		return classifyCheck(err, pgErr.ConstraintName, code, format, a...)
	case sqlStateDeadlockDetected:
		// Because the lock order is unified this does not arise in normal flows;
		// this is the last line of defense. The transaction has been rolled back,
		// the same request can be retried as it is — that is why this is Conflict
		// and not Internal (500).
		return errors.Wrap(err, errors.KindConflict, codeConcurrentUpdate,
			"conflicted with a concurrent operation; the request can be retried")
	}
	return errors.Wrap(err, errors.KindInternal, code, format, a...)
}

// classifyCheck turns CHECK constraint violations into typed errors.
//
// The totals identity violation is kept apart: the service runs the same check
// FIRST with a more readable error, so landing here means the check was skipped
// (or that SQL was touched directly) and the message has to say so.
func classifyCheck(err error, constraint, code, format string, a ...any) error {
	switch {
	case constraint == constraintCartTotals:
		return errors.Wrap(err, errors.KindInvalid, codeTotalsInconsistent,
			"cart totals are inconsistent: total = subtotal - discount_total + tax_total + shipping_total is required")
	case constraint == constraintLineTotals:
		return errors.Wrap(err, errors.KindInvalid, codeTotalsInconsistent,
			"line item totals are inconsistent: total = subtotal - discount_total + tax_total is required")
	case constraint == constraintTotalsRevRange:
		return errors.Wrap(err, errors.KindInvalid, codeTotalsInconsistent,
			"totals cannot be stamped for a cart shape that does not exist yet")
	case constraint == constraintLineQtyPositive:
		return errors.Wrap(err, errors.KindInvalid, codeAmountOutOfRange,
			"the line item quantity must be positive")
	case strings.HasSuffix(constraint, constraintNonnegSuffix):
		// carts_*_nonneg, cart_line_items_*_nonneg and
		// cart_shipping_methods_amount_nonneg fall into the same class: negative
		// money.
		return errors.Wrap(err, errors.KindInvalid, codeAmountOutOfRange,
			"the amount cannot be negative (constraint: %s)", constraint)
	}
	return errors.Wrap(err, errors.KindInternal, code, format, a...)
}

// nullString turns the empty string into SQL NULL.
func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// stringValue turns SQL NULL into the empty string.
func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// toTime converts a timestamptz value into a UTC time.Time.
//
// An invalid (NULL) value returns the zero time: on NOT NULL columns this case
// does not arise, so seeing the zero time is the sign of corrupted data.
func toTime(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

// toTimePtr converts a nullable timestamptz value into a *time.Time.
func toTimePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}

// toJSONMap converts a jsonb column into a map.
//
// An empty or JSON null value returns a nil map; that way the API response omits
// the field entirely (omitempty) instead of carrying "metadata": null.
func toJSONMap(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeMetadataInvalid,
			"the JSON field could not be decoded")
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// fromJSONMap converts a map into the bytes to be written to a jsonb column.
//
// A nil map is converted to the empty object ('{}'): the column is NOT NULL and
// the difference between "no data" and "empty data" means nothing in this module.
func fromJSONMap(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, codeMetadataInvalid,
			"the JSON field could not be encoded")
	}
	return raw, nil
}

// toCart converts a database row into the domain model.
func toCart(row cartdb.Cart) (models.Cart, error) {
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.Cart{}, err
	}
	return models.Cart{
		ID:             row.ID,
		RegionID:       row.RegionID,
		CustomerID:     stringValue(row.CustomerID),
		Email:          stringValue(row.Email),
		CurrencyCode:   row.CurrencyCode,
		Subtotal:       row.Subtotal,
		DiscountTotal:  row.DiscountTotal,
		TaxTotal:       row.TaxTotal,
		ShippingTotal:  row.ShippingTotal,
		Total:          row.Total,
		Revision:       row.Revision,
		TotalsRevision: row.TotalsRevision,
		Metadata:       meta,
		CompletedAt:    toTimePtr(row.CompletedAt),
		CreatedAt:      toTime(row.CreatedAt),
		UpdatedAt:      toTime(row.UpdatedAt),
		DeletedAt:      toTimePtr(row.DeletedAt),
	}, nil
}

// toCarts converts a row slice into a domain model slice.
func toCarts(rows []cartdb.Cart) ([]models.Cart, error) {
	out := make([]models.Cart, 0, len(rows))
	// The loop is walked by index: the row structs are large and copying them by
	// value would haul a few hundred bytes for nothing on every turn.
	for i := range rows {
		cart, err := toCart(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, cart)
	}
	return out, nil
}

// toLineItem converts a database row into the domain model.
func toLineItem(row cartdb.CartLineItem) (models.LineItem, error) {
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.LineItem{}, err
	}
	return models.LineItem{
		ID:            row.ID,
		CartID:        row.CartID,
		VariantID:     row.VariantID,
		Title:         row.Title,
		Quantity:      row.Quantity,
		UnitPrice:     row.UnitPrice,
		Subtotal:      row.Subtotal,
		DiscountTotal: row.DiscountTotal,
		TaxTotal:      row.TaxTotal,
		Total:         row.Total,
		Metadata:      meta,
		CreatedAt:     toTime(row.CreatedAt),
		UpdatedAt:     toTime(row.UpdatedAt),
	}, nil
}

// toLineItems converts a row slice into a domain model slice.
func toLineItems(rows []cartdb.CartLineItem) ([]models.LineItem, error) {
	out := make([]models.LineItem, 0, len(rows))
	for i := range rows {
		item, err := toLineItem(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// toCartAddress converts a database row into the domain model.
func toCartAddress(row cartdb.CartAddress) (models.CartAddress, error) {
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.CartAddress{}, err
	}
	return models.CartAddress{
		ID:              row.ID,
		CartID:          row.CartID,
		Type:            models.AddressType(row.AddressType),
		SourceAddressID: stringValue(row.SourceAddressID),
		FirstName:       stringValue(row.FirstName),
		LastName:        stringValue(row.LastName),
		Company:         stringValue(row.Company),
		Address1:        stringValue(row.Address1),
		Address2:        stringValue(row.Address2),
		City:            stringValue(row.City),
		Province:        stringValue(row.Province),
		PostalCode:      stringValue(row.PostalCode),
		CountryCode:     stringValue(row.CountryCode),
		Phone:           stringValue(row.Phone),
		Metadata:        meta,
		CreatedAt:       toTime(row.CreatedAt),
		UpdatedAt:       toTime(row.UpdatedAt),
	}, nil
}

// toShippingMethod converts a database row into the domain model.
func toShippingMethod(row cartdb.CartShippingMethod) (models.ShippingMethod, error) {
	data, err := toJSONMap(row.Data)
	if err != nil {
		return models.ShippingMethod{}, err
	}
	return models.ShippingMethod{
		ID:               row.ID,
		CartID:           row.CartID,
		Name:             row.Name,
		ShippingOptionID: stringValue(row.ShippingOptionID),
		Amount:           row.Amount,
		Data:             data,
		CreatedAt:        toTime(row.CreatedAt),
		UpdatedAt:        toTime(row.UpdatedAt),
	}, nil
}
