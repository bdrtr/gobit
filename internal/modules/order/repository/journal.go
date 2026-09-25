package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository/orderdb"
)

// JournalFacts reads the records the order journal is derived from, inside the
// half-open window [from, to) and in one currency when currencyCode is set
// (ADR 0188).
//
// Each of the three kinds is read with a limit of limit+1, so a caller that
// gets more than limit facts back knows the window was cut. The facts come
// back grouped by kind; ordering the whole is the caller's.
func (r *Repository) JournalFacts(
	ctx context.Context, from, to time.Time, currencyCode string, limit int32,
) ([]models.JournalFact, error) {
	rowLimit := limit + 1
	currency := nullString(currencyCode)
	fromAt, toAt := fromTimePtr(&from), fromTimePtr(&to)
	q := r.queries(ctx)

	placed, err := q.JournalOrdersPlaced(ctx, orderdb.JournalOrdersPlacedParams{
		FromAt: fromAt, ToAt: toAt, CurrencyCode: currency, RowLimit: rowLimit,
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the placed orders of the journal could not be read")
	}
	canceled, err := q.JournalOrdersCanceled(ctx, orderdb.JournalOrdersCanceledParams{
		FromAt: fromAt, ToAt: toAt, CurrencyCode: currency, RowLimit: rowLimit,
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the canceled orders of the journal could not be read")
	}
	credits, err := q.JournalCreditLines(ctx, orderdb.JournalCreditLinesParams{
		FromAt: fromAt, ToAt: toAt, CurrencyCode: currency, RowLimit: rowLimit,
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the credit lines of the journal could not be read")
	}

	out := make([]models.JournalFact, 0, len(placed)+len(canceled)+len(credits))
	for i := range placed {
		row := &placed[i]
		out = append(out, models.JournalFact{
			ID: row.ID, Kind: models.JournalOrderPlaced, OrderID: row.ID,
			OccurredAt: toTime(row.PlacedAt), CurrencyCode: row.CurrencyCode,
			Subtotal: row.Subtotal, DiscountTotal: row.DiscountTotal, TaxTotal: row.TaxTotal,
			ShippingTotal: row.ShippingTotal, Total: row.Total,
		})
	}
	for i := range canceled {
		row := &canceled[i]
		out = append(out, models.JournalFact{
			ID: row.ID, Kind: models.JournalOrderCanceled, OrderID: row.ID,
			OccurredAt: toTime(row.CanceledAt), CurrencyCode: row.CurrencyCode,
			Subtotal: row.Subtotal, DiscountTotal: row.DiscountTotal, TaxTotal: row.TaxTotal,
			ShippingTotal: row.ShippingTotal, Total: row.Total,
		})
	}
	for i := range credits {
		row := &credits[i]
		out = append(out, models.JournalFact{
			ID: row.ID, Kind: models.JournalCreditLine, OrderID: row.OrderID,
			OccurredAt: toTime(row.CreatedAt), CurrencyCode: row.CurrencyCode, Amount: row.Amount,
		})
	}

	return out, nil
}

// JournalCauses reads which order each of the given returns and claims belongs
// to (ADR 0189). An id that is neither — an exchange's, or nothing of this
// module's — has no row.
func (r *Repository) JournalCauses(ctx context.Context, ids []string) ([]models.JournalCause, error) {
	if len(ids) == 0 {
		return []models.JournalCause{}, nil
	}
	rows, err := r.queries(ctx).JournalCauses(ctx, ids)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the causes of the journal's refunds could not be read")
	}

	out := make([]models.JournalCause, 0, len(rows))
	for i := range rows {
		out = append(out, models.JournalCause{
			ID: rows[i].ID, Kind: rows[i].Kind, OrderID: rows[i].OrderID, CurrencyCode: rows[i].CurrencyCode,
		})
	}

	return out, nil
}
