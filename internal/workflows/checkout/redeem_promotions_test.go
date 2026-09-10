package checkout

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// discountedAmount is what a cart carrying [discountedTotals] comes to.
const discountedAmount = testAmount - 300

// discountedTotals returns totals whose discount rests on two promotions.
//
// The discount falls on the LINES, because the plan checks both identities: the
// cart's subtotal is the sum of the line subtotals, and every line's subtotal is
// its unit price times its quantity. Only the discount fields are free.
func discountedTotals(ctx context.Context, cartID string) (cartwf.Totals, error) {
	totals, err := defaultTotals(ctx, cartID)
	if err != nil {
		return cartwf.Totals{}, err
	}
	totals.Lines[0].DiscountTotal = 200
	totals.Lines[0].Total -= 200
	totals.Lines[1].DiscountTotal = 100
	totals.Lines[1].Total -= 100
	totals.DiscountTotal = 300
	totals.Total = discountedAmount
	totals.Applied = []cartwf.AppliedPromotion{
		{PromotionID: "promo_coupon", Code: "SUMMER20", Amount: 200},
		{PromotionID: "promo_auto", Amount: 100},
	}

	return totals, nil
}

// discounted scripts the harness for a cart whose discount rests on two
// promotions.
//
// The payment fake's collection is scripted too: it answers with [testAmount] by
// default and the capture step verifies the collection against the PLAN, so a
// discounted cart would otherwise fail on a mismatch that has nothing to do with
// what these tests are about.
func discounted(h *harness) {
	h.totals.calculateFn = discountedTotals
	h.payments.collectionFn = func(context.Context, string) (string, int64, int64, int64, int64, error) {
		return "captured", discountedAmount, 0, discountedAmount, 0, nil
	}
}

// TestTheCouponsAreSpentWhenTheCartBecomesAnOrder is the write the discount
// round never makes.
//
// The round is side-effect free by design: the cart's total is recomputed on
// every change and a computation that spent a coupon would make looking at the
// cart the same thing as using it. Until this step existed NOTHING ever moved a
// usage counter, so a coupon limited to one use could be spent forever.
func TestTheCouponsAreSpentWhenTheCartBecomesAnOrder(t *testing.T) {
	h := newHarness(t)
	discounted(h)

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	assert.Equal(t, 1, h.promotions.spent["promo_coupon"])
	assert.Equal(t, 1, h.promotions.spent["promo_auto"],
		"an automatic promotion is spent too; it has no code, not no budget")
	assert.Equal(t, int64(200), h.promotions.redeemedAmounts["promo_coupon"],
		"the amount is the one the customer was shown, per promotion")
}

// TestTheRedemptionReferenceIsTheCart is what makes a retry safe.
//
// The reference is what the promotion module keys idempotency on, and it has to
// be stable across a replay of this step. The cart is: it exists before the saga
// starts and a saga resumed from the record carries the same one. The order's
// identifier is produced by a LATER step, so using it would mean the coupons
// could only be spent after an order existed — and an exhausted coupon would
// then be found only after one had been placed.
func TestTheRedemptionReferenceIsTheCart(t *testing.T) {
	h := newHarness(t)
	discounted(h)

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	require.NotEmpty(t, h.promotions.references)
	for _, ref := range h.promotions.references {
		assert.Equal(t, testCartID, ref)
	}
}

// TestTheCouponsAreSpentBEFORETheOrderIsPlaced pins the order of the saga.
//
// A promotion whose last use was taken while the shopper was on the payment page
// has to refuse the checkout, and refusing it after an order exists means
// canceling one that should never have been placed.
func TestTheCouponsAreSpentBEFORETheOrderIsPlaced(t *testing.T) {
	h := newHarness(t)
	discounted(h)

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	calls := h.rec.snapshot()
	redeem, place := -1, -1
	for i, call := range calls {
		if call == "promotion:redeem" && redeem < 0 {
			redeem = i
		}
		if call == "order:place" {
			place = i
		}
	}
	require.NotEqual(t, -1, redeem, "the coupons were spent")
	require.NotEqual(t, -1, place, "the order was placed")
	assert.Less(t, redeem, place, "the coupon is spent first")
}

// TestACartWithNoPromotionAsksThePromotionModuleNothing keeps a shop that sells
// without coupons from paying for the step.
func TestACartWithNoPromotionAsksThePromotionModuleNothing(t *testing.T) {
	h := newHarness(t)

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	assert.Zero(t, h.rec.count("promotion:redeem"))
}

// TestAnExhaustedCouponStopsTheCheckout is the refusal the merchant's budget
// depends on.
//
// The discount round eliminated nothing — the coupon was usable when the cart
// was priced — and its last use was taken while the shopper was paying. Letting
// it through would give away a discount the campaign cannot pay for.
func TestAnExhaustedCouponStopsTheCheckout(t *testing.T) {
	h := newHarness(t)
	discounted(h)
	h.promotions.redeemErr = map[string]error{
		"promo_auto": errors.Conflict("promotion_usage_exhausted", "no uses left"),
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())

	require.Error(t, err)
	assert.Zero(t, h.rec.count("order:place"), "no order was placed")
	assert.Zero(t, h.rec.count("payment:capture"), "and no money moved")
}

// TestAHalfSpentStepGivesBackWhatItTook is the compensation.
//
// The first promotion was spent and the second refused. What the step took has
// to come back, or a coupon the customer never got the benefit of stays counted
// against them.
func TestAHalfSpentStepGivesBackWhatItTook(t *testing.T) {
	h := newHarness(t)
	discounted(h)
	h.promotions.redeemErr = map[string]error{
		"promo_auto": errors.Conflict("promotion_usage_exhausted", "no uses left"),
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.Zero(t, h.promotions.spent["promo_coupon"],
		"the use taken before the refusal was given back")
	assert.Equal(t, 1, h.rec.count("promotion:release"))
}

// TestAFailureLaterInTheSagaReleasesTheCoupons proves the compensation runs for
// a failure that is not the redemption's own.
func TestAFailureLaterInTheSagaReleasesTheCoupons(t *testing.T) {
	h := newHarness(t)
	discounted(h)
	h.orders.placeFn = func(context.Context, json.RawMessage) (string, error) {
		return "", errors.Internal("order_write_failed", "the order could not be written")
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.Zero(t, h.promotions.spent["promo_coupon"])
	assert.Zero(t, h.promotions.spent["promo_auto"])
	assert.Equal(t, 2, h.rec.count("promotion:release"), "both uses came back")
}

// TestACartWhoseDiscountRestsOnAPromotionNeedsTheModule fails CLOSED.
//
// The plan carries promotions and the module that owns them is not bound. Going
// on would place an order at a discounted price with no coupon ever spent, so
// the usage limit would mean nothing.
func TestACartWhoseDiscountRestsOnAPromotionNeedsTheModule(t *testing.T) {
	h := newHarness(t)
	discounted(h)
	h.wf.promotions = nil

	_, err := h.wf.CompleteCart(context.Background(), h.input())

	require.Error(t, err)
	assert.Equal(t, CodePromotionUnavailable, errors.CodeOf(err))
	assert.Zero(t, h.rec.count("order:place"))
}

// TestAShopWithoutThePromotionModuleStillSells is the other half: the surface is
// optional and a cart with no discount never asks for it.
func TestAShopWithoutThePromotionModuleStillSells(t *testing.T) {
	h := newHarness(t)
	h.wf.promotions = nil

	_, err := h.wf.CompleteCart(context.Background(), h.input())

	require.NoError(t, err)
}
