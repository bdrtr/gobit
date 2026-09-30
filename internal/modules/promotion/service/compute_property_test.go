package service

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// propertyVariants are the variant ids items carry and rules name, few enough
// that a drawn rule matches a drawn item often.
var propertyVariants = []string{"v0", "v1", "v2", "v3"}

// drawComputeInput draws a cart the engine's own checks admit: unique ids,
// each amount its unit amount times its quantity, and the lines within the
// ceiling a computation's subtotal has. The draw goes through the service's own
// normalizer, so a cart it would refuse fails the test rather than reaching
// the engine.
func drawComputeInput(t *rapid.T) ComputeInput {
	in := ComputeInput{CurrencyCode: "TRY", At: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
	var subtotal int64
	for i := range rapid.IntRange(1, 5).Draw(t, "items") {
		quantity := rapid.Int64Range(1, 5).Draw(t, "quantity")
		ceiling := (models.MaxAmount - subtotal) / quantity
		unit := rapid.OneOf(rapid.Just(int64(0)), rapid.Int64Range(min(1, ceiling), min(9, ceiling)),
			rapid.Int64Range(min(10, ceiling), min(100_000, ceiling)), rapid.Int64Range(ceiling/2, ceiling)).Draw(t, "unit amount")
		subtotal += unit * quantity
		in.Items = append(in.Items, ComputeItem{
			ID: fmt.Sprintf("li_%d", i), UnitAmount: unit, Quantity: quantity, Amount: unit * quantity,
			Attributes: map[string]string{"variant_id": rapid.SampledFrom(propertyVariants).Draw(t, "variant")},
		})
	}
	for i := range rapid.IntRange(0, 2).Draw(t, "shipping methods") {
		in.ShippingMethods = append(in.ShippingMethods, ComputeShippingMethod{
			ID: fmt.Sprintf("sm_%d", i), Amount: rapid.Int64Range(0, 10_000).Draw(t, "shipping amount"),
		})
	}

	normalized, err := normalizeComputeInput(in, in.At)
	require.NoError(t, err, "the generator draws only carts the engine admits")

	return normalized
}

// drawCandidates draws promotions whose method and rules the promotion
// service's own builders admit; a draw they refuse is not a promotion, and is
// left out rather than handed to the engine.
//
// Each promotion draws a shape first — it applies, or it is skipped as a draft,
// for its currency or for want of its code — and applying is half of them, so
// several promotions of one kind competing for the same lines is an ordinary
// draw. A first generator drew each flag on its own, and a hundred draws did
// not always reach two applied promotions of one kind: the engine could apply
// them in the order given and the property passed.
func drawCandidates(t *rapid.T) (candidates []models.PromotionCandidate, codes []string) {
	now := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	count := rapid.OneOf(rapid.Just(0), rapid.Just(1), rapid.IntRange(2, 4), rapid.IntRange(2, 4)).Draw(t, "promotions")
	for i := range count {
		shape := rapid.SampledFrom([]string{"applies", "applies", "applies", "draft", "other currency", "no code"}).Draw(t, "shape")
		id := fmt.Sprintf("promo_%02d", i)
		in := ApplicationMethodInput{
			Type:       rapid.SampledFrom([]models.ApplicationMethodType{models.MethodFixed, models.MethodPercentage}).Draw(t, "method"),
			TargetType: rapid.SampledFrom([]models.ApplicationTargetType{models.TargetItems, models.TargetShippingMethods, models.TargetOrder}).Draw(t, "target"),
			Allocation: rapid.SampledFrom([]models.Allocation{"", models.AllocationEach, models.AllocationAcross}).Draw(t, "allocation"),
		}
		if in.Type == models.MethodPercentage {
			in.Value = rapid.Int64Range(0, models.BasisPointDenominator).Draw(t, "percentage")
		} else {
			in.Value = rapid.OneOf(rapid.Int64Range(1, 1_000), rapid.Int64Range(1, models.MaxAmount)).Draw(t, "fixed amount")
			in.CurrencyCode = "TRY"
			if shape == "other currency" {
				in.CurrencyCode = "EUR"
			}
		}
		if rapid.Bool().Draw(t, "a quantity cap") {
			quantity := rapid.Int64Range(1, 5).Draw(t, "max quantity")
			in.MaxQuantity = &quantity
		}
		promotionType := models.PromotionStandard
		if rapid.IntRange(0, 3).Draw(t, "buy and get") == 0 {
			buy, apply := rapid.Int64Range(1, 3).Draw(t, "buy"), rapid.Int64Range(1, 3).Draw(t, "get")
			in.BuyQuantity, in.ApplyToQuantity = &buy, &apply
			promotionType = models.PromotionBuyGet
		}
		method, err := buildApplicationMethod("method_"+id, id, in, now)
		if err != nil {
			continue
		}

		var rules []models.PromotionRule
		for j := range rapid.IntRange(0, 2).Draw(t, "rules") {
			rule := RuleInput{
				RuleType:  rapid.SampledFrom([]models.RuleType{models.RuleTarget, models.RuleBuy}).Draw(t, "rule type"),
				Attribute: "variant_id",
				Operator:  models.OpIn,
				Values:    rapid.SliceOfNDistinct(rapid.SampledFrom(propertyVariants), 1, 3, rapid.ID[string]).Draw(t, "rule values"),
			}
			if validateRuleInput(rule) != nil {
				continue
			}
			rules = append(rules, models.PromotionRule{
				ID: fmt.Sprintf("rule_%s_%d", id, j), PromotionID: id,
				RuleType: rule.RuleType, Attribute: rule.Attribute, Operator: rule.Operator, Values: rule.Values,
			})
		}

		promotion := models.Promotion{
			ID: id, Code: fmt.Sprintf("CODE%d", i), Type: promotionType,
			IsAutomatic: shape != "no code" && rapid.Bool().Draw(t, "automatic"),
			Status:      models.PromotionActive,
		}
		if shape == "draft" {
			promotion.Status = models.PromotionDraft
		}
		if !promotion.IsAutomatic && shape != "no code" {
			codes = append(codes, promotion.Code)
		}
		candidates = append(candidates, models.PromotionCandidate{Promotion: promotion, Method: &method, Rules: rules})
	}

	return candidates, codes
}

// TestTheDiscountEngineStaysWithinWhatItDiscounts is ADR 0249 on the discount
// engine, which ADR 0080 kept out of fuzzing because a generator would hide its
// assumptions: here every promotion is one the promotion service's builders
// admit, and every cart one the engine's checks admit. No line is discounted
// below zero or past its amount, the totals are the lines' sums and the
// applied promotions', the lines come back in the cart's order, and the answer
// does not depend on the order of the lines or of the promotions.
func TestTheDiscountEngineStaysWithinWhatItDiscounts(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := drawComputeInput(t)
		candidates, codes := drawCandidates(t)
		codes = append(codes, "NOT_A_CODE")
		in.Codes = codes

		result := computeDiscounts(candidates, in)

		require.Len(t, result.Items, len(in.Items))
		var items int64
		for i := range result.Items {
			require.Equal(t, in.Items[i].ID, result.Items[i].ID, "the lines come back in the cart's order")
			require.GreaterOrEqual(t, result.Items[i].Amount, int64(0))
			require.LessOrEqual(t, result.Items[i].Amount, in.Items[i].Amount, "line %s", in.Items[i].ID)
			items += result.Items[i].Amount
		}
		require.Len(t, result.ShippingMethods, len(in.ShippingMethods))
		var shipping int64
		for i := range result.ShippingMethods {
			require.GreaterOrEqual(t, result.ShippingMethods[i].Amount, int64(0))
			require.LessOrEqual(t, result.ShippingMethods[i].Amount, in.ShippingMethods[i].Amount)
			shipping += result.ShippingMethods[i].Amount
		}
		require.Equal(t, items, result.ItemsDiscountTotal)
		require.Equal(t, shipping, result.ShippingDiscountTotal)
		require.Equal(t, items+shipping, result.DiscountTotal)
		var applied int64
		for _, a := range result.Applied {
			require.Positive(t, a.Amount)
			applied += a.Amount
		}
		require.Equal(t, result.DiscountTotal, applied, "what the promotions gave is what the lines got")
		require.Len(t, result.Skipped, len(candidates)-countEligible(candidates, in))

		// The same cart with its lines and its promotions in another order, and
		// with its promotions reversed, which moves every pair of them.
		shuffled := in
		shuffled.Items = rapid.Permutation(in.Items).Draw(t, "lines reordered")
		reversed := slices.Clone(candidates)
		slices.Reverse(reversed)
		for _, order := range [][]models.PromotionCandidate{rapid.Permutation(candidates).Draw(t, "promotions reordered"), reversed} {
			again := computeDiscounts(order, shuffled)
			require.Equal(t, byID(result.Items), byID(again.Items), "the order of the lines changes nothing")
			require.Equal(t, result.ShippingMethods, again.ShippingMethods)
			require.Equal(t, result.Applied, again.Applied, "the order of the promotions changes nothing")
		}
	})
}

// countEligible counts the candidates the engine does not skip.
func countEligible(candidates []models.PromotionCandidate, in ComputeInput) int {
	eligible, _ := partitionCandidates(slices.Clone(candidates), in)
	return len(eligible)
}

// byID keys line discounts by line.
func byID(lines []LineDiscount) map[string]int64 {
	out := make(map[string]int64, len(lines))
	for _, line := range lines {
		out[line.ID] = line.Amount
	}
	return out
}

// TestPromotionsOfOneKindApplyInTheOrderOfTheirIDs pins the tie-break the
// property above found untested: two automatic promotions competing for one
// line apply in the order of their ids, whatever order they are handed in.
// promo_a takes 50 of the line's 100 and promo_b the 50 that is left; handed
// in as given, promo_b would take 80 first.
func TestPromotionsOfOneKindApplyInTheOrderOfTheirIDs(t *testing.T) {
	now := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	candidate := func(id string, value int64) models.PromotionCandidate {
		method, err := buildApplicationMethod("method_"+id, id, ApplicationMethodInput{
			Type: models.MethodFixed, TargetType: models.TargetItems, Value: value, CurrencyCode: "TRY",
		}, now)
		require.NoError(t, err)
		return models.PromotionCandidate{
			Promotion: models.Promotion{ID: id, Code: id, Type: models.PromotionStandard, IsAutomatic: true, Status: models.PromotionActive},
			Method:    &method,
		}
	}
	in := ComputeInput{CurrencyCode: "TRY", At: now, Items: []ComputeItem{{ID: "li_1", Amount: 100, UnitAmount: 100, Quantity: 1}}}

	result := computeDiscounts([]models.PromotionCandidate{candidate("promo_b", 80), candidate("promo_a", 50)}, in)

	require.Equal(t, []AppliedPromotion{
		{PromotionID: "promo_a", Code: "promo_a", IsAutomatic: true, Amount: 50},
		{PromotionID: "promo_b", Code: "promo_b", IsAutomatic: true, Amount: 50},
	}, result.Applied)
}
