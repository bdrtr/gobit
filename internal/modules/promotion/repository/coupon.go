package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository/promotiondb"
)

// CreatePromotionWithMethod writes a promotion and its discount in one
// transaction (ADR 0314): a coupon whose discount is refused leaves no
// promotion behind, and one written is never without what it gives.
func (r *Repo) CreatePromotionWithMethod(
	ctx context.Context, p models.Promotion, m models.ApplicationMethod, now time.Time,
) (models.Promotion, error) {
	metadata, err := encodeMetadata(p.Metadata)
	if err != nil {
		return models.Promotion{}, err
	}

	var out models.Promotion
	err = r.inTx(ctx, func(q *promotiondb.Queries) error {
		row, txErr := q.InsertPromotion(ctx, promotiondb.InsertPromotionParams{
			ID: p.ID, Code: p.Code, IsAutomatic: p.IsAutomatic, Type: string(p.Type),
			CampaignID: copyString(p.CampaignID), Status: string(p.Status),
			UsageLimit: copyInt64(p.UsageLimit), Metadata: metadata, CreatedAt: fromTime(now),
		})
		if txErr != nil {
			// The operator chose the code; the constraint's name is not theirs.
			var pgErr *pgconn.PgError
			if errors.As(txErr, &pgErr) && pgErr.Code == sqlstateUniqueViolation {
				return errors.Conflict(CodeDuplicate, "a promotion with the code %s exists", p.Code)
			}
			return wrapDB(txErr, "the coupon %s could not be created", p.Code)
		}

		if _, txErr := q.UpsertApplicationMethod(ctx, promotiondb.UpsertApplicationMethodParams{
			ID: m.ID, PromotionID: row.ID, Type: string(m.Type), TargetType: string(m.TargetType),
			Allocation: string(m.Allocation), Value: m.Value, MaxQuantity: copyInt64(m.MaxQuantity),
			BuyQuantity: copyInt64(m.BuyQuantity), ApplyToQuantity: copyInt64(m.ApplyToQuantity),
			CurrencyCode: nilIfEmpty(m.CurrencyCode), CreatedAt: fromTime(now),
		}); txErr != nil {
			return wrapDB(txErr, "the discount of coupon %s could not be written", p.Code)
		}

		out = toPromotion(row)
		return nil
	})
	if err != nil {
		return models.Promotion{}, err
	}

	return out, nil
}
