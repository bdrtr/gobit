package cart

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// serveCartWithCodes scripts the fake cart so that the snapshot carries whatever
// codes the fake currently holds.
//
// It is written this way and not with a fixed list because the point of these
// tests is the ROUND TRIP: the flow writes a code and then reads the snapshot,
// and a script that always returned the same codes could not tell a write that
// happened from one that did not.
func serveCartWithCodes(carts *stubCarts, revision int64, items []SnapshotItem) {
	carts.snapshotFn = func(_ context.Context, cartID string) (json.RawMessage, error) {
		snap := snapshotOf(revision, items, nil)
		snap.ID = cartID
		snap.PromotionCodes = carts.codes[cartID]

		return json.Marshal(snap)
	}
}

// TestACouponCodeReachesTheDiscountRound is the wire the cart could not fill.
//
// The request has carried a "codes" array since the discount engine was built
// and the cart had nowhere to keep a code, so the array was always empty. A
// consumer that kept sending [] would have left the whole coupon path dead while
// every test passed.
func TestACouponCodeReachesTheDiscountRound(t *testing.T) {
	ctx := context.Background()
	h := newModuleHarness(t)
	h.discounts.perLine = map[string]int64{testLineA: 100}
	serveCartWithCodes(h.carts, 1, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}})

	require.NoError(t, h.wf.ApplyPromotionCode(ctx, testCartID, "SUMMER20"))

	require.NotEmpty(t, h.discounts.requests)
	last := h.discounts.requests[len(h.discounts.requests)-1]
	assert.Equal(t, []string{"SUMMER20"}, last.Codes,
		"the code the cart holds has to be in the round's request")
}

// TestTheCodeIsCheckedBEFOREItIsWritten pins the order.
//
// The reverse — write, reprice, then look at what could not be matched — leaves
// the cart holding an unusable code for as long as the round takes, and has to
// unwrite it afterwards; the failure of that unwrite leaves the shopper with a
// coupon nothing will ever honor.
func TestTheCodeIsCheckedBEFOREItIsWritten(t *testing.T) {
	ctx := context.Background()
	h := newModuleHarness(t)
	h.discounts.usableCodes = map[string]bool{"REAL": true}
	serveCartWithCodes(h.carts, 1, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}})

	err := h.wf.ApplyPromotionCode(ctx, testCartID, "MADE_UP")

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, CodeCouponNotUsable, errors.CodeOf(err))
	assert.Empty(t, h.carts.codes[testCartID], "the cart was not written to")
	assert.Equal(t, []string{"MADE_UP"}, h.discounts.couponChecks)
	assert.Zero(t, h.discounts.calls, "and no round was run for a code that never landed")
}

// TestACouponThatDiscountsNothingIsStillApplied is the distinction the promotion
// module already draws and this flow does not add a second one to.
//
// A valid coupon whose target matches no line is not an invalid code, only one
// that did nothing today — and it may start working when another item is added.
func TestACouponThatDiscountsNothingIsStillApplied(t *testing.T) {
	ctx := context.Background()
	h := newModuleHarness(t)
	h.discounts.usableCodes = map[string]bool{"WINTER": true}
	// No per-line discount is scripted, so the round gives nothing back.
	serveCartWithCodes(h.carts, 1, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}})

	require.NoError(t, h.wf.ApplyPromotionCode(ctx, testCartID, "WINTER"))

	assert.Equal(t, []string{"WINTER"}, h.carts.codes[testCartID])
}

// TestApplyingACouponRepricesTheCart is why the flow and not the service owns
// this.
//
// The cart's WRITTEN total is what the completion saga charges. Leaving the
// repricing to the client would let a cart be completed at the amount it had
// before the coupon.
func TestApplyingACouponRepricesTheCart(t *testing.T) {
	ctx := context.Background()
	h := newModuleHarness(t)
	h.discounts.perLine = map[string]int64{testLineA: 400}
	serveCartWithCodes(h.carts, 1, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}})

	require.NoError(t, h.wf.ApplyPromotionCode(ctx, testCartID, "SUMMER20"))

	require.NotEmpty(t, h.carts.written, "the totals were written")
	last := h.carts.written[len(h.carts.written)-1]
	assert.Equal(t, int64(400), last.DiscountTotal)
}

// TestTheRoundReportsWhichPromotionsApplied is the field the cart's consumer
// needs.
//
// The order that is about to be created has to SPEND the coupons, and the
// redemption is addressed per promotion and takes an amount. A consumer that
// dropped this field would leave the order unable to say which coupon to spend —
// the fault ADR 0102 found one boundary over, in the other direction.
func TestTheRoundReportsWhichPromotionsApplied(t *testing.T) {
	ctx := context.Background()
	h := newModuleHarness(t)
	h.discounts.perLine = map[string]int64{testLineA: 400}
	serveCartWithCodes(h.carts, 1, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}})

	require.NoError(t, h.wf.ApplyPromotionCode(ctx, testCartID, "SUMMER20"))

	totals, err := h.wf.CalculateTotals(ctx, testCartID)
	require.NoError(t, err)
	require.Len(t, totals.Applied, 1)
	assert.Equal(t, "promo_SUMMER20", totals.Applied[0].PromotionID)
	assert.Equal(t, "SUMMER20", totals.Applied[0].Code)
	assert.Equal(t, int64(400), totals.Applied[0].Amount)
}

// TestRemovingACouponAsksThePromotionModuleNothing keeps a shopper from being
// stuck with a coupon that expired while the page was open.
func TestRemovingACouponAsksThePromotionModuleNothing(t *testing.T) {
	ctx := context.Background()
	h := newModuleHarness(t)
	h.discounts.usableCodes = map[string]bool{"SUMMER20": true}
	serveCartWithCodes(h.carts, 1, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}})

	require.NoError(t, h.wf.ApplyPromotionCode(ctx, testCartID, "SUMMER20"))

	// The coupon stops being usable while the shopper is looking at the page.
	h.discounts.usableCodes = map[string]bool{}
	checks := len(h.discounts.couponChecks)

	require.NoError(t, h.wf.RemovePromotionCode(ctx, testCartID, "SUMMER20"))

	assert.Empty(t, h.carts.codes[testCartID])
	assert.Len(t, h.discounts.couponChecks, checks, "the removal asked nothing")
}

// TestACouponCannotBeAppliedWithoutThePromotionModule fails CLOSED.
//
// Writing the code in an installation that has no promotion module would put a
// coupon on the cart that nothing can ever honor, and answering "applied" would
// be a lie the shopper only finds out about at the till.
func TestACouponCannotBeAppliedWithoutThePromotionModule(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	serveCartWithCodes(h.carts, 1, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}})

	err := h.wf.ApplyPromotionCode(ctx, testCartID, "SUMMER20")

	require.Error(t, err)
	assert.Equal(t, CodeCouponNotUsable, errors.CodeOf(err))
	assert.Empty(t, h.carts.codes[testCartID])
}
