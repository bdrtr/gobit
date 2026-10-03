package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository/promotiondb"
)

// SetApplicationMethod writes the promotion's application method; if one
// exists it OVERWRITES it.
//
// If the promotion does not exist or has been deleted, errors.NotFound is
// returned.
//
// The replacement is a single statement (upsert): "delete first, then insert"
// would leave a promotion without a method between the two statements, and a
// computation running in that gap would have produced no discount.
//
// The promotion is read under a SHARED lock and the method is written in the
// SAME transaction (see [requireLivePromotion]). The upsert being a SINGLE
// statement is not enough: what is a single statement is the write itself, not
// the knowledge that the promotion is live. A foreign key is not enough either
// — because a soft delete leaves the row in place, the FK LETS THROUGH a method
// written under a deleted promotion.
//
// Putting the condition INSIDE the upsert (INSERT ... SELECT) was rejected: the
// conflict branch (ON CONFLICT DO UPDATE) cannot see the promotion table and
// the condition could not have been written again in that branch; the two
// branches would have had different guarantees.
func (r *Repo) SetApplicationMethod(
	ctx context.Context,
	m models.ApplicationMethod,
	now time.Time,
) (models.ApplicationMethod, error) {
	var out models.ApplicationMethod

	err := r.inTx(ctx, func(q *promotiondb.Queries) error {
		if txErr := requireLivePromotion(ctx, q, m.PromotionID); txErr != nil {
			return txErr
		}

		row, txErr := q.UpsertApplicationMethod(ctx, promotiondb.UpsertApplicationMethodParams{
			ID:              m.ID,
			PromotionID:     m.PromotionID,
			Type:            string(m.Type),
			TargetType:      string(m.TargetType),
			Allocation:      string(m.Allocation),
			Value:           m.Value,
			MaxQuantity:     copyInt64(m.MaxQuantity),
			BuyQuantity:     copyInt64(m.BuyQuantity),
			ApplyToQuantity: copyInt64(m.ApplyToQuantity),
			CurrencyCode:    nilIfEmpty(m.CurrencyCode),
			CreatedAt:       fromTime(now),
		})
		if txErr != nil {
			return wrapDB(txErr, "the application method could not be written: %s", m.PromotionID)
		}
		out = toApplicationMethod(row)
		return nil
	})
	if err != nil {
		return models.ApplicationMethod{}, err
	}
	return out, nil
}

// GetApplicationMethod returns the promotion's application method; if there is
// none, errors.NotFound.
func (r *Repo) GetApplicationMethod(ctx context.Context, promotionID string) (models.ApplicationMethod, error) {
	if err := r.ready(); err != nil {
		return models.ApplicationMethod{}, err
	}

	row, err := r.q.GetApplicationMethod(ctx, promotionID)
	if err != nil {
		return models.ApplicationMethod{}, notFoundOr(err, CodeApplicationMethodNotFound,
			"the promotion has no application method: %s", promotionID)
	}
	return toApplicationMethod(row), nil
}

// DeleteApplicationMethod deletes the method with a soft delete; if there is
// none, errors.NotFound.
//
// A promotion left without a method is NOT AN ERROR: it produces no discount
// and is skipped in the computation. This is the way to disable a promotion
// temporarily without deleting it.
func (r *Repo) DeleteApplicationMethod(ctx context.Context, promotionID string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	if _, err := r.q.SoftDeleteApplicationMethod(ctx, promotiondb.SoftDeleteApplicationMethodParams{
		PromotionID: promotionID,
		DeletedAt:   fromTime(now),
	}); err != nil {
		return notFoundOr(err, CodeApplicationMethodNotFound,
			"the promotion has no application method: %s", promotionID)
	}
	return nil
}

// toApplicationMethod turns the generated row into the domain model.
func toApplicationMethod(row promotiondb.PromotionApplicationMethod) models.ApplicationMethod {
	return models.ApplicationMethod{
		ID:              row.ID,
		PromotionID:     row.PromotionID,
		Type:            models.ApplicationMethodType(row.Type),
		TargetType:      models.ApplicationTargetType(row.TargetType),
		Allocation:      models.Allocation(row.Allocation),
		Value:           row.Value,
		MaxQuantity:     copyInt64(row.MaxQuantity),
		BuyQuantity:     copyInt64(row.BuyQuantity),
		ApplyToQuantity: copyInt64(row.ApplyToQuantity),
		CurrencyCode:    deref(row.CurrencyCode),
		CreatedAt:       toTime(row.CreatedAt),
		UpdatedAt:       toTime(row.UpdatedAt),
	}
}
