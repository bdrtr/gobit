//go:build integration

package e2e

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	promotionmodels "github.com/bdrtr/gobit/internal/modules/promotion/models"
	promotionsvc "github.com/bdrtr/gobit/internal/modules/promotion/service"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// This file proves the "buy N, get M" mechanic END TO END (ADR 0112).
//
// The unit tests of the promotion service prove the arithmetic against hand-built
// candidates. What they cannot prove is the hop: the reward is measured per UNIT,
// so the cart has to send the UNIT PRICE across a JSON boundary the compiler does
// not check, and the two packages cannot import each other (ADR 0006). If the
// field drifts on either side the promotion module refuses the request — a cart
// that cannot be priced at all — and this is the file that would say so.
//
// The scenario is the plainest form of the promise: buy two shirts, get a tie.

// The manually computed amounts of the buyget scenario.
//
//	shirts: 5_000 x 2 = 10_000 subtotal, no discount (they bought the reward)
//	tie:      800 x 1 =    800 subtotal, discount 800 (it IS the reward)
//
//	tax (20%): shirts (10_000 - 0) x 20% = 2_000
//	           tie    (   800 - 800) x 20% =     0
//
//	subtotal = 10_800, discount = 800, tax = 2_000
//	total    = 10_800 - 800 + 2_000 + 0 = 12_000
const (
	buygetShirtPrice    int64 = 5_000
	buygetShirtQuantity int64 = 2
	buygetTiePrice      int64 = 800
	buygetTieQuantity   int64 = 1

	buygetSubtotal int64 = 10_800
	buygetDiscount int64 = 800
	buygetTax      int64 = 2_000
	buygetTotal    int64 = 12_000

	// buygetFreeBps is a reward of one hundred percent: the tie is free.
	buygetFreeBps int64 = 10_000
	// buygetBuyQuantity is how many shirts earn the reward.
	buygetBuyQuantity int64 = 2
	// buygetApplyQuantity is how many ties the reward pays for.
	buygetApplyQuantity int64 = 1
)

// TestBuyTwoGetOneGivesTheRewardAwayInTheCart runs the mechanic through the real
// modules: promotion decides, the cart flow carries the unit price and assembles
// the totals, tax bases itself on what is left.
func TestBuyTwoGetOneGivesTheRewardAwayInTheCart(t *testing.T) {
	ctx := t.Context()

	shirt := newVariant(ctx, t, "E2E Buyget Shirt", map[string]int64{taxedCurrency: buygetShirtPrice})
	tie := newVariant(ctx, t, "E2E Buyget Tie", map[string]int64{taxedCurrency: buygetTiePrice})

	newBuygetPromotion(ctx, t, "E2E-BUYGET-SHIRT-TIE", shirt, tie)

	cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: taxedCountry})
	require.NoError(t, err, "the cart must open")

	_, err = workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: cart.CartID, VariantID: shirt, Quantity: buygetShirtQuantity,
	})
	require.NoError(t, err, "the shirts must be addable")
	result, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: cart.CartID, VariantID: tie, Quantity: buygetTieQuantity,
	})
	require.NoError(t, err, "the tie must be addable")

	assertTotals(t, result.Totals, expectedTotal{
		subtotal: buygetSubtotal,
		discount: buygetDiscount,
		tax:      buygetTax,
		shipping: 0,
		total:    buygetTotal,
	}, "in the cart that earned a reward")

	lines := lineTotalsByID(t, result.Totals)
	for _, line := range result.Totals.Lines {
		switch line.UnitPrice {
		case buygetShirtPrice:
			assert.Zero(t, lines[line.LineItemID].DiscountTotal,
				"the shirts BOUGHT the reward; a discount on them would mean the same "+
					"units both satisfied the condition and collected the prize")
		case buygetTiePrice:
			assert.Equal(t, buygetTiePrice, lines[line.LineItemID].DiscountTotal,
				"the tie is the reward and the whole of it is given")
			assert.Zero(t, lines[line.LineItemID].TaxTotal,
				"tax is charged on what is left to pay, and nothing is left on this line")
		default:
			t.Fatalf("a line at an unexpected unit price: %d", line.UnitPrice)
		}
	}
}

// TestABuygetWithoutItsCountsChangesNothing is the other half of the pairing rule,
// seen from a real cart: a mechanic that was never finished is inert rather than
// dangerous.
func TestABuygetWithoutItsCountsChangesNothing(t *testing.T) {
	ctx := t.Context()

	shirt := newVariant(ctx, t, "E2E Halfbuilt Shirt", map[string]int64{taxedCurrency: buygetShirtPrice})

	promotion, err := promotionSvc.CreatePromotion(ctx, promotionsvc.PromotionInput{
		Code:        "E2E-BUYGET-HALFBUILT",
		IsAutomatic: true,
		Type:        promotionmodels.PromotionBuyGet,
		Status:      promotionmodels.PromotionActive,
	})
	require.NoError(t, err, "a buyget promotion can be published; the counts are the method's half")

	_, err = promotionSvc.SetApplicationMethod(ctx, promotion.ID, promotionsvc.ApplicationMethodInput{
		Type:       promotionmodels.MethodPercentage,
		TargetType: promotionmodels.TargetItems,
		Allocation: promotionmodels.AllocationEach,
		Value:      buygetFreeBps,
	})
	require.NoError(t, err, "a method with neither count is a legal row")

	cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: taxedCountry})
	require.NoError(t, err, "the cart must open")

	result, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: cart.CartID, VariantID: shirt, Quantity: buygetShirtQuantity,
	})
	require.NoError(t, err, "the line must be addable")

	assert.Zero(t, result.Totals.DiscountTotal,
		"a buyget that never said how many units it rewards gives nothing away; "+
			"a hundred percent method falling through to the standard path would have "+
			"emptied this cart")
}

// newBuygetPromotion writes a published "buy N, get M" promotion whose purchase is
// one variant and whose reward is another.
func newBuygetPromotion(ctx context.Context, t *testing.T, code, buyVariantID, rewardVariantID string) string {
	t.Helper()

	promotion, err := promotionSvc.CreatePromotion(ctx, promotionsvc.PromotionInput{
		Code:        code,
		IsAutomatic: true,
		Type:        promotionmodels.PromotionBuyGet,
		Status:      promotionmodels.PromotionActive,
	})
	require.NoError(t, err, "the fixture promotion could not be created")

	_, err = promotionSvc.SetApplicationMethod(ctx, promotion.ID, promotionsvc.ApplicationMethodInput{
		Type:            promotionmodels.MethodPercentage,
		TargetType:      promotionmodels.TargetItems,
		Allocation:      promotionmodels.AllocationEach,
		Value:           buygetFreeBps,
		BuyQuantity:     ptrOf(buygetBuyQuantity),
		ApplyToQuantity: ptrOf(buygetApplyQuantity),
	})
	require.NoError(t, err, "the fixture promotion's application method could not be written")

	_, err = promotionSvc.AddPromotionRule(ctx, promotion.ID, promotionsvc.RuleInput{
		RuleType:  promotionmodels.RuleBuy,
		Attribute: attrVariantID,
		Operator:  promotionmodels.OpIn,
		Values:    []string{buyVariantID},
	})
	require.NoError(t, err, "the fixture promotion's BUY rule could not be written")

	_, err = promotionSvc.AddPromotionRule(ctx, promotion.ID, promotionsvc.RuleInput{
		RuleType:  promotionmodels.RuleTarget,
		Attribute: attrVariantID,
		Operator:  promotionmodels.OpIn,
		Values:    []string{rewardVariantID},
	})
	require.NoError(t, err, "the fixture promotion's TARGET rule could not be written")

	return promotion.ID
}
