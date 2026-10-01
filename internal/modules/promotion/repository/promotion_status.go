package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository/promotiondb"
)

// SwitchPromotionStatus moves the promotion from one status to another if it is
// still in the first, and reports whether it did; a promotion that is not, or
// does not exist, is left as it is (ADR 0312).
func (r *Repo) SwitchPromotionStatus(
	ctx context.Context, id string, from, to models.PromotionStatus, now time.Time,
) (models.Promotion, bool, error) {
	if err := r.ready(); err != nil {
		return models.Promotion{}, false, err
	}

	row, err := r.q.SwitchPromotionStatus(ctx, promotiondb.SwitchPromotionStatusParams{
		ID: id, FromStatus: string(from), ToStatus: string(to), UpdatedAt: fromTime(now),
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return models.Promotion{}, false, nil
	case err != nil:
		return models.Promotion{}, false, wrapDB(err, "the status of promotion %s could not be switched", id)
	}

	return toPromotion(row), true, nil
}
