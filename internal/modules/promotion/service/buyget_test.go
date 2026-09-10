package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// buygetCandidate builds an active buyget promotion with the counts and rules
// given.
func buygetCandidate(bps, buy, apply int64, rules ...models.PromotionRule) models.PromotionCandidate {
	method := percentageMethod("promo_bg", bps, models.TargetItems, models.AllocationEach)
	method.BuyQuantity = ptr(buy)
	method.ApplyToQuantity = ptr(apply)

	return models.PromotionCandidate{
		Promotion: models.Promotion{
			ID:          "promo_bg",
			Code:        "BUYGET",
			IsAutomatic: true,
			Type:        models.PromotionBuyGet,
			Status:      models.PromotionActive,
		},
		Method: method,
		Rules:  rules,
	}
}

// lineRule is a rule over a line attribute.
func lineRule(ruleType models.RuleType, attribute, value string) models.PromotionRule {
	return models.PromotionRule{
		RuleType:  ruleType,
		Attribute: attribute,
		Operator:  models.OpEq,
		Values:    []string{value},
	}
}

// TestTheCheapestUnitLeftOverIsTheOneRewarded is the mechanic in one case: three
// units at the same price, two of them bought, one given.
func TestTheCheapestUnitLeftOverIsTheOneRewarded(t *testing.T) {
	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{unitItem("li_1", 1000, 3, nil)},
	}

	res := computeDiscounts([]models.PromotionCandidate{buygetCandidate(10000, 2, 1)}, in)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(1000), res.DiscountTotal, "one unit of the three is free")
	require.Len(t, res.Applied, 1)
	assert.Equal(t, int64(1000), res.Applied[0].Amount)
}

// TestABoughtUnitIsNotAlsoARewardedUnit is the decision that makes "buy two, get
// one" need THREE units.
//
// The other reading — the two units both satisfying the condition and collecting
// the reward — hands the shopper two for the price of one under the same wording.
func TestABoughtUnitIsNotAlsoARewardedUnit(t *testing.T) {
	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{unitItem("li_1", 1000, 2, nil)},
	}

	res := computeDiscounts([]models.PromotionCandidate{buygetCandidate(10000, 2, 1)}, in)

	assertInvariants(t, in, res)
	assert.Zero(t, res.DiscountTotal, "both units paid for the condition; none is left to give")
	assert.Empty(t, res.Applied, "a promotion that discounted nothing is not reported as applied")
}

// TestTheBuySetAndTheRewardSetAreDifferentSets is the promotion a single rule
// type cannot express: buy a shirt, get a tie.
func TestTheBuySetAndTheRewardSetAreDifferentSets(t *testing.T) {
	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			unitItem("li_shirt", 5000, 2, map[string]string{"kind": "shirt"}),
			unitItem("li_tie", 800, 1, map[string]string{"kind": "tie"}),
		},
	}
	candidate := buygetCandidate(10000, 2, 1,
		lineRule(models.RuleBuy, "kind", "shirt"),
		lineRule(models.RuleTarget, "kind", "tie"),
	)

	res := computeDiscounts([]models.PromotionCandidate{candidate}, in)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(800), res.DiscountTotal, "the tie is free and the shirts are not touched")
	assert.Zero(t, res.Items[0].Amount, "the shirt bought the reward, it did not receive one")
	assert.Equal(t, int64(800), res.Items[1].Amount)
}

// TestTheMostExpensiveUnitsSatisfyThePurchase pins WHICH units the condition
// consumes when the two sets overlap.
//
// It is the supermarket's own rule and the merchant-safe direction: the expensive
// ones are paid for and the cheapest one is given away.
func TestTheMostExpensiveUnitsSatisfyThePurchase(t *testing.T) {
	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			unitItem("li_cheap", 400, 1, nil),
			unitItem("li_dear", 900, 2, nil),
		},
	}

	res := computeDiscounts([]models.PromotionCandidate{buygetCandidate(10000, 2, 1)}, in)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(400), res.DiscountTotal, "the two dear units were bought, the cheap one is free")
	assert.Equal(t, int64(400), res.Items[0].Amount)
	assert.Zero(t, res.Items[1].Amount)
}

// TestTheRewardLandsOnTheCheapestUnitAvailable pins the reward's own order, which
// is the opposite of the purchase's: the shopper pays for the dear ones and the
// cheap one is the gift.
func TestTheRewardLandsOnTheCheapestUnitAvailable(t *testing.T) {
	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			unitItem("li_cheap", 300, 2, nil),
			unitItem("li_dear", 900, 2, nil),
		},
	}

	res := computeDiscounts([]models.PromotionCandidate{buygetCandidate(10000, 1, 1)}, in)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(300), res.DiscountTotal,
		"one dear unit satisfied the purchase and a CHEAP unit is the reward")
	assert.Equal(t, int64(300), res.Items[0].Amount)
	assert.Zero(t, res.Items[1].Amount)
}

// TestAConditionThatWasNeverMetRewardsNothing is the guard that stands between an
// unsatisfied purchase and a reward the cart could still pay out.
//
// The reward lines are a DIFFERENT set here, so nothing else stops it: without
// the count the tie is given away to a shopper who bought no shirt.
func TestAConditionThatWasNeverMetRewardsNothing(t *testing.T) {
	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{unitItem("li_tie", 800, 1, map[string]string{"kind": "tie"})},
	}
	candidate := buygetCandidate(10000, 2, 1,
		lineRule(models.RuleBuy, "kind", "shirt"),
		lineRule(models.RuleTarget, "kind", "tie"),
	)

	res := computeDiscounts([]models.PromotionCandidate{candidate}, in)

	assertInvariants(t, in, res)
	assert.Zero(t, res.DiscountTotal, "no shirt was bought, so no tie is given")
}

// TestAFixedRewardIsCappedAtTheUnitItPays keeps a fixed amount from spilling from
// the unit it rewards onto the units beside it.
func TestAFixedRewardIsCappedAtTheUnitItPays(t *testing.T) {
	method := fixedMethod("promo_bg", 5000, models.TargetItems, models.AllocationEach)
	method.CurrencyCode = "TRY"
	method.BuyQuantity = ptr(int64(1))
	method.ApplyToQuantity = ptr(int64(1))

	candidate := buygetCandidate(0, 1, 1)
	candidate.Method = method

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{unitItem("li_1", 300, 4, nil)},
	}

	res := computeDiscounts([]models.PromotionCandidate{candidate}, in)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(300), res.DiscountTotal,
		"a 5000 discount on a 300 unit discounts 300, and the rest does not fall on the next unit")
}

// TestTheRewardStopsAtTheUnitsThatAreThere is the reward asking for more units
// than the cart can give.
func TestTheRewardStopsAtTheUnitsThatAreThere(t *testing.T) {
	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{unitItem("li_1", 1000, 3, nil)},
	}

	res := computeDiscounts([]models.PromotionCandidate{buygetCandidate(10000, 1, 9)}, in)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(2000), res.DiscountTotal, "one unit bought it, two were left to give")
}

// TestARewardWorthNothingDoesNotConsumeASlot keeps a free line from eating the
// reward and handing the shopper nothing.
func TestARewardWorthNothingDoesNotConsumeASlot(t *testing.T) {
	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			unitItem("li_free", 0, 1, nil),
			unitItem("li_paid", 700, 2, nil),
		},
	}

	res := computeDiscounts([]models.PromotionCandidate{buygetCandidate(10000, 1, 1)}, in)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(700), res.DiscountTotal,
		"the zero-priced line is met first and skipped; the reward lands on a unit that costs something")
}

// TestTheRewardIsGrantedOnce pins the ladder this mechanic does NOT climb.
//
// Six units under "buy two, get one" are one free unit and not two. The repeating
// form is a different promise and it is recorded as one (ADR 0112).
func TestTheRewardIsGrantedOnce(t *testing.T) {
	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{unitItem("li_1", 1000, 6, nil)},
	}

	res := computeDiscounts([]models.PromotionCandidate{buygetCandidate(10000, 2, 1)}, in)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(1000), res.DiscountTotal, "one reward, however large the cart")
}

// TestAPurchaseTooSmallRewardsNothing is the condition doing its job.
func TestAPurchaseTooSmallRewardsNothing(t *testing.T) {
	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{unitItem("li_1", 1000, 1, nil)},
	}

	res := computeDiscounts([]models.PromotionCandidate{buygetCandidate(10000, 3, 1)}, in)

	assertInvariants(t, in, res)
	assert.Zero(t, res.DiscountTotal)
}

// TestAHalfConfiguredMechanicIsRefusedInBothDirections is the pairing rule seen
// from the computation.
func TestAHalfConfiguredMechanicIsRefusedInBothDirections(t *testing.T) {
	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{unitItem("li_1", 1000, 3, nil)},
	}

	buygetWithoutCounts := buygetCandidate(10000, 2, 1)
	buygetWithoutCounts.Method.BuyQuantity = nil
	buygetWithoutCounts.Method.ApplyToQuantity = nil

	standardWithCounts := buygetCandidate(10000, 2, 1)
	standardWithCounts.Promotion.Type = models.PromotionStandard

	for name, candidate := range map[string]models.PromotionCandidate{
		"a buyget that never said how many":   buygetWithoutCounts,
		"a standard promotion counting units": standardWithCounts,
	} {
		t.Run(name, func(t *testing.T) {
			res := computeDiscounts([]models.PromotionCandidate{candidate}, in)

			assert.Zero(t, res.DiscountTotal, "not applying is the safe direction")
			require.Len(t, res.Skipped, 1, "and it is not silent")
			assert.Equal(t, SkipRewardMismatch, res.Skipped[0].Reason)
		})
	}
}

// TestTheLineCeilingHoldsOverAReward keeps the reward inside the invariant every
// other promotion obeys: a line's total discount never exceeds the line.
func TestTheLineCeilingHoldsOverAReward(t *testing.T) {
	full := models.PromotionCandidate{
		Promotion: models.Promotion{
			ID: "promo_all", Code: "ALL", IsAutomatic: true,
			Type: models.PromotionStandard, Status: models.PromotionActive,
		},
		Method: percentageMethod("promo_all", 10000, models.TargetItems, models.AllocationEach),
	}

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{unitItem("li_1", 1000, 3, nil)},
	}

	res := computeDiscounts([]models.PromotionCandidate{full, buygetCandidate(10000, 2, 1)}, in)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(3000), res.DiscountTotal, "the line was already free; the reward adds nothing")
	require.Len(t, res.Applied, 1, "a promotion clipped to zero is not reported as applied")
	assert.Equal(t, "promo_all", res.Applied[0].PromotionID)
}
