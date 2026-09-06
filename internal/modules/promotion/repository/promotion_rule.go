package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository/promotiondb"
)

// CreatePromotionRule bir promosyona kural ekler; promosyon yoksa ya da
// silinmişse errors.NotFound döner.
//
// Promosyon PAYLAŞIMLI kilit altında okunur ve kural AYNI işlemde yazılır
// (bkz. [requireLivePromotion]). Kilidin burada, foreign key'in ise hiçbir
// yerde yeterli OLMADIĞININ gerekçesi orada yazılıdır: yumuşak silme satırı
// yerinde bıraktığı için FK, silinmiş bir promosyonun altına yazılan kuralı
// GEÇİRİR.
func (r *Repo) CreatePromotionRule(
	ctx context.Context,
	rule models.PromotionRule,
	now time.Time,
) (models.PromotionRule, error) {
	var out models.PromotionRule

	err := r.inTx(ctx, func(q *promotiondb.Queries) error {
		if txErr := requireLivePromotion(ctx, q, rule.PromotionID); txErr != nil {
			return txErr
		}

		row, txErr := q.InsertPromotionRule(ctx, promotiondb.InsertPromotionRuleParams{
			ID:          rule.ID,
			PromotionID: rule.PromotionID,
			RuleType:    string(rule.RuleType),
			Attribute:   rule.Attribute,
			Operator:    string(rule.Operator),
			RuleValues:  rule.Values,
			CreatedAt:   fromTime(now),
		})
		if txErr != nil {
			return wrapDB(txErr, "promosyon kuralı eklenemedi: %s", rule.PromotionID)
		}
		out = toPromotionRule(row)
		return nil
	})
	if err != nil {
		return models.PromotionRule{}, err
	}
	return out, nil
}

// GetPromotionRule kimliğe göre kuralı döner; yoksa errors.NotFound.
func (r *Repo) GetPromotionRule(ctx context.Context, id string) (models.PromotionRule, error) {
	if err := r.ready(); err != nil {
		return models.PromotionRule{}, err
	}

	row, err := r.q.GetPromotionRule(ctx, id)
	if err != nil {
		return models.PromotionRule{}, notFoundOr(err, CodePromotionRuleNotFound,
			"promosyon kuralı bulunamadı: %s", id)
	}
	return toPromotionRule(row), nil
}

// ListPromotionRules bir promosyonun canlı kurallarını döner.
func (r *Repo) ListPromotionRules(ctx context.Context, promotionID string) ([]models.PromotionRule, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.q.ListPromotionRules(ctx, promotionID)
	if err != nil {
		return nil, wrapDB(err, "promosyon kuralları alınamadı: %s", promotionID)
	}

	out := make([]models.PromotionRule, 0, len(rows))
	for i := range rows {
		out = append(out, toPromotionRule(rows[i]))
	}
	return out, nil
}

// DeletePromotionRule kuralı soft delete ile siler; yoksa errors.NotFound.
func (r *Repo) DeletePromotionRule(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	if _, err := r.q.SoftDeletePromotionRule(ctx, promotiondb.SoftDeletePromotionRuleParams{
		ID:        id,
		DeletedAt: fromTime(now),
	}); err != nil {
		return notFoundOr(err, CodePromotionRuleNotFound, "promosyon kuralı bulunamadı: %s", id)
	}
	return nil
}

// toPromotionRule üretilen satırı domain modeline çevirir.
func toPromotionRule(row promotiondb.PromotionRule) models.PromotionRule {
	return models.PromotionRule{
		ID:          row.ID,
		PromotionID: row.PromotionID,
		RuleType:    models.RuleType(row.RuleType),
		Attribute:   row.Attribute,
		Operator:    models.RuleOperator(row.Operator),
		Values:      row.RuleValues,
		CreatedAt:   toTime(row.CreatedAt),
		UpdatedAt:   toTime(row.UpdatedAt),
	}
}
