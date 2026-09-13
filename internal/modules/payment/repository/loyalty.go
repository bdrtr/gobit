package repository

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/repository/paymentdb"
)

// The one table loyalty points are kept in (ADR 0164).
//
// It is the module's own record of what a customer earned from money that
// actually moved. Every row is written by the service's earn path, which is
// reached from the ONE function that moves a collection's totals; there is no
// other writer, and an arch gate holds that.

// AppendLoyaltyEntry adds one row to a customer's point ledger.
//
// There is no update and no delete: because the balance is the sum of the rows, a
// correction is A NEW ROW — the module's rule since migration 000003, "a money
// record is kept", and the reason a refund appends a reverse rather than editing
// the earn it is undoing.
func (r *Repository) AppendLoyaltyEntry(
	ctx context.Context, entry models.LoyaltyEntry,
) (models.LoyaltyEntry, error) {
	row, err := r.queries(ctx).InsertLoyaltyEntry(ctx, paymentdb.InsertLoyaltyEntryParams{
		ID:           entry.ID,
		CustomerID:   entry.CustomerID,
		CurrencyCode: entry.CurrencyCode,
		Points:       entry.Points,
		Kind:         entry.Kind.String(),
		Reference:    entry.Reference,
	})
	if err != nil {
		return models.LoyaltyEntry{}, classify(err, codeQueryFailed,
			"the loyalty point entry could not be written")
	}

	return toLoyaltyEntry(row), nil
}

// LoyaltyPointsForReference sums what ONE collection has already been written.
//
// It is the read that makes the write a target rather than an increment: the earn
// path computes where this collection's points should be and appends the
// difference, so being called twice for the same totals appends nothing. A
// collection with no rows has earned zero.
func (r *Repository) LoyaltyPointsForReference(
	ctx context.Context, reference string,
) (int64, error) {
	points, err := r.queries(ctx).LoyaltyPointsForReference(ctx, reference)
	if err != nil {
		return 0, classify(err, codeQueryFailed,
			"the collection's loyalty points could not be read")
	}

	return points, nil
}

// LoyaltyBalance returns a customer's points in ONE currency.
//
// A customer with no rows gets ZERO rather than an error, for
// [Repository.StoreCreditBalance]'s reason: somebody who never earned and
// somebody whose points were all reversed hold the same number of points.
func (r *Repository) LoyaltyBalance(
	ctx context.Context, customerID, currencyCode string,
) (int64, error) {
	points, err := r.queries(ctx).LoyaltyBalance(ctx, paymentdb.LoyaltyBalanceParams{
		CustomerID:   customerID,
		CurrencyCode: currencyCode,
	})
	if err != nil {
		return 0, classify(err, codeQueryFailed, "the loyalty point balance could not be read")
	}

	return points, nil
}

// ListLoyaltyEntries pages one customer's point history, newest first; the second
// value is the count of ALL their rows in that currency.
func (r *Repository) ListLoyaltyEntries(
	ctx context.Context, customerID, currencyCode string, limit, offset int64,
) ([]models.LoyaltyEntry, int64, error) {
	rows, err := r.queries(ctx).ListLoyaltyEntries(ctx, paymentdb.ListLoyaltyEntriesParams{
		CustomerID:   customerID,
		CurrencyCode: currencyCode,
		RowLimit:     limit,
		RowOffset:    offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed,
			"the loyalty point history could not be read")
	}

	total, err := r.queries(ctx).CountLoyaltyEntries(ctx, paymentdb.CountLoyaltyEntriesParams{
		CustomerID:   customerID,
		CurrencyCode: currencyCode,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed,
			"the loyalty point history could not be counted")
	}

	out := make([]models.LoyaltyEntry, 0, len(rows))
	for i := range rows {
		out = append(out, toLoyaltyEntry(rows[i]))
	}

	return out, total, nil
}

// toLoyaltyEntry turns a database row into the domain model.
func toLoyaltyEntry(row paymentdb.PaymentLoyaltyEntry) models.LoyaltyEntry {
	return models.LoyaltyEntry{
		ID:           row.ID,
		CustomerID:   row.CustomerID,
		CurrencyCode: row.CurrencyCode,
		Points:       row.Points,
		Kind:         models.LoyaltyKind(row.Kind),
		Reference:    row.Reference,
		CreatedAt:    toTime(row.CreatedAt),
	}
}
