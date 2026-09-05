package repository

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/repository/invoicedb"
)

// wrapDB turns a driver error into the module's typed error.
//
// pgx.ErrNoRows becomes NOT FOUND and everything else becomes an internal
// fault: a missing row is an answer, a broken connection is not.
func wrapDB(err error, code, message string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return coreerrors.NotFound(code, "%s", message)
	}

	return coreerrors.Wrap(err, coreerrors.KindInternal, code, "%s", message)
}

// fromTime turns a Go time into the driver's timestamp.
func fromTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// optional returns nil for the empty string.
//
// The SetInvoiceStatus query COALESCEs a null onto the existing value, so nil
// means "leave what is there" and an empty string would mean "overwrite it with
// nothing" — a difference that decides whether a cancellation erases the
// provider that transmitted the document.
func optional(v string) *string {
	if v == "" {
		return nil
	}

	return &v
}

// orEmptyMap returns an empty map for nil, so the metadata column never
// receives a JSON null.
func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}

	return m
}

// cursorBounds returns the keyset parameters, each null when the cursor names
// no position.
func cursorBounds(c corepage.Cursor) (at pgtype.Timestamptz, id *string) {
	if !c.Time.IsZero() {
		at = pgtype.Timestamptz{Time: c.Time, Valid: true}
	}
	if c.ID != "" {
		value := c.ID
		id = &value
	}

	return at, id
}

// toSeries turns a series row into the domain model.
func toSeries(row invoicedb.InvoiceSeries) models.Series {
	return models.Series{
		ID:         row.ID,
		Prefix:     row.Prefix,
		Year:       row.Year,
		LastNumber: row.LastNumber,
		CreatedAt:  row.CreatedAt.Time,
		UpdatedAt:  row.UpdatedAt.Time,
	}
}

// toInvoice turns an invoice row into the domain model, without its lines.
func toInvoice(row invoicedb.Invoice) (models.Invoice, error) {
	metadata := map[string]any{}
	if len(row.Metadata) > 0 {
		if err := json.Unmarshal(row.Metadata, &metadata); err != nil {
			return models.Invoice{}, coreerrors.Internal(codeQueryFailed,
				"the invoice metadata could not be decoded: %v", err)
		}
	}

	return models.Invoice{
		ID:           row.ID,
		Number:       row.Number,
		SeriesID:     row.SeriesID,
		Kind:         models.Kind(row.Kind),
		Status:       models.Status(row.Status),
		CurrencyCode: row.CurrencyCode,
		Seller: models.Party{
			Name:        row.SellerName,
			TaxNumber:   row.SellerTaxNumber,
			TaxOffice:   row.SellerTaxOffice,
			Email:       row.SellerEmail,
			Address:     row.SellerAddress,
			CountryCode: row.SellerCountryCode,
		},
		Buyer: models.Party{
			Name:        row.BuyerName,
			TaxNumber:   row.BuyerTaxNumber,
			TaxOffice:   row.BuyerTaxOffice,
			Email:       row.BuyerEmail,
			Address:     row.BuyerAddress,
			CountryCode: row.BuyerCountryCode,
		},
		Subtotal:      row.Subtotal,
		DiscountTotal: row.DiscountTotal,
		TaxTotal:      row.TaxTotal,
		Total:         row.Total,
		IssuedAt:      row.IssuedAt.Time,
		ProviderID:    row.ProviderID,
		ExternalID:    row.ExternalID,
		StatusReason:  row.StatusReason,
		Metadata:      metadata,
		CreatedAt:     row.CreatedAt.Time,
		UpdatedAt:     row.UpdatedAt.Time,
	}, nil
}

// toLine turns a line row into the domain model.
func toLine(row invoicedb.InvoiceLine) models.Line {
	return models.Line{
		ID:            row.ID,
		InvoiceID:     row.InvoiceID,
		Position:      row.Position,
		Description:   row.Description,
		Quantity:      row.Quantity,
		UnitPrice:     row.UnitPrice,
		Subtotal:      row.Subtotal,
		DiscountTotal: row.DiscountTotal,
		TaxRateBps:    row.TaxRateBps,
		TaxTotal:      row.TaxTotal,
		Total:         row.Total,
	}
}
