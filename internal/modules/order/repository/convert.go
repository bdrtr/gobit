package repository

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository/orderdb"
)

// This file is the SINGLE place for the pgtype <-> domain model conversions
// and for the classification of driver errors.
//
// The boundary sitting here is deliberate: driver-specific types
// (pgtype.Timestamptz, []byte for jsonb, *pgconn.PgError) DO NOT LEAVE the
// repository. The service and the API layer see time.Time, map[string]any and
// errors typed by core/errors.

// Error codes. The caller can look these up with errors.CodeOf; the API layer
// passes the same codes on to the client.
const (
	codeOrderNotFound      = "order_not_found"
	codeSummaryNotFound    = "order_summary_not_found"
	codeReturnNotFound     = "order_return_not_found"
	codeExchangeNotFound   = "order_exchange_not_found"
	codeClaimNotFound      = "order_claim_not_found"
	codeDisplayIDTaken     = "order_display_id_taken"
	codeIdempotencyReplay  = "order_idempotency_key_taken"
	codeSummaryExists      = "order_summary_exists"
	codeOrderExists        = "order_already_exists"
	codeStateChanged       = "order_state_changed"
	codeTotalsInconsistent = "order_totals_inconsistent"
	codeAmountOutOfRange   = "order_amount_out_of_range"
	codeStatusInvalid      = "order_status_invalid"
	codeInconsistentState  = "order_inconsistent_state"
	codeMetadataInvalid    = "order_metadata_invalid"
	codeTxRequired         = "order_tx_required"
	codeQueryFailed        = "order_query_failed"
	codeConcurrentUpdate   = "order_concurrent_update"
)

// Constraint names; used to convert a driver error into a meaningful typed
// error. The names are EXACTLY the same as the names in the migration.
const (
	constraintOrdersPK            = "orders_pkey"
	constraintDisplayIDUniq       = "orders_display_id_uniq"
	constraintIdempotencyUniq     = "orders_idempotency_key_uniq"
	constraintSummaryOrderUniq    = "order_summaries_order_id_key"
	constraintOrderTotals         = "orders_totals_consistent"
	constraintOrderDiscount       = "orders_discount_within_subtotal"
	constraintLineTotals          = "order_line_items_totals_consistent"
	constraintLineDiscount        = "order_line_items_discount_within_subtotal"
	constraintLineQtyPositive     = "order_line_items_quantity_positive"
	constraintRefundWithinPaid    = "order_summaries_refund_within_paid"
	constraintOrdersCanceledStamp = "orders_canceled_stamp"
	constraintOrdersCompleteStamp = "orders_completed_stamp"
	// constraintStatusSuffix is the common suffix of every CHECK constraint that
	// enforces a status set (orders_status_valid, order_returns_status_valid …);
	// instead of listing them one by one, they are recognized by the suffix.
	constraintStatusSuffix = "_status_valid"
	// constraintNonnegSuffix is the common suffix of every CHECK constraint that
	// forbids negative money.
	constraintNonnegSuffix = "_nonneg"
	// constraintOrderFKSuffix is the common suffix of the foreign key names of
	// every child table that links to the order.
	constraintOrderFKSuffix = "_order_id_fkey"
)

// PostgreSQL SQLSTATE codes.
const (
	sqlStateUniqueViolation     = "23505"
	sqlStateForeignKeyViolation = "23503"
	sqlStateCheckViolation      = "23514"
	sqlStateDeadlockDetected    = "40P01"
)

// classify converts a driver error into a typed error.
//
// Uniqueness, foreign key and CHECK violations are situations the client (or
// the workflow) can correct; left unclassified they would all show up as 500
// and the real cause would stay in the log alone. A deadlock is handled
// separately for the same reason: there is nothing wrong with the operation
// itself, it CAN BE RETRIED.
func classify(err error, code, format string, a ...any) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return errors.Wrap(err, errors.KindInternal, code, format, a...)
	}

	switch pgErr.Code {
	case sqlStateUniqueViolation:
		return classifyUnique(err, pgErr.ConstraintName, code, format, a...)
	case sqlStateForeignKeyViolation:
		// A line, a summary or a return record cannot be linked to an order that
		// DOES NOT EXIST.
		if strings.HasSuffix(pgErr.ConstraintName, constraintOrderFKSuffix) {
			return errors.Wrap(err, errors.KindNotFound, codeOrderNotFound, "order not found")
		}
	case sqlStateCheckViolation:
		return classifyCheck(err, pgErr.ConstraintName, code, format, a...)
	case sqlStateDeadlockDetected:
		// Because the lock ordering is uniform this does not occur in normal
		// flows; this is the last line of defense. The transaction has been rolled
		// back, the same request can be retried as it is — which is why this is
		// Conflict and not Internal (500).
		return errors.Wrap(err, errors.KindConflict, codeConcurrentUpdate,
			"conflicted with a concurrent transaction; the request can be retried")
	}
	return errors.Wrap(err, errors.KindInternal, code, format, a...)
}

// classifyUnique converts uniqueness violations into typed errors.
func classifyUnique(err error, constraint, code, format string, a ...any) error {
	switch constraint {
	case constraintDisplayIDUniq:
		// A sequence does not collide; landing here means the sequence was rewound
		// by hand (setval) or that a record was copied. Catching it before the
		// order is written is the reason this constraint exists.
		return errors.Wrap(err, errors.KindConflict, codeDisplayIDTaken,
			"the order number is already in use; the display_id sequence may have been changed by hand")
	case constraintIdempotencyUniq:
		return errors.Wrap(err, errors.KindConflict, codeIdempotencyReplay,
			"an order with this idempotency key already exists")
	case constraintSummaryOrderUniq:
		return errors.Wrap(err, errors.KindConflict, codeSummaryExists,
			"the order already has a summary")
	case constraintOrdersPK:
		return errors.Wrap(err, errors.KindConflict, codeOrderExists,
			"an order with this identifier already exists")
	}
	return errors.Wrap(err, errors.KindInternal, code, format, a...)
}

// classifyCheck converts CHECK constraint violations into typed errors.
//
// The totals identity violation is kept apart: the service does the same check
// FIRST with a more readable error, so landing here means the check was skipped
// (or that SQL was applied directly) and the message must say so.
func classifyCheck(err error, constraint, code, format string, a ...any) error {
	switch {
	case constraint == constraintOrderTotals:
		return errors.Wrap(err, errors.KindInvalid, codeTotalsInconsistent,
			"order totals are inconsistent: total = subtotal - discount_total + tax_total + shipping_total must hold")
	case constraint == constraintLineTotals:
		return errors.Wrap(err, errors.KindInvalid, codeTotalsInconsistent,
			"line totals are inconsistent: total = subtotal - discount_total + tax_total must hold")
	case constraint == constraintOrderDiscount, constraint == constraintLineDiscount:
		return errors.Wrap(err, errors.KindInvalid, codeTotalsInconsistent,
			"the discount cannot exceed the subtotal (constraint: %s)", constraint)
	case constraint == constraintLineQtyPositive:
		return errors.Wrap(err, errors.KindInvalid, codeAmountOutOfRange,
			"the line quantity must be positive")
	case constraint == constraintRefundWithinPaid:
		return errors.Wrap(err, errors.KindInvalid, codeAmountOutOfRange,
			"the refunded amount cannot exceed the amount collected")
	case constraint == constraintOrdersCanceledStamp, constraint == constraintOrdersCompleteStamp:
		// The status and the stamp can only diverge through direct SQL
		// intervention; no path in the service can violate this constraint.
		return errors.Wrap(err, errors.KindInternal, codeInconsistentState,
			"the order status and the timestamp are inconsistent (constraint: %s)", constraint)
	case strings.HasSuffix(constraint, constraintStatusSuffix):
		return errors.Wrap(err, errors.KindInvalid, codeStatusInvalid,
			"undefined status value (constraint: %s)", constraint)
	case strings.HasSuffix(constraint, constraintNonnegSuffix):
		return errors.Wrap(err, errors.KindInvalid, codeAmountOutOfRange,
			"the amount cannot be negative (constraint: %s)", constraint)
	}
	return errors.Wrap(err, errors.KindInternal, code, format, a...)
}

// nullString converts an empty string to SQL NULL.
func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// stringValue converts SQL NULL to an empty string.
func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// toTime converts a timestamptz value to a UTC time.Time.
//
// An invalid (NULL) value returns the zero time: in NOT NULL columns that case
// does not arise, so seeing the zero time is the sign of corrupted data.
func toTime(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

// toTimePtr converts a nullable timestamptz value to a *time.Time.
func toTimePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}

// toJSONMap converts a jsonb column to a map.
//
// An empty or JSON null value returns a nil map; that way the field does not
// appear at all in the API response instead of "metadata": null (omitempty).
func toJSONMap(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeMetadataInvalid,
			"could not decode JSON field")
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// fromJSONMap converts a map to the bytes to be written into a jsonb column.
//
// A nil map is converted to an empty object ('{}'): the column is NOT NULL and
// the distinction between "no data" and "empty data" means nothing in this
// module.
func fromJSONMap(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, codeMetadataInvalid,
			"could not encode JSON field")
	}
	return raw, nil
}

// toOrder converts a database row into the domain model.
func toOrder(row orderdb.Order) (models.Order, error) {
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.Order{}, err
	}
	return models.Order{
		ID:             row.ID,
		DisplayID:      row.DisplayID,
		Status:         models.OrderStatus(row.Status),
		RegionID:       row.RegionID,
		CustomerID:     stringValue(row.CustomerID),
		Email:          stringValue(row.Email),
		CurrencyCode:   row.CurrencyCode,
		CartID:         stringValue(row.CartID),
		IdempotencyKey: stringValue(row.IdempotencyKey),
		Subtotal:       row.Subtotal,
		DiscountTotal:  row.DiscountTotal,
		TaxTotal:       row.TaxTotal,
		ShippingTotal:  row.ShippingTotal,
		Total:          row.Total,
		Metadata:       meta,
		PlacedAt:       toTime(row.PlacedAt),
		CompletedAt:    toTimePtr(row.CompletedAt),
		CanceledAt:     toTimePtr(row.CanceledAt),
		CancelReason:   stringValue(row.CancelReason),
		CreatedAt:      toTime(row.CreatedAt),
		UpdatedAt:      toTime(row.UpdatedAt),
		DeletedAt:      toTimePtr(row.DeletedAt),
	}, nil
}

// toOrders converts a row slice into a domain model slice.
func toOrders(rows []orderdb.Order) ([]models.Order, error) {
	out := make([]models.Order, 0, len(rows))
	// The loop walks by index: the row structs are large and copying them by
	// value would move a few hundred bytes for nothing on every pass.
	for i := range rows {
		order, err := toOrder(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, order)
	}
	return out, nil
}

// toLineItem converts a database row into the domain model.
func toLineItem(row orderdb.OrderLineItem) (models.OrderLineItem, error) {
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.OrderLineItem{}, err
	}
	return models.OrderLineItem{
		ID:            row.ID,
		OrderID:       row.OrderID,
		VariantID:     row.VariantID,
		Title:         row.Title,
		Quantity:      row.Quantity,
		UnitPrice:     row.UnitPrice,
		Subtotal:      row.Subtotal,
		DiscountTotal: row.DiscountTotal,
		TaxTotal:      row.TaxTotal,
		TaxRateBps:    row.TaxRateBps,
		Total:         row.Total,
		Metadata:      meta,
		CreatedAt:     toTime(row.CreatedAt),
		UpdatedAt:     toTime(row.UpdatedAt),
	}, nil
}

// toLineItems converts a row slice into a domain model slice.
func toLineItems(rows []orderdb.OrderLineItem) ([]models.OrderLineItem, error) {
	out := make([]models.OrderLineItem, 0, len(rows))
	for i := range rows {
		item, err := toLineItem(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// toSummary converts a database row into the domain model.
func toSummary(row orderdb.OrderSummary) models.OrderSummary {
	return models.OrderSummary{
		ID:            row.ID,
		OrderID:       row.OrderID,
		PaidTotal:     row.PaidTotal,
		RefundedTotal: row.RefundedTotal,
		CreatedAt:     toTime(row.CreatedAt),
		UpdatedAt:     toTime(row.UpdatedAt),
	}
}

// toReturn converts a database row into the domain model.
func toReturn(row orderdb.OrderReturn) (models.Return, error) {
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.Return{}, err
	}
	return models.Return{
		ID:                 row.ID,
		OrderID:            row.OrderID,
		Status:             models.ReturnStatus(row.Status),
		RefundAmount:       row.RefundAmount,
		Reason:             stringValue(row.Reason),
		Note:               stringValue(row.Note),
		Metadata:           meta,
		ReceivedAt:         toTimePtr(row.ReceivedAt),
		ReceivedLocationID: stringValue(row.ReceivedLocationID),
		CanceledAt:         toTimePtr(row.CanceledAt),
		CreatedAt:          toTime(row.CreatedAt),
		UpdatedAt:          toTime(row.UpdatedAt),
	}, nil
}

// toReturns converts a row slice into a domain model slice.
func toReturns(rows []orderdb.OrderReturn) ([]models.Return, error) {
	out := make([]models.Return, 0, len(rows))
	for i := range rows {
		item, err := toReturn(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// toExchange converts a database row into the domain model.
func toExchange(row orderdb.OrderExchange) (models.Exchange, error) {
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.Exchange{}, err
	}
	return models.Exchange{
		ID:            row.ID,
		OrderID:       row.OrderID,
		Status:        models.ExchangeStatus(row.Status),
		DifferenceDue: row.DifferenceDue,
		Note:          stringValue(row.Note),
		Metadata:      meta,
		CompletedAt:   toTimePtr(row.CompletedAt),
		CanceledAt:    toTimePtr(row.CanceledAt),
		CreatedAt:     toTime(row.CreatedAt),
		UpdatedAt:     toTime(row.UpdatedAt),
	}, nil
}

// toExchanges converts a row slice into a domain model slice.
func toExchanges(rows []orderdb.OrderExchange) ([]models.Exchange, error) {
	out := make([]models.Exchange, 0, len(rows))
	for i := range rows {
		item, err := toExchange(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// toClaim converts a database row into the domain model.
func toClaim(row orderdb.OrderClaim) (models.Claim, error) {
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.Claim{}, err
	}
	return models.Claim{
		ID:           row.ID,
		OrderID:      row.OrderID,
		Type:         models.ClaimType(row.ClaimType),
		Status:       models.ClaimStatus(row.Status),
		RefundAmount: row.RefundAmount,
		Reason:       stringValue(row.Reason),
		Note:         stringValue(row.Note),
		Metadata:     meta,
		CompletedAt:  toTimePtr(row.CompletedAt),
		CanceledAt:   toTimePtr(row.CanceledAt),
		CreatedAt:    toTime(row.CreatedAt),
		UpdatedAt:    toTime(row.UpdatedAt),
	}, nil
}

// toClaims converts a row slice into a domain model slice.
func toClaims(rows []orderdb.OrderClaim) ([]models.Claim, error) {
	out := make([]models.Claim, 0, len(rows))
	for i := range rows {
		item, err := toClaim(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}
