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
	limit int32,
) ([]models.PaymentSession, error) {
	rows, err := r.queries(ctx).ListSessionsForReconciliation(ctx,
		paymentdb.ListSessionsForReconciliationParams{
			UpdatedAt: fromTime(unchangedSince),
			Limit:     limit,
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
