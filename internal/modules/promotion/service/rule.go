package service

import (
	"context"
	"slices"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/condition"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// RuleInput is the write input of a single promotion rule.
type RuleInput struct {
	// RuleType is what the rule looks at (context | target).
	RuleType models.RuleType
	// Attribute is the name of the field to look at in the context or on the line.
	Attribute string
	// Operator is the comparison operator.
	Operator models.RuleOperator
	// Values is the right-hand side of the comparison; it has to contain at least
	// one element.
	Values []string
}

// ApplicationMethodInput is the write input of an application method.
type ApplicationMethodInput struct {
	// Type is the measure of the discount (fixed | percentage).
	Type models.ApplicationMethodType
	// TargetType is the target of the discount (items | shipping_methods | order).
	TargetType models.ApplicationTargetType
	// Allocation is the distribution form; if it is given empty "each" is
	// assumed, and if the target is "order" it is forced to "across".
	Allocation models.Allocation
	// Value is a fixed amount (minor unit) or basis points (according to [Type]).
	Value int64
	// MaxQuantity is the maximum quantity the fixed amount is applied to; if nil,
	// unlimited.
	MaxQuantity *int64
	// BuyQuantity is the quantity that has to be bought to earn the reward; it is
	// meaningful only on a "buyget" promotion.
	BuyQuantity *int64
	// ApplyToQuantity is the quantity the reward lands on; it is meaningful only
	// on a "buyget" promotion.
	//
	// The two are given TOGETHER or not at all; the reasoning is in the godoc of
	// [models.ApplicationMethod.RewardsPurchase].
	ApplyToQuantity *int64
	// CurrencyCode is the currency of a "fixed" discount; it must not be given on
	// "percentage".
	CurrencyCode string
}

// AddPromotionRule adds a rule to a promotion; if the promotion does not exist or
// has been deleted it returns errors.NotFound.
//
// The check that the promotion is LIVE is NOT made here but in the same
// transaction as the write, under a row lock (see repository.CreatePromotionRule).
// The rejected alternative — and what this method did for a while — was to make
// the check here, with a separate read: in that form the read and the write are
// two SEPARATE autocommit statements, and a soft delete that slips in between
// lets the rule land under a deleted promotion. A foreign key does not stop this;
// a soft delete leaves the row in place (measured, 2026-09-06).
func (s *Service) AddPromotionRule(
	ctx context.Context,
	promotionID string,
	in RuleInput,
) (models.PromotionRule, error) {
	if err := s.ready(); err != nil {
		return models.PromotionRule{}, err
	}
	if err := requireID(promotionID, models.PromotionIDPrefix, "promotion id"); err != nil {
		return models.PromotionRule{}, err
	}
	if err := validateRuleInput(in); err != nil {
		return models.PromotionRule{}, err
	}

	now := s.clock()
	return s.repo.CreatePromotionRule(ctx, models.PromotionRule{
		ID:          models.NewPromotionRuleID(now),
		PromotionID: promotionID,
		RuleType:    in.RuleType,
		Attribute:   in.Attribute,
		Operator:    in.Operator,
		Values:      slices.Clone(in.Values),
		CreatedAt:   now,
		UpdatedAt:   now,
	}, now)
}

// GetPromotionRule returns the rule by id; errors.NotFound if there is none.
func (s *Service) GetPromotionRule(ctx context.Context, id string) (models.PromotionRule, error) {
	if err := s.ready(); err != nil {
		return models.PromotionRule{}, err
	}
	if err := requireID(id, models.PromotionRuleIDPrefix, "promotion rule id"); err != nil {
		return models.PromotionRule{}, err
	}
	return s.repo.GetPromotionRule(ctx, id)
}

// ListPromotionRules returns the rules of a promotion.
//
// It is for the ADMIN surface. Rules are NOT LEAKED to the customer: the
// right-hand side of a rule (e.g. the id of a customer group or a segment list)
// is business information and no endpoint on the store surface returns it.
func (s *Service) ListPromotionRules(ctx context.Context, promotionID string) ([]models.PromotionRule, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(promotionID, models.PromotionIDPrefix, "promotion id"); err != nil {
		return nil, err
	}
	// The promotion's existence is verified: if the rules of a promotion that
	// does not exist came back as an empty slice, the client would take it for
	// "it has no rules" instead of a 404.
	if _, err := s.repo.GetPromotion(ctx, promotionID); err != nil {
		return nil, err
	}
	return s.repo.ListPromotionRules(ctx, promotionID)
}

// DeletePromotionRule deletes the rule with a soft delete.
func (s *Service) DeletePromotionRule(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.PromotionRuleIDPrefix, "promotion rule id"); err != nil {
		return err
	}
	return s.repo.DeletePromotionRule(ctx, id, s.clock())
}

// SetApplicationMethod writes the promotion's application method, overwriting
// it if one exists. If the promotion does not exist or has been deleted it
// returns errors.NotFound.
//
// The check that the promotion is live is NOT made here but in the same
// transaction as the write, under a row lock; the reasoning is the same as for
// [Service.AddPromotionRule].
func (s *Service) SetApplicationMethod(
	ctx context.Context,
	promotionID string,
	in ApplicationMethodInput,
) (models.ApplicationMethod, error) {
	if err := s.ready(); err != nil {
		return models.ApplicationMethod{}, err
	}
	if err := requireID(promotionID, models.PromotionIDPrefix, "promotion id"); err != nil {
		return models.ApplicationMethod{}, err
	}

	now := s.clock()
	method, err := buildApplicationMethod(models.NewApplicationMethodID(now), promotionID, in, now)
	if err != nil {
		return models.ApplicationMethod{}, err
	}
	return s.repo.SetApplicationMethod(ctx, method, now)
}

// GetApplicationMethod returns the promotion's application method;
// errors.NotFound if there is none.
func (s *Service) GetApplicationMethod(ctx context.Context, promotionID string) (models.ApplicationMethod, error) {
	if err := s.ready(); err != nil {
		return models.ApplicationMethod{}, err
	}
	if err := requireID(promotionID, models.PromotionIDPrefix, "promotion id"); err != nil {
		return models.ApplicationMethod{}, err
	}
	return s.repo.GetApplicationMethod(ctx, promotionID)
}

// DeleteApplicationMethod deletes the method with a soft delete.
//
// A promotion left without a method produces no discount and is skipped in the
// computation; this is the way to disable a promotion temporarily without
// deleting it.
func (s *Service) DeleteApplicationMethod(ctx context.Context, promotionID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(promotionID, models.PromotionIDPrefix, "promotion id"); err != nil {
		return err
	}
	return s.repo.DeleteApplicationMethod(ctx, promotionID, s.clock())
}

// buildApplicationMethod validates the input and converts it into the domain
// model to be written.
//
// Three type-dependent rules are enforced, and all three match the CHECK
// constraints in the migration:
//
//   - "fixed" REQUIRES a currency and its value cannot exceed [models.MaxAmount].
//   - "percentage" CARRIES NO currency and its value cannot exceed
//     [models.BasisPointDenominator] (that is, 100%).
//   - If the target is "order" the allocation is "across": an order is a single
//     total and "each one separately" means nothing there.
func buildApplicationMethod(
	id, promotionID string,
	in ApplicationMethodInput,
	now time.Time,
) (models.ApplicationMethod, error) {
	if !in.Type.Valid() {
		return models.ApplicationMethod{}, errors.Invalid(CodeInvalidInput,
			"application method type is undefined: %q", string(in.Type))
	}
	if !in.TargetType.Valid() {
		return models.ApplicationMethod{}, errors.Invalid(CodeInvalidInput,
			"application target is undefined: %q", string(in.TargetType))
	}

	allocation := in.Allocation
	if allocation == "" {
		allocation = models.AllocationEach
	}
	if !allocation.Valid() {
		return models.ApplicationMethod{}, errors.Invalid(CodeInvalidInput,
			"allocation form is undefined: %q", string(in.Allocation))
	}
	if in.TargetType == models.TargetOrder {
		// REFUSING rather than silently correcting was chosen: an operator who
		// asks for "each" may believe a per-line discount will be applied to the
		// whole order, and a silent correction would keep that misconception
		// alive.
		if in.Allocation != "" && in.Allocation != models.AllocationAcross {
			return models.ApplicationMethod{}, errors.Invalid(CodeInvalidInput,
				"an order-targeted discount is applied only with the %q allocation, %q given",
				string(models.AllocationAcross), string(in.Allocation))
		}
		allocation = models.AllocationAcross
	}

	if in.MaxQuantity != nil {
		if err := validateQuantity("maximum quantity", *in.MaxQuantity); err != nil {
			return models.ApplicationMethod{}, err
		}
	}
	if err := validateRewardQuantities(in.BuyQuantity, in.ApplyToQuantity); err != nil {
		return models.ApplicationMethod{}, err
	}

	currency := ""
	switch in.Type {
	case models.MethodFixed:
		if err := validateAmount("discount amount", in.Value); err != nil {
			return models.ApplicationMethod{}, err
		}
		code, err := normalizeCurrency(in.CurrencyCode)
		if err != nil {
			return models.ApplicationMethod{}, err
		}
		currency = code
	case models.MethodPercentage:
		if in.Value < 0 || in.Value > models.BasisPointDenominator {
			return models.ApplicationMethod{}, errors.Invalid(CodeInvalidInput,
				"a percentage discount has to be in the [0, %d] basis point range, %d given",
				models.BasisPointDenominator, in.Value)
		}
		if in.CurrencyCode != "" {
			return models.ApplicationMethod{}, errors.Invalid(CodeInvalidInput,
				"a percentage discount cannot be given a currency, %q given", in.CurrencyCode)
		}
	}

	return models.ApplicationMethod{
		ID:              id,
		PromotionID:     promotionID,
		Type:            in.Type,
		TargetType:      in.TargetType,
		Allocation:      allocation,
		Value:           in.Value,
		MaxQuantity:     copyInt64(in.MaxQuantity),
		BuyQuantity:     copyInt64(in.BuyQuantity),
		ApplyToQuantity: copyInt64(in.ApplyToQuantity),
		CurrencyCode:    currency,
		CreatedAt:       now,
		UpdatedAt:       now,
	}, nil
}

// validateRewardQuantities validates the "buy X, get Y" pair of counts.
//
// The pair is given either IN FULL or NOT AT ALL. A buy quantity on its own is a
// condition without a reward and produces no discount; a reward quantity on its
// own is an unearned discount. The same pairing also stands as a CHECK in the
// migration, so a row written by hand cannot be left half-done either.
//
// The promotion's TYPE is not looked at, and cannot be: the type is in another
// table. The case where the type and the pair disagree is owned by the
// computation — a "buyget" without the pair produces no discount and says why
// ([SkipRewardMismatch]).
func validateRewardQuantities(buy, apply *int64) error {
	if (buy == nil) != (apply == nil) {
		return errors.Invalid(CodeInvalidInput,
			"the buy quantity and the reward quantity are given together; one cannot be given while the other is left empty")
	}
	if buy == nil {
		return nil
	}
	if err := validateQuantity("buy quantity", *buy); err != nil {
		return err
	}
	return validateQuantity("reward quantity", *apply)
}

// matchRules reports whether ALL of the given rules match the context.
// A promotion without rules is unconditional and always matches.
func matchRules(
	rules []models.PromotionRule, attributes map[string]string, lists map[string][]string,
) bool {
	for i := range rules {
		if !matchRule(rules[i], attributes, lists) {
			return false
		}
	}
	return true
}

// matchRule reports whether a single rule matches the context.
//
// The reading is [condition.Match]'s, shared with pricing and fulfillment (ADR
// 0396): a field ABSENT from the context does not match, even for negative
// operators such as "ne", so an empty context cannot open the segment discounts
// to everyone. A rule WITHOUT VALUES, a value no writer accepts and an
// unrecognized operator do not match and do not panic: an unreadable condition
// must NOT silently disable the rule and OPEN the discount to everyone.
//
// `any_in` reads only the context's LIST for the attribute and every other
// operator only its single value; an empty list does not match (ADR 0144).
func matchRule(
	rule models.PromotionRule, attributes map[string]string, lists map[string][]string,
) bool {
	value, ok := attributes[rule.Attribute]
	return condition.Match(condition.Operator(rule.Operator), rule.Values, value, ok, lists[rule.Attribute])
}
