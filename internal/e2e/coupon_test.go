//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	cartsvc "github.com/bdrtr/gobit/internal/modules/cart/service"
	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	promotionmodels "github.com/bdrtr/gobit/internal/modules/promotion/models"
	promotionsvc "github.com/bdrtr/gobit/internal/modules/promotion/service"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// This file proves the COUPON leg: a code the customer types lowers the cart's
// total, and the order SPENDS it.
//
// Both halves need the real modules. The cart flow cannot import promotion
// (ADR 0006), so the coupon check, the code carried into the discount request
// and the redemption at order time all travel over JSON schemas declared twice.
// The unit tests on either side read their own copy; only this file makes them
// meet.
//
// The amounts of the coupon scenario, computed by hand:
//
//	unit price 10_000, quantity 1
//	subtotal 10_000
//	discount 10_000 x 25% = 2_500
//	tax base 10_000 - 2_500 = 7_500
//	tax      7_500 x 20% = 1_500
//	total    10_000 - 2_500 + 1_500 = 9_000
const (
	couponUnitPrice = int64(10_000)
	couponRateBps   = int64(2_500)
	couponSubtotal  = couponUnitPrice
	couponDiscount  = int64(2_500)
	couponTax       = int64(1_500)
	couponTotal     = int64(9_000)
	couponStock     = int64(5)
)

// TestATypedCouponLowersTheCartAndIsSpentByTheOrder is the whole path.
//
// Until 2026-09-10 the cart had nowhere to keep a code: the discount request
// carried a "codes" array that was always empty, so only automatic promotions
// could reach a cart and no coupon was ever spent (ADR 0109).
func TestATypedCouponLowersTheCartAndIsSpentByTheOrder(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Coupon Product", map[string]int64{
		taxedCurrency: couponUnitPrice,
	}, couponStock)

	code := fmt.Sprintf("E2E-COUPON-%d", fixtureCounter.Add(1))
	promotionID := newCouponPromotion(ctx, t, code, couponRateBps, []string{variantID})

	cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{
		CountryCode: taxedCountry, CustomerID: customerID,
	})
	require.NoError(t, err, "the cart must open")

	added, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: cart.CartID, VariantID: variantID, Quantity: 1,
	})
	require.NoError(t, err, "the line must be addable")
	require.Zero(t, added.Totals.DiscountTotal,
		"precondition: the promotion needs a CODE, so it must not apply on its own")

	// --- 1) the code lowers the total ---

	require.NoError(t, workflows.ApplyPromotionCode(ctx, cart.CartID, code),
		"the coupon must be applicable")

	totals, err := workflows.CalculateTotals(ctx, cart.CartID)
	require.NoError(t, err, "the cart must be priceable after the coupon")

	assertTotals(t, totals, expectedTotal{
		subtotal: couponSubtotal,
		discount: couponDiscount,
		tax:      couponTax,
		shipping: 0,
		total:    couponTotal,
	}, "in the cart with the typed coupon")

	// --- 2) the round says WHICH promotion produced the discount ---

	require.Len(t, totals.Applied, 1,
		"the round has to report the promotion, because the order spends it BY ID")
	assert.Equal(t, promotionID, totals.Applied[0].PromotionID)
	assert.Equal(t, code, totals.Applied[0].Code)
	assert.Equal(t, couponDiscount, totals.Applied[0].Amount,
		"the amount the order will book is the one the customer was shown")

	// --- 3) the calculation left the counter UNSPENT ---

	promotion, err := promotionSvc.GetPromotion(ctx, promotionID)
	require.NoError(t, err)
	require.Zero(t, promotion.UsageCount,
		"pricing a cart must not spend a coupon; only the order does")

	// --- 4) the order spends it ---

	placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cart.CartID,
		LocationID:        stockLocationID,
		PaymentProviderID: paymentmanual.ID,
		PaymentData:       paymentBehavior(t, paymentmanual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     couponTotal,
	})
	require.NoError(t, err, "the order must be placeable at the discounted total")
	assert.Equal(t, couponTotal, placed.Amount, "and the card is charged the discounted amount")

	spent, err := promotionSvc.GetPromotion(ctx, promotionID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), spent.UsageCount,
		"the order has to spend the coupon; a counter that never moves makes a "+
			"single-use coupon usable forever")
}

// TestACouponThatIsNotACouponIsRefusedAndNotWritten is the check that happens
// BEFORE the code is stored.
func TestACouponThatIsNotACouponIsRefusedAndNotWritten(t *testing.T) {
	ctx := t.Context()

	variantID := newVariant(ctx, t, "E2E Coupon Refusal", map[string]int64{
		taxedCurrency: couponUnitPrice,
	})

	cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: taxedCountry})
	require.NoError(t, err)
	_, err = workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: cart.CartID, VariantID: variantID, Quantity: 1,
	})
	require.NoError(t, err)

	err = workflows.ApplyPromotionCode(ctx, cart.CartID, "E2E-NO-SUCH-COUPON")

	require.Error(t, err, "a code that names no usable promotion must be refused")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	detail, err := cartSvc.GetCart(ctx, cart.CartID)
	require.NoError(t, err)
	assert.Empty(t, detail.PromotionCodes, "and it must not be on the cart")
}

// TestACartFilledToTheCouponCeilingCanStillBePriced binds the two limits.
//
// [cartsvc.MaxPromotionCodes] restates the promotion module's own
// MaxCodesPerCompute, because the cart cannot import it. Nothing in the compiler
// holds them together; what does is this: a cart filled to the cart's ceiling has
// to be one the discount round still accepts.
func TestACartFilledToTheCouponCeilingCanStillBePriced(t *testing.T) {
	ctx := t.Context()

	variantID := newVariant(ctx, t, "E2E Coupon Ceiling", map[string]int64{
		taxedCurrency: couponUnitPrice,
	})

	cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: taxedCountry})
	require.NoError(t, err)
	_, err = workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: cart.CartID, VariantID: variantID, Quantity: 1,
	})
	require.NoError(t, err)

	seq := fixtureCounter.Add(1)
	for i := range cartsvc.MaxPromotionCodes {
		code := fmt.Sprintf("E2E-CEIL-%d-%02d", seq, i)
		newCouponPromotion(ctx, t, code, 100, []string{variantID})
		require.NoError(t, workflows.ApplyPromotionCode(ctx, cart.CartID, code),
			"coupon %d must be applicable", i)
	}

	totals, err := workflows.CalculateTotals(ctx, cart.CartID)
	require.NoError(t, err,
		"a cart holding the cart module's MAXIMUM number of codes must still be "+
			"priceable; if the discount round's own limit were lower the cart could "+
			"never be bought")
	assert.Positive(t, totals.DiscountTotal)
}

// newCouponPromotion sets up a promotion that needs a CODE and returns its id.
//
// The target rule is mandatory for [newAutomaticPercentagePromotion]'s reason:
// the tests share one database.
func newCouponPromotion(
	ctx context.Context,
	t *testing.T,
	code string,
	rateBps int64,
	variantIDs []string,
) string {
	t.Helper()

	promotion, err := promotionSvc.CreatePromotion(ctx, promotionsvc.PromotionInput{
		Code:        code,
		IsAutomatic: false,
		Status:      promotionmodels.PromotionActive,
	})
	require.NoError(t, err, "the fixture coupon could not be created")
	require.False(t, promotion.IsAutomatic,
		"the promotion must need a CODE; an automatic one would apply without being typed "+
			"and the test would prove nothing about the coupon path")

	_, err = promotionSvc.SetApplicationMethod(ctx, promotion.ID, promotionsvc.ApplicationMethodInput{
		Type:       promotionmodels.MethodPercentage,
		TargetType: promotionmodels.TargetItems,
		Allocation: promotionmodels.AllocationEach,
		Value:      rateBps,
	})
	require.NoError(t, err, "the fixture coupon's application method could not be written")

	_, err = promotionSvc.AddPromotionRule(ctx, promotion.ID, promotionsvc.RuleInput{
		RuleType:  promotionmodels.RuleTarget,
		Attribute: attrVariantID,
		Operator:  promotionmodels.OpIn,
		Values:    variantIDs,
	})
	require.NoError(t, err, "the fixture coupon's target rule could not be written")

	return promotion.ID
}

// TestACartsOwnDataCanRuleAPromotion is the hook an embedder had no way to
// reach.
//
// The discount engine's rule context was built from two names the cart flow
// decides — the region and the customer's group — and `internal/app.Options`
// takes only Modules and Plugins, so nobody could add a third. A shop selling two
// brands from one installation could not write "10% off, brand A only" without a
// column in the cart module for a concept that module has never heard of.
//
// It needs the real modules: the context crosses to promotion as JSON, the rule
// is matched by promotion's own engine, and the prefix that keeps the bag from
// shadowing the fixed names is decided on the cart side.
func TestACartsOwnDataCanRuleAPromotion(t *testing.T) {
	ctx := t.Context()

	variantID := newVariant(ctx, t, "E2E Metadata Ruled", map[string]int64{
		taxedCurrency: couponUnitPrice,
	})

	brand := fmt.Sprintf("brand-%d", fixtureCounter.Add(1))
	promotionID := newContextRuledPromotion(ctx, t, couponRateBps,
		cartwf.CartAttributePrefix+"brand", brand, []string{variantID})

	// --- the cart that carries the brand gets the discount ---

	ruled, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{
		CountryCode: taxedCountry,
		Metadata:    json.RawMessage(`{"brand":"` + brand + `"}`),
	})
	require.NoError(t, err, "the cart must open")

	added, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: ruled.CartID, VariantID: variantID, Quantity: 1,
	})
	require.NoError(t, err)
	require.Equal(t, couponDiscount, added.Totals.DiscountTotal,
		"the promotion is ruled on the cart's OWN data and this cart carries it")

	// --- and a cart that does not carry it gets nothing ---

	plain, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: taxedCountry})
	require.NoError(t, err)

	bare, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: plain.CartID, VariantID: variantID, Quantity: 1,
	})
	require.NoError(t, err)
	assert.Zero(t, bare.Totals.DiscountTotal,
		"a cart missing the attribute must not match: the engine's own rule is that "+
			"an absent attribute does not match, and that is what keeps a segment "+
			"discount from opening to everyone")

	promotion, err := promotionSvc.GetPromotion(ctx, promotionID)
	require.NoError(t, err)
	require.Zero(t, promotion.UsageCount, "pricing spends nothing")
}

// newContextRuledPromotion sets up an automatic promotion ruled on a CONTEXT
// attribute and returns its id.
//
// It carries a target rule as well, for [newAutomaticPercentagePromotion]'s
// reason: the tests share one database and a promotion that targets nothing lands
// on every cart in the suite.
func newContextRuledPromotion(
	ctx context.Context,
	t *testing.T,
	rateBps int64,
	attribute, value string,
	variantIDs []string,
) string {
	t.Helper()

	promotion, err := promotionSvc.CreatePromotion(ctx, promotionsvc.PromotionInput{
		Code:        fmt.Sprintf("E2E-CTX-%d", fixtureCounter.Add(1)),
		IsAutomatic: true,
		Status:      promotionmodels.PromotionActive,
	})
	require.NoError(t, err)

	_, err = promotionSvc.SetApplicationMethod(ctx, promotion.ID, promotionsvc.ApplicationMethodInput{
		Type:       promotionmodels.MethodPercentage,
		TargetType: promotionmodels.TargetItems,
		Allocation: promotionmodels.AllocationEach,
		Value:      rateBps,
	})
	require.NoError(t, err)

	_, err = promotionSvc.AddPromotionRule(ctx, promotion.ID, promotionsvc.RuleInput{
		RuleType:  promotionmodels.RuleContext,
		Attribute: attribute,
		Operator:  promotionmodels.OpEq,
		Values:    []string{value},
	})
	require.NoError(t, err)

	_, err = promotionSvc.AddPromotionRule(ctx, promotion.ID, promotionsvc.RuleInput{
		RuleType:  promotionmodels.RuleTarget,
		Attribute: attrVariantID,
		Operator:  promotionmodels.OpIn,
		Values:    variantIDs,
	})
	require.NoError(t, err)

	return promotion.ID
}
