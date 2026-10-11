package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/repository/paymentdb"
)

// ListSessionsForReconciliation returns the sessions a reconciler has to ASK
// the provider about: authorized but not captured locally, and in that state
// for a while.
//
// The window is a parameter rather than a constant here because the repository
// does not know how long a capture legitimately takes; the caller does. See the
// query's own documentation for why the set is exactly this one.
func (r *Repository) ListSessionsForReconciliation(
	ctx context.Context,
	unchangedSince time.Time,
	excluded []string,
	limit int32,
) ([]models.PaymentSession, error) {
	if excluded == nil {
		// A NULL array makes `<> ALL` unknown for every row; an empty one is
		// true for every row.
		excluded = []string{}
	}
	rows, err := r.queries(ctx).ListSessionsForReconciliation(ctx,
		paymentdb.ListSessionsForReconciliationParams{
			UnchangedSince:    fromTime(unchangedSince),
			ExcludedProviders: excluded,
			RowLimit:          limit,
		})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the sessions to reconcile could not be listed")
	}

	out := make([]models.PaymentSession, 0, len(rows))
	for i := range rows {
		out = append(out, toSession(rows[i]))
	}

	return out, nil
}

// ListPendingSessionsForReconciliation returns the pending sessions of the
// given providers last written in [settledAfter, unchangedSince), newest first:
// the sessions of a provider whose customer pays on its own page, where money
// can move while this module still holds the session pending (ADR 0438). See
// the query's documentation for why it reads a window.
func (r *Repository) ListPendingSessionsForReconciliation(
	ctx context.Context,
	unchangedSince, settledAfter time.Time,
	providerIDs []string,
	limit int32,
) ([]models.PaymentSession, error) {
	if len(providerIDs) == 0 {
		// No provider's customer pays on its page, so no pending session can
		// hold money; the query is not made.
		return []models.PaymentSession{}, nil
	}
	rows, err := r.queries(ctx).ListPendingSessionsForReconciliation(ctx,
		paymentdb.ListPendingSessionsForReconciliationParams{
			UnchangedSince: fromTime(unchangedSince),
			SettledAfter:   fromTime(settledAfter),
			ProviderIds:    providerIDs,
			RowLimit:       limit,
		})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the pending sessions to reconcile could not be listed")
	}

	out := make([]models.PaymentSession, 0, len(rows))
	for i := range rows {
		out = append(out, toSession(rows[i]))
	}

	return out, nil
}
