package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository/promotiondb"
)

// CreatePromotionRule bir promosyona kural ekler.
//
// Promosyon yoksa foreign key ihlali oluşur ve errors.Invalid dönülür; kuralın
// yetim kalması yapısal olarak imkânsızdır.
func (r *Repo) CreatePromotionRule(
	ctx context.Context,
	rule models.PromotionRule,
	now time.Time,
) (models.PromotionRule, error) {
	if err := r.ready(); err != nil {
		return models.PromotionRule{}, err
	}

	row, err := r.q.InsertPromotionRule(ctx, promotiondb.InsertPromotionRuleParams{
		ID:          rule.ID,
		PromotionID: rule.PromotionID,
		RuleType:    string(rule.RuleType),
		Attribute:   rule.Attribute,
		Operator:    string(rule.Operator),
		RuleValues:  rule.Values,
		CreatedAt:   fromTime(now),
	})
	if err != nil {
		return models.PromotionRule{}, wrapDB(err, "promosyon kuralı eklenemedi: %s", rule.PromotionID)
	}
	return toPromotionRule(row), nil
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
