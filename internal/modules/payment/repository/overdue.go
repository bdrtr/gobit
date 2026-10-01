package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/repository/paymentdb"
)

// ListOverdueOfflineSessions returns, oldest first after the given key, the
// authorized sessions of the given providers opened before each provider's
// cutoff, in collections that captured nothing (ADR 0289). providerIDs and
// cutoffs are parallel.
func (r *Repository) ListOverdueOfflineSessions(
	ctx context.Context,
	providerIDs []string, cutoffs []time.Time,
	afterCreatedAt time.Time, afterID string,
	limit int32,
) ([]models.PaymentSession, error) {
	stamps := make([]pgtype.Timestamptz, 0, len(cutoffs))
	for _, cutoff := range cutoffs {
		stamps = append(stamps, fromTime(cutoff))
	}
	rows, err := r.queries(ctx).ListOverdueOfflineSessions(ctx, paymentdb.ListOverdueOfflineSessionsParams{
		ProviderIds:    providerIDs,
		Cutoffs:        stamps,
		AfterCreatedAt: fromTime(afterCreatedAt),
		AfterID:        afterID,
		RowLimit:       limit,
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the overdue offline sessions could not be listed")
	}

	out := make([]models.PaymentSession, 0, len(rows))
	for i := range rows {
		out = append(out, toSession(rows[i]))
	}

	return out, nil
}
