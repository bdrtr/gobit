package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository/promotiondb"
)

// CreatePromotionRule adds a rule to a promotion; if the promotion does not
// exist or has been deleted, errors.NotFound is returned.
//
// The promotion is read under a SHARED lock and the rule is written in the
// SAME transaction (see [requireLivePromotion]). The reason the lock is needed
// here, and the foreign key is NOT enough anywhere, is written there: because
// a soft delete leaves the row in place, the FK LETS THROUGH a rule written
// under a deleted promotion.
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
			return wrapDB(txErr, "the promotion rule could not be added: %s", rule.PromotionID)
		}
		out = toPromotionRule(row)
		return nil
	})
	if err != nil {
		return models.PromotionRule{}, err
	}
	return out, nil
}

// GetPromotionRule returns the rule by id; if there is none, errors.NotFound.
func (r *Repo) GetPromotionRule(ctx context.Context, id string) (models.PromotionRule, error) {
	if err := r.ready(); err != nil {
		return models.PromotionRule{}, err
	}

	row, err := r.q.GetPromotionRule(ctx, id)
	if err != nil {
		return models.PromotionRule{}, notFoundOr(err, CodePromotionRuleNotFound,
			"promotion rule not found: %s", id)
	}
	return toPromotionRule(row), nil
}

// ListPromotionRules returns a promotion's live rules.
func (r *Repo) ListPromotionRules(ctx context.Context, promotionID string) ([]models.PromotionRule, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.q.ListPromotionRules(ctx, promotionID)
	if err != nil {
		return nil, wrapDB(err, "the promotion rules could not be read: %s", promotionID)
	}

	out := make([]models.PromotionRule, 0, len(rows))
	for i := range rows {
		out = append(out, toPromotionRule(rows[i]))
	}
	return out, nil
}

// DeletePromotionRule deletes the rule with a soft delete; if there is none,
// errors.NotFound.
func (r *Repo) DeletePromotionRule(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	if _, err := r.q.SoftDeletePromotionRule(ctx, promotiondb.SoftDeletePromotionRuleParams{
		ID:        id,
		DeletedAt: fromTime(now),
	}); err != nil {
		return notFoundOr(err, CodePromotionRuleNotFound, "promotion rule not found: %s", id)
	}
	return nil
}

// toPromotionRule turns the generated row into the domain model.
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
