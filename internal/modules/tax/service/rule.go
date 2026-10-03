package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// CreateRateRuleInput is the write input of a new tax rule.
type CreateRateRuleInput struct {
	// TaxRateID is the rate the rule is bound to; it is required.
	TaxRateID string
	// Reference is the kind of item: "product", "tax_class", "product_type" or
	// "shipping_option".
	Reference string
	// ReferenceID is the id of that kind; it is required.
	ReferenceID string
}

// CreateRateRule adds a rule to a rate.
//
// A rule NARROWS the rate's SCOPE: a ruled rate applies only to the line item
// that matches its rule. That is why a rule CANNOT BE ADDED to a DEFAULT rate
// (errors.Conflict) — had "a rate that applies to everything" and "a rate that
// applies only to what matches" been combined in the same row, the rate's
// scope would become unreadable. The check is made in the repository layer,
// under the rate's row LOCK.
//
// # ReferenceID is not validated
//
// The id belongs to other modules (product, fulfillment) and this module does
// not know them (ADR 0001, Principle 2.2). A rule written for an id that does
// not exist is harmless: no line item ever enters the calculation with that
// id, so the rule never matches.
func (s *Service) CreateRateRule(ctx context.Context, in CreateRateRuleInput) (models.TaxRateRule, error) {
	if err := s.ready(); err != nil {
		return models.TaxRateRule{}, err
	}
	if err := requireID(in.TaxRateID, models.TaxRateIDPrefix, "tax rate id"); err != nil {
		return models.TaxRateRule{}, err
	}

	reference := models.RuleReference(in.Reference)
	if !reference.Valid() {
		return models.TaxRateRule{}, errors.Invalid(CodeInvalidInput,
			"the rule reference has to be %q, %q, %q or %q; %q was given",
			models.ReferenceProduct, models.ReferenceTaxClass, models.ReferenceProductType,
			models.ReferenceShippingOption, in.Reference)
	}
	if err := requireReferenceID(in.ReferenceID); err != nil {
		return models.TaxRateRule{}, err
	}

	now := s.clock()
	return s.repo.CreateTaxRateRule(ctx, models.TaxRateRule{
		ID:          models.NewTaxRateRuleID(now),
		TaxRateID:   in.TaxRateID,
		Reference:   reference,
		ReferenceID: in.ReferenceID,
	}, now)
}

// ListRateRules returns a rate's rules.
func (s *Service) ListRateRules(ctx context.Context, rateID string) ([]models.TaxRateRule, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(rateID, models.TaxRateIDPrefix, "tax rate id"); err != nil {
		return nil, err
	}
	if _, err := s.repo.GetTaxRate(ctx, rateID); err != nil {
		return nil, err
	}
	return s.repo.ListTaxRateRules(ctx, rateID)
}

// DeleteRateRule soft-deletes the rule; errors.NotFound if there is none.
//
// A rate left without rules matches no line item and is effectively dead; it
// does NOT BECOME the DEFAULT. This is deliberate — a rate starting to apply to
// every line item in the cart because its last rule was deleted would be a
// silent change that is expensive to undo.
func (s *Service) DeleteRateRule(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.TaxRateRuleIDPrefix, "tax rate rule id"); err != nil {
		return err
	}
	return s.repo.DeleteTaxRateRule(ctx, id, s.clock())
}
