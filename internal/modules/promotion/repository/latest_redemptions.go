package repository

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository/promotiondb"
)

// ListLatestRedemptions returns the promotion's latest uses, newest first,
// released ones included (ADR 0313).
func (r *Repo) ListLatestRedemptions(ctx context.Context, promotionID string, limit int32) ([]models.Redemption, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.q.ListLatestRedemptions(ctx, promotiondb.ListLatestRedemptionsParams{
		PromotionID: promotionID, Limit: limit,
	})
	if err != nil {
		return nil, wrapDB(err, "the latest uses of promotion %s could not be read", promotionID)
	}

	out := make([]models.Redemption, 0, len(rows))
	for i := range rows {
		out = append(out, toRedemption(rows[i]))
	}

	return out, nil
}
