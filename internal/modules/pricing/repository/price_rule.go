package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/repository/pricingdb"
)

// CreatePriceRule adds a rule to a price.
//
// If the price DOES NOT EXIST AT ALL, a foreign key violation occurs and
// errors.Invalid is returned.
//
// If the price was DELETED, the foreign key STAYS SILENT and the rule is
// written: deletion is soft, the row stays in place, and the FK looks at the
// row's EXISTENCE, not at its deleted_at. For a while this comment said "a rule
// being orphaned is structurally impossible"; that was measured and turned out
// to be wrong (2026-09-06, see
// TestARuleCanBeWrittenToADeletedPriceButIsUnreachable in
// pricing_integration_test.go).
//
// NO lock was ADDED, and that is deliberate. There is no decision for a lock to
// protect: this path makes a SINGLE repository call and the service reads
// nothing beforehand, so a "read → decide → write" race cannot arise. The
// consequence of the rule written is an UNREACHABLE row as well — because the
// price itself was deleted, it does not enter the candidate query and the
// amount the customer pays does not change. The second half of the test holds
// exactly that.
func (r *Repo) CreatePriceRule(ctx context.Context, rule models.PriceRule, now time.Time) (models.PriceRule, error) {
	if err := r.ready(); err != nil {
		return models.PriceRule{}, err
	}

	var created models.PriceRule
	err := r.inTx(ctx, func(q *pricingdb.Queries) error {
		row, err := q.InsertPriceRule(ctx, pricingdb.InsertPriceRuleParams{
			ID:         rule.ID,
			PriceID:    rule.PriceID,
			Attribute:  rule.Attribute,
			Operator:   string(rule.Operator),
			RuleValues: rule.Values,
			CreatedAt:  fromTime(now),
		})
		if err != nil {
			return wrapDB(err, "the price rule could not be inserted: %s", rule.PriceID)
		}
		created = toPriceRule(row)

		// A rule takes its price out of every context that does not satisfy it,
		// the storefront's among them, so the set's history records the change
		// (ADR 0167).
		return recordSetOfPrice(ctx, q, rule.PriceID, now)
	})
	if err != nil {
		return models.PriceRule{}, err
	}
	return created, nil
}

// GetPriceRule returns the rule with the given id; errors.NotFound if there is
// none.
func (r *Repo) GetPriceRule(ctx context.Context, id string) (models.PriceRule, error) {
	if err := r.ready(); err != nil {
		return models.PriceRule{}, err
	}

	row, err := r.q.GetPriceRule(ctx, id)
	if err != nil {
		return models.PriceRule{}, notFoundOr(err, CodePriceRuleNotFound, "price rule not found: %s", id)
	}
	return toPriceRule(row), nil
}

// ListPriceRules returns a price's live rules.
func (r *Repo) ListPriceRules(ctx context.Context, priceID string) ([]models.PriceRule, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.q.ListPriceRulesByPrice(ctx, priceID)
	if err != nil {
		return nil, wrapDB(err, "the price rules could not be read: %s", priceID)
	}

	rules := make([]models.PriceRule, 0, len(rows))
	for i := range rows {
		rules = append(rules, toPriceRule(rows[i]))
	}
	return rules, nil
}

// DeletePriceRule soft-deletes the rule; errors.NotFound if there is none.
func (r *Repo) DeletePriceRule(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	return r.inTx(ctx, func(q *pricingdb.Queries) error {
		rule, err := q.GetPriceRule(ctx, id)
		if err != nil {
			return notFoundOr(err, CodePriceRuleNotFound, "price rule not found: %s", id)
		}

		if _, err := q.SoftDeletePriceRule(ctx, pricingdb.SoftDeletePriceRuleParams{
			ID:        id,
			DeletedAt: fromTime(now),
		}); err != nil {
			return notFoundOr(err, CodePriceRuleNotFound, "price rule not found: %s", id)
		}

		// The price the rule held back competes again (ADR 0167).
		return recordSetOfPrice(ctx, q, rule.PriceID, now)
	})
}

// toPriceRule turns the generated row into the domain model.
func toPriceRule(row pricingdb.PriceRule) models.PriceRule {
	return models.PriceRule{
		ID:        row.ID,
		PriceID:   row.PriceID,
		Attribute: row.Attribute,
		Operator:  models.RuleOperator(row.Operator),
		Values:    row.RuleValues,
		CreatedAt: toTime(row.CreatedAt),
		UpdatedAt: toTime(row.UpdatedAt),
	}
}
