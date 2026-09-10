package service_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// codesOf returns the coupon codes the cart holds.
func codesOf(ctx context.Context, t *testing.T, svc *service.Service, cartID string) []string {
	t.Helper()

	detail, err := svc.GetCart(ctx, cartID)
	require.NoError(t, err)

	return detail.PromotionCodes
}

// TestACartCanHoldACouponCode is the storage the coupon path did not have.
//
// Until this existed only AUTOMATIC promotions could reach a cart: the discount
// request carried a "codes" array and nothing could fill it.
func TestACartCanHoldACouponCode(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	cart := newCart(ctx, t, svc)

	codes, err := svc.AddPromotionCode(ctx, cart.ID, "  summer20  ")
	require.NoError(t, err)

	assert.Equal(t, []string{"SUMMER20"}, codes, "the code is trimmed and upper-cased")
	assert.Equal(t, []string{"SUMMER20"}, codesOf(ctx, t, svc, cart.ID))
}

// TestACouponCodeIsNotCaseSensitive is a STORAGE decision and it belongs to the
// promotion module.
//
// "summer20" and "SUMMER20" are one coupon. Keeping the difference would let a
// cart hold both and ask the other module about a code it cannot match.
func TestACouponCodeIsNotCaseSensitive(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	cart := newCart(ctx, t, svc)

	_, err := svc.AddPromotionCode(ctx, cart.ID, "summer20")
	require.NoError(t, err)
	codes, err := svc.AddPromotionCode(ctx, cart.ID, "SuMmEr20")
	require.NoError(t, err)

	assert.Equal(t, []string{"SUMMER20"}, codes, "one coupon, not two")
}

// TestApplyingTheSameCodeTwiceChangesNothing keeps a double press from becoming
// a second coupon.
func TestApplyingTheSameCodeTwiceChangesNothing(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	cart := newCart(ctx, t, svc)

	_, err := svc.AddPromotionCode(ctx, cart.ID, "SUMMER20")
	require.NoError(t, err)
	before := store.bumpCalls

	codes, err := svc.AddPromotionCode(ctx, cart.ID, "SUMMER20")
	require.NoError(t, err)

	assert.Equal(t, []string{"SUMMER20"}, codes)
	assert.Equal(t, before+1, store.bumpCalls,
		"the second call still bumps the shape once; what it must not do is add a row")
}

// TestACouponCodeChangesTheCartsShape is why the counter goes up.
//
// A coupon changes what the cart COSTS, so the totals no longer belong to its
// current shape. Without the bump a cart could be completed at the amount it had
// before the coupon.
func TestACouponCodeChangesTheCartsShape(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	cart := newCart(ctx, t, svc)

	_, err := svc.AddPromotionCode(ctx, cart.ID, "SUMMER20")
	require.NoError(t, err)

	after, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Greater(t, after.Revision, cart.Revision)

	require.NoError(t, svc.RemovePromotionCode(ctx, cart.ID, "SUMMER20"))

	removed, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Greater(t, removed.Revision, after.Revision, "taking one off is a change too")
}

// TestRemovingACodeTheCartDoesNotHoldIsNotFound keeps a delete of nothing from
// reading as a success.
func TestRemovingACodeTheCartDoesNotHoldIsNotFound(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	cart := newCart(ctx, t, svc)

	err := svc.RemovePromotionCode(ctx, cart.ID, "NEVER_APPLIED")

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "got %v", err)
}

// TestACartCannotHoldMoreCodesThanTheRoundCanCarry is the ceiling.
//
// Every code becomes a database read and a rule evaluation inside the discount
// round. Past the round's own limit the round REFUSES the request — and a cart
// that cannot be priced cannot be bought, so the refusal has to happen at the
// moment the code is typed.
func TestACartCannotHoldMoreCodesThanTheRoundCanCarry(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	cart := newCart(ctx, t, svc)

	for i := range service.MaxPromotionCodes {
		_, err := svc.AddPromotionCode(ctx, cart.ID, fmt.Sprintf("CODE%02d", i))
		require.NoError(t, err, "code %d", i)
	}

	_, err := svc.AddPromotionCode(ctx, cart.ID, "ONE_TOO_MANY")

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeTooManyPromotionCodes, errors.CodeOf(err))
	assert.Len(t, codesOf(ctx, t, svc, cart.ID), service.MaxPromotionCodes)
}

// TestARepeatedCodeDoesNotSpendTheCeiling is the ceiling's other half.
//
// A cart at the limit can still be sent a code it already holds — that is a
// double press, and refusing it would make the shopper think the coupon fell
// off.
func TestARepeatedCodeDoesNotSpendTheCeiling(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	cart := newCart(ctx, t, svc)

	for i := range service.MaxPromotionCodes {
		_, err := svc.AddPromotionCode(ctx, cart.ID, fmt.Sprintf("CODE%02d", i))
		require.NoError(t, err)
	}

	_, err := svc.AddPromotionCode(ctx, cart.ID, "CODE00")
	require.NoError(t, err)
}

// TestACodeWithANonASCIICharacterIsRefused is what makes the column's CHECK
// sound.
//
// The row asserts `code = upper(code)`, and upper() folds the letters the
// CLUSTER's CTYPE knows: on a --locale=C database that is ASCII and nothing
// else. A code carrying "é" would be upper-cased differently by Go and by the
// database, so the same insert would succeed on one installation and be refused
// on another.
func TestACodeWithANonASCIICharacterIsRefused(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	cart := newCart(ctx, t, svc)

	_, err := svc.AddPromotionCode(ctx, cart.ID, "CAFÉ20")

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Empty(t, codesOf(ctx, t, svc, cart.ID))
}

// TestAnEmptyCodeIsRefused keeps out the one value that claims a coupon while
// naming none.
func TestAnEmptyCodeIsRefused(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	cart := newCart(ctx, t, svc)

	_, err := svc.AddPromotionCode(ctx, cart.ID, "   ")

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestACompletedCartTakesNoCoupon guards the record an order rests on.
func TestACompletedCartTakesNoCoupon(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	cart := newCart(ctx, t, svc)
	fill(ctx, t, svc, cart.ID, variantA, 1)
	require.NoError(t, settle(ctx, t, svc, cart.ID))

	_, err := svc.AddPromotionCode(ctx, cart.ID, "SUMMER20")

	require.Error(t, err)
	assert.Equal(t, service.CodeCompleted, errors.CodeOf(err))
}

// TestTheMergeCarriesTheCouponCodes is what ADR 0107 could not say.
//
// When the merge was decided a cart could not hold a code at all, so "only the
// lines move" described everything there was. Losing the coupon typed on the
// phone is the same complaint the merge exists to answer, one field over.
func TestTheMergeCarriesTheCouponCodes(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	target := newCart(ctx, t, svc)
	source := newCart(ctx, t, svc)

	_, err := svc.AddPromotionCode(ctx, target.ID, "MINE")
	require.NoError(t, err)
	_, err = svc.AddPromotionCode(ctx, source.ID, "THEIRS")
	require.NoError(t, err)
	_, err = svc.AddPromotionCode(ctx, source.ID, "MINE")
	require.NoError(t, err)

	_, err = svc.MergeCart(ctx, source.ID, target.ID)
	require.NoError(t, err)

	assert.Equal(t, []string{"MINE", "THEIRS"}, codesOf(ctx, t, svc, target.ID),
		"the union, and the shared code is not held twice")
}

// TestAMergeThatOnlyMovesCodesStillBumpsTheShape is the boundary of "nothing
// moved".
//
// A source with no lines but a coupon DID change the target: the cart now costs
// something different, so the totals have to be recomputed.
func TestAMergeThatOnlyMovesCodesStillBumpsTheShape(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)

	target := newCart(ctx, t, svc)
	source := newCart(ctx, t, svc)
	_, err := svc.AddPromotionCode(ctx, source.ID, "SUMMER20")
	require.NoError(t, err)

	before := store.bumpCalls
	_, err = svc.MergeCart(ctx, source.ID, target.ID)
	require.NoError(t, err)

	assert.Equal(t, before+1, store.bumpCalls)
	assert.Equal(t, []string{"SUMMER20"}, codesOf(ctx, t, svc, target.ID))
}

// TestDeletingACartTakesItsCouponsWithIt keeps a binding from outliving what it
// binds.
func TestDeletingACartTakesItsCouponsWithIt(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	cart := newCart(ctx, t, svc)

	_, err := svc.AddPromotionCode(ctx, cart.ID, "SUMMER20")
	require.NoError(t, err)

	require.NoError(t, svc.DeleteCart(ctx, cart.ID))

	held, err := store.ListPromotionCodes(ctx, cart.ID)
	require.NoError(t, err)
	assert.Empty(t, held)
}
