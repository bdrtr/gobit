package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository/promotiondb"
)

// SetPromotionCampaign puts the promotion into the live campaign to, or out of
// any when to is nil, if it is still in the campaign from, and reports whether
// it did; otherwise the promotion is left as it is (ADR 0320).
func (r *Repo) SetPromotionCampaign(
	ctx context.Context, id string, from, to *string, now time.Time,
) (models.Promotion, bool, error) {
	if err := r.ready(); err != nil {
		return models.Promotion{}, false, err
	}

	row, err := r.q.SetPromotionCampaign(ctx, promotiondb.SetPromotionCampaignParams{
		ID: id, FromCampaignID: from, ToCampaignID: to, UpdatedAt: fromTime(now),
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return models.Promotion{}, false, nil
	case err != nil:
		return models.Promotion{}, false, wrapDB(err, "the campaign of promotion %s could not be set", id)
	}

	return toPromotion(row), true, nil
}
