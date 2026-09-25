package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/repository/paymentdb"
)

// JournalMovements reads the rows the payment journal is derived from, inside
// the half-open window [from, to) and in one currency when currencyCode is set
// (ADR 0186).
//
// Each of the four kinds is read with a limit of limit+1, so a caller that gets
// more than limit movements back knows the window was cut and can refuse it
// rather than export a journal missing its tail. The rows come back grouped by
// kind; ordering the whole is the caller's.
func (r *Repository) JournalMovements(
	ctx context.Context,
	from, to time.Time,
	currencyCode string,
	limit int32,
) ([]models.JournalMovement, error) {
	rowLimit := limit + 1
	currency := nullString(currencyCode)
	q := r.queries(ctx)

	captures, err := q.JournalCaptures(ctx, paymentdb.JournalCapturesParams{
		FromAt: fromTime(from), ToAt: fromTime(to), CurrencyCode: currency, RowLimit: rowLimit,
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the captures of the journal could not be read")
	}
	refunds, err := q.JournalRefunds(ctx, paymentdb.JournalRefundsParams{
		FromAt: fromTime(from), ToAt: fromTime(to), CurrencyCode: currency, RowLimit: rowLimit,
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the refunds of the journal could not be read")
	}
	issues, err := q.JournalStoreCreditIssues(ctx, paymentdb.JournalStoreCreditIssuesParams{
		FromAt: fromTime(from), ToAt: fromTime(to), CurrencyCode: currency, RowLimit: rowLimit,
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the store credit grants of the journal could not be read")
	}
	grants, err := q.JournalLoyaltyGrants(ctx, paymentdb.JournalLoyaltyGrantsParams{
		FromAt: fromTime(from), ToAt: fromTime(to), CurrencyCode: currency, RowLimit: rowLimit,
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the loyalty grants of the journal could not be read")
	}

	out := make([]models.JournalMovement, 0, len(captures)+len(refunds)+len(issues)+len(grants))
	for i := range captures {
		row := &captures[i]
		out = append(out, models.JournalMovement{
			ID: row.ID, Kind: models.JournalCapture, OccurredAt: toTime(row.CapturedAt),
			CurrencyCode: row.CurrencyCode, Amount: row.Amount, CollectionID: row.PaymentCollectionID,
			ProviderID: row.ProviderID, CustomerID: stringValue(row.CustomerID),
		})
	}
	for i := range refunds {
		row := &refunds[i]
		out = append(out, models.JournalMovement{
			ID: row.ID, Kind: models.JournalRefund, OccurredAt: toTime(row.CreatedAt),
			CurrencyCode: row.CurrencyCode, Amount: row.Amount, CollectionID: row.PaymentCollectionID,
			ProviderID: row.ProviderID, CustomerID: stringValue(row.CustomerID),
		})
	}
	for i := range issues {
		row := &issues[i]
		out = append(out, models.JournalMovement{
			ID: row.ID, Kind: models.JournalStoreCreditIssue, OccurredAt: toTime(row.CreatedAt),
			CurrencyCode: row.CurrencyCode, Amount: row.Amount, CustomerID: row.CustomerID,
		})
	}
	for i := range grants {
		row := &grants[i]
		kind := models.JournalLoyaltyEarn
		if row.Kind == "reverse" {
			kind = models.JournalLoyaltyReverse
		}
		out = append(out, models.JournalMovement{
			ID: row.ID, Kind: kind, OccurredAt: toTime(row.CreatedAt),
			CurrencyCode: row.CurrencyCode, Amount: row.Points, CustomerID: row.CustomerID,
		})
	}

	return out, nil
}

// CausedRefunds reads the refunds that name a cause inside [from, to), in one
// currency when currencyCode is set, at most limit+1 of them (ADR 0189).
func (r *Repository) CausedRefunds(
	ctx context.Context, from, to time.Time, currencyCode string, limit int32,
) ([]models.CausedRefund, error) {
	rows, err := r.queries(ctx).CausedRefunds(ctx, paymentdb.CausedRefundsParams{
		FromAt: fromTime(from), ToAt: fromTime(to), CurrencyCode: nullString(currencyCode), RowLimit: limit + 1,
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the refunds that name a cause could not be read")
	}

	out := make([]models.CausedRefund, 0, len(rows))
	for i := range rows {
		out = append(out, models.CausedRefund{
			ID: rows[i].ID, Reference: rows[i].Reference, Amount: rows[i].Amount,
			CurrencyCode: rows[i].CurrencyCode, CollectionID: rows[i].PaymentCollectionID,
			RefundedAt: toTime(rows[i].CreatedAt),
		})
	}

	return out, nil
}
