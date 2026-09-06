package repository

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/repository/invoicedb"
)

const (
	// SQLStateRetained is the code the four triggers of migration 000002 raise
	// when something tries to delete or truncate an invoice or one of its
	// lines. It is the schema half of ADR 0032.
	//
	// It is a CUSTOM code rather than 23001 (restrict_violation) on purpose.
	// 23001 is also what a genuine FOREIGN KEY ... ON DELETE RESTRICT raises,
	// so anyone mapping it onto "this is the invoice retention refusal" would
	// misclassify every real constraint failure they ever met — and would never
	// notice, because both are refusals to delete a row. PostgreSQL assigns no
	// condition class beginning with the letters G and B (its own run 00 to 58,
	// plus F0, HV, P0 and XX), so this value cannot collide with one the server
	// raises by itself.
	//
	// It is EXPORTED, unlike the other constants in this file, because the
	// module's integration test asserts the SQLSTATE that comes back from a raw
	// DELETE. That assertion is the only thing that would notice if the trigger
	// were dropped from the schema, which is the same silence that disqualified
	// REVOKE DELETE as the mechanism (see the head of migration 000002).
	SQLStateRetained = "GB001"
)

// wrapDB turns a driver error into the module's typed error.
//
// pgx.ErrNoRows becomes NOT FOUND, the retention refusal becomes FORBIDDEN and
// everything else becomes an internal fault: a missing row is an answer, a
// refusal somebody decided on is an answer, a broken connection is not.
func wrapDB(err error, code, message string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return coreerrors.NotFound(code, "%s", message)
	}

	// The module writes no DELETE at all, so nothing here is expected to hit
	// the triggers today. The mapping exists anyway, and that is the point: the
	// day somebody adds a delete path, the answer it gets is the module's
	// retention error with a sentence a controller can repeat, rather than a
	// 500 whose real reason sits only in the log.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == SQLStateRetained {
		// FORBIDDEN and not CONFLICT: a conflict invites the caller to look at
		// the current state and try again, and there is no state an invoice can
		// reach in which this succeeds. The act itself is refused.
		return coreerrors.Wrap(err, coreerrors.KindForbidden, codeRetained,
			"an issued invoice is retained and cannot be deleted (ADR 0032): %s", pgErr.Message)
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
