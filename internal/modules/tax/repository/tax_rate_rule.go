package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
	"github.com/bdrtr/gobit/internal/modules/tax/repository/taxdb"
)

// CreateTaxRateRule adds a rule to a rate.
//
// The rate is read UNDER A LOCK and whether it is the default is checked
// there: had the check been made without the lock, an update slipping in
// between could make the rate the default and the rule would be written
// anyway. A default rate has no rules.
func (r *Repo) CreateTaxRateRule(
	ctx context.Context,
	rule models.TaxRateRule,
	now time.Time,
) (models.TaxRateRule, error) {
	if err := r.ready(); err != nil {
		return models.TaxRateRule{}, err
	}

	var out models.TaxRateRule
	err := r.WithTx(ctx, func(ctx context.Context) error {
		q := r.queries(ctx)

		rate, err := q.GetTaxRateForUpdate(ctx, rule.TaxRateID)
		if err != nil {
			return notFoundOr(err, CodeTaxRateNotFound,
				"tax rate not found: %s", rule.TaxRateID)
		}
		if rate.IsDefault {
			return errors.Conflict(CodeConstraintViolation,
				"%s is the region's DEFAULT rate and cannot have rules; "+
					"define a separate rate for a ruled rate", rule.TaxRateID)
		}
		if rate.StacksOnID != nil {
			// A rate standing on top is NOT SELECTED; it applies by expanding the
			// base. Writing a rule would tie it to a match: the rate would apply
			// only when its rule held, and that is a second scoping mechanism
			// nobody declared. The BASE decides the stack's scope.
			return errors.Conflict(CodeConstraintViolation,
				"the rate %s stands on another rate and cannot have rules; "+
					"the rules of the BASE rate decide the stack's scope", rule.TaxRateID)
		}

		row, err := q.InsertTaxRateRule(ctx, taxdb.InsertTaxRateRuleParams{
			ID:          rule.ID,
			TaxRateID:   rule.TaxRateID,
			Reference:   rule.Reference.String(),
			ReferenceID: rule.ReferenceID,
			CreatedAt:   fromTime(now),
		})
		if err != nil {
			return wrapDB(err, "the tax rule could not be inserted: %s/%s",
				rule.Reference, rule.ReferenceID)
		}

		out = toTaxRateRule(row)
		return nil
	})
	if err != nil {
		return models.TaxRateRule{}, err
	}
	return out, nil
}

// GetTaxRateRule returns the rule by id; errors.NotFound if there is none.
func (r *Repo) GetTaxRateRule(ctx context.Context, id string) (models.TaxRateRule, error) {
	if err := r.ready(); err != nil {
		return models.TaxRateRule{}, err
	}

	row, err := r.queries(ctx).GetTaxRateRule(ctx, id)
	if err != nil {
		return models.TaxRateRule{}, notFoundOr(err, CodeTaxRateRuleNotFound,
			"tax rule not found: %s", id)
	}
	return toTaxRateRule(row), nil
}

// ListTaxRateRules returns a rate's live rules.
func (r *Repo) ListTaxRateRules(ctx context.Context, rateID string) ([]models.TaxRateRule, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.queries(ctx).ListTaxRateRulesByRate(ctx, rateID)
	if err != nil {
		return nil, wrapDB(err, "the tax rules could not be read: %s", rateID)
	}
	return toTaxRateRules(rows), nil
}

// ListTaxRateRulesByRates returns the rules of several rates in a SINGLE query
// (the calculation path's way of reading; there is no N+1).
func (r *Repo) ListTaxRateRulesByRates(ctx context.Context, rateIDs []string) ([]models.TaxRateRule, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(rateIDs) == 0 {
		return []models.TaxRateRule{}, nil
	}

	rows, err := r.queries(ctx).ListTaxRateRulesByRates(ctx, rateIDs)
	if err != nil {
		return nil, wrapDB(err, "the tax rules could not be read")
	}
	return toTaxRateRules(rows), nil
}

// DeleteTaxRateRule soft-deletes the rule; errors.NotFound if there is none.
func (r *Repo) DeleteTaxRateRule(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	if _, err := r.queries(ctx).SoftDeleteTaxRateRule(ctx, taxdb.SoftDeleteTaxRateRuleParams{
		ID:        id,
		DeletedAt: fromTime(now),
	}); err != nil {
		return notFoundOr(err, CodeTaxRateRuleNotFound, "tax rule not found: %s", id)
	}
	return nil
}

// toTaxRateRule turns a generated row into the domain model.
func toTaxRateRule(row taxdb.TaxRateRule) models.TaxRateRule {
	return models.TaxRateRule{
		ID:          row.ID,
		TaxRateID:   row.TaxRateID,
		Reference:   models.RuleReference(row.Reference),
		ReferenceID: row.ReferenceID,
		CreatedAt:   toTime(row.CreatedAt),
		UpdatedAt:   toTime(row.UpdatedAt),
		DeletedAt:   toTimePtr(row.DeletedAt),
	}
}

// toTaxRateRules turns a slice of rows into domain models.
func toTaxRateRules(rows []taxdb.TaxRateRule) []models.TaxRateRule {
	out := make([]models.TaxRateRule, 0, len(rows))
	for i := range rows {
		out = append(out, toTaxRateRule(rows[i]))
	}
	return out
}
