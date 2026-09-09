package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// fill adds a line to the cart.
func fill(ctx context.Context, t *testing.T, svc *service.Service, cartID, variant string, quantity int64) {
	t.Helper()

	_, err := svc.AddLineItem(ctx, cartID, service.AddLineItemInput{
		VariantID: variant,
		Title:     variant,
		Quantity:  quantity,
		UnitPrice: 1000,
	})
	require.NoError(t, err)
}

// settle computes the cart's totals and closes it.
//
// Completing goes through the totals, because [service.Service.MarkCompleted]
// refuses a cart whose amounts do not belong to its current shape.
func settle(ctx context.Context, t *testing.T, svc *service.Service, cartID string) error {
	t.Helper()

	detail, err := svc.GetCart(ctx, cartID)
	require.NoError(t, err)

	lines := make([]service.LineTotals, 0, len(detail.Items))
	var subtotal int64

	for i := range detail.Items {
		item := detail.Items[i]
		amount := item.UnitPrice * item.Quantity
		subtotal += amount
		lines = append(lines, service.LineTotals{
			LineItemID: item.ID, UnitPrice: item.UnitPrice,
			Subtotal: amount, Total: amount,
		})
	}

	require.NoError(t, svc.SetTotals(ctx, cartID, service.Totals{
		Revision: detail.Revision, Subtotal: subtotal, Total: subtotal, Lines: lines,
	}))

	_, err = svc.MarkCompleted(ctx, cartID)

	return err
}

// quantities returns the cart's lines as variant → quantity.
func quantities(ctx context.Context, t *testing.T, svc *service.Service, cartID string) map[string]int64 {
	t.Helper()

	detail, err := svc.GetCart(ctx, cartID)
	require.NoError(t, err)

	out := map[string]int64{}
	for i := range detail.Items {
		out[detail.Items[i].VariantID] = detail.Items[i].Quantity
	}

	return out
}

// TestMergingTwoCartsSumsTheSharedVariant is the decision the merge exists to
// apply.
//
// The quantity is SUMMED and that is [service.Service.AddLineItem]'s rule, not a
// new one: the same two additions must not answer differently for having been
// made in two sessions.
func TestMergingTwoCartsSumsTheSharedVariant(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	target := newCart(ctx, t, svc)
	source := newCart(ctx, t, svc)
	fill(ctx, t, svc, target.ID, variantA, 1)
	fill(ctx, t, svc, source.ID, variantA, 2)
	fill(ctx, t, svc, source.ID, variantB, 3)

	merged, err := svc.MergeCart(ctx, source.ID, target.ID)
	require.NoError(t, err)
	assert.Equal(t, target.ID, merged.ID, "the cart that survives is the target")

	assert.Equal(t, map[string]int64{variantA: 3, variantB: 3},
		quantities(ctx, t, svc, target.ID))
}

// TestTheMergedLineKeepsWhatTheTargetKnew pins which side wins on a collision.
//
// Only the QUANTITY travels. The line already in the target carries the title
// and the price that cart was quoted, and replacing them would let a stale guest
// session rewrite what the member is looking at.
func TestTheMergedLineKeepsWhatTheTargetKnew(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	target := newCart(ctx, t, svc)
	source := newCart(ctx, t, svc)

	_, err := svc.AddLineItem(ctx, target.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "the name the member sees", Quantity: 1, UnitPrice: 1000,
	})
	require.NoError(t, err)
	_, err = svc.AddLineItem(ctx, source.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "an older name", Quantity: 1, UnitPrice: 9999,
	})
	require.NoError(t, err)

	_, err = svc.MergeCart(ctx, source.ID, target.ID)
	require.NoError(t, err)

	detail, err := svc.GetCart(ctx, target.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)
	assert.Equal(t, "the name the member sees", detail.Items[0].Title)
	assert.Equal(t, int64(1000), detail.Items[0].UnitPrice)
	assert.Equal(t, int64(2), detail.Items[0].Quantity)
}

// TestTheSourceIsEmptiedAndDeleted is what keeps the goods from being bought
// twice.
//
// A cart still holding its lines can still be completed. Leaving it alive would
// turn one basket into two orders.
func TestTheSourceIsEmptiedAndDeleted(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	target := newCart(ctx, t, svc)
	source := newCart(ctx, t, svc)
	fill(ctx, t, svc, source.ID, variantA, 2)

	_, err := svc.MergeCart(ctx, source.ID, target.ID)
	require.NoError(t, err)

	_, err = svc.GetCart(ctx, source.ID)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "got %v", err)
}

// TestTheMergeTakesBothLocksInIdentifierOrder is the deadlock this could have
// been.
//
// Two merges running opposite ways — a phone folding into a laptop while the
// laptop folds into the phone — would each hold the row the other waits for. The
// order is by IDENTIFIER, so both take the same row first and one waits.
func TestTheMergeTakesBothLocksInIdentifierOrder(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)

	one := newCart(ctx, t, svc)
	two := newCart(ctx, t, svc)

	first, second := one.ID, two.ID
	if first > second {
		first, second = second, first
	}

	store.lockedCarts = nil
	_, err := svc.MergeCart(ctx, one.ID, two.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{first, second}, store.lockedCarts)

	// The other direction, on two fresh carts, takes the same order.
	three := newCart(ctx, t, svc)
	four := newCart(ctx, t, svc)

	low, high := three.ID, four.ID
	if low > high {
		low, high = high, low
	}

	store.lockedCarts = nil
	_, err = svc.MergeCart(ctx, high, low)
	require.NoError(t, err)
	assert.Equal(t, []string{low, high}, store.lockedCarts)
}

// TestAMergeAcrossRegionsIsRefused keeps a price out of a cart it was never
// valid in.
func TestAMergeAcrossRegionsIsRefused(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	target := newCart(ctx, t, svc)
	source, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: "region_ELSEWHERE", CurrencyCode: currency,
	})
	require.NoError(t, err)

	_, err = svc.MergeCart(ctx, source.ID, target.ID)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeRegionMismatch, errors.CodeOf(err))
}

// TestAMergeAcrossCurrenciesIsRefused is the same refusal for the other half of
// what a price is quoted in.
func TestAMergeAcrossCurrenciesIsRefused(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	target := newCart(ctx, t, svc)
	source, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CurrencyCode: "EUR",
	})
	require.NoError(t, err)

	_, err = svc.MergeCart(ctx, source.ID, target.ID)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeCurrencyMismatch, errors.CodeOf(err))
}

// TestACartCannotSwallowAnotherCustomersCart is UpdateCart's refusal seen from
// the other end.
func TestACartCannotSwallowAnotherCustomersCart(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	target := newCart(ctx, t, svc)
	source, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CurrencyCode: currency, CustomerID: "cust_SOMEBODY_ELSE",
	})
	require.NoError(t, err)

	_, err = svc.MergeCart(ctx, source.ID, target.ID)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeCustomerMismatch, errors.CodeOf(err))
}

// TestAGuestCartFoldsIntoAMembersCart is the case the feature was built for, and
// it is the one an over-eager owner check would have broken.
func TestAGuestCartFoldsIntoAMembersCart(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	target, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CurrencyCode: currency, CustomerID: "cust_THE_MEMBER",
	})
	require.NoError(t, err)
	source := newCart(ctx, t, svc)
	fill(ctx, t, svc, source.ID, variantA, 2)

	_, err = svc.MergeCart(ctx, source.ID, target.ID)
	require.NoError(t, err)

	assert.Equal(t, map[string]int64{variantA: 2}, quantities(ctx, t, svc, target.ID))
}

// TestACompletedCartIsNeitherSourceNorTarget guards the record an order rests
// on, from both sides.
func TestACompletedCartIsNeitherSourceNorTarget(t *testing.T) {
	ctx := context.Background()

	for name, completeSource := range map[string]bool{"source": true, "target": false} {
		t.Run(name, func(t *testing.T) {
			svc, _ := newService(t)

			target := newCart(ctx, t, svc)
			source := newCart(ctx, t, svc)
			fill(ctx, t, svc, target.ID, variantA, 1)
			fill(ctx, t, svc, source.ID, variantB, 1)

			closed := target.ID
			if completeSource {
				closed = source.ID
			}
			require.NoError(t, settle(ctx, t, svc, closed))

			_, err := svc.MergeCart(ctx, source.ID, target.ID)

			require.Error(t, err)
			assert.Equal(t, errors.KindConflict, errors.KindOf(err))
			assert.Equal(t, service.CodeCompleted, errors.CodeOf(err))
		})
	}
}

// TestACartCannotBeMergedIntoItself keeps a cart from doubling its own
// quantities.
func TestACartCannotBeMergedIntoItself(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	cart := newCart(ctx, t, svc)
	fill(ctx, t, svc, cart.ID, variantA, 2)

	_, err := svc.MergeCart(ctx, cart.ID, cart.ID)

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, map[string]int64{variantA: 2}, quantities(ctx, t, svc, cart.ID))
}

// TestAMergeThatMovedNothingLeavesTheShapeAlone keeps an empty cart from costing
// a repricing round.
//
// The revision is the totals workflow's signal that the cart changed. A source
// with no lines changed nothing, and bumping anyway would stale totals that are
// still correct.
func TestAMergeThatMovedNothingLeavesTheShapeAlone(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)

	target := newCart(ctx, t, svc)
	source := newCart(ctx, t, svc)
	fill(ctx, t, svc, target.ID, variantA, 1)

	before := store.bumpCalls
	merged, err := svc.MergeCart(ctx, source.ID, target.ID)
	require.NoError(t, err)

	assert.Equal(t, before, store.bumpCalls, "nothing moved, so no shape changed")
	assert.Equal(t, map[string]int64{variantA: 1}, quantities(ctx, t, svc, target.ID))
	assert.Equal(t, target.ID, merged.ID)
}

// TestAMergeThatMovedSomethingBumpsTheShape is the other half: the totals have
// to be told.
func TestAMergeThatMovedSomethingBumpsTheShape(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)

	target := newCart(ctx, t, svc)
	source := newCart(ctx, t, svc)
	fill(ctx, t, svc, source.ID, variantA, 1)

	before := store.bumpCalls
	_, err := svc.MergeCart(ctx, source.ID, target.ID)
	require.NoError(t, err)

	assert.Equal(t, before+1, store.bumpCalls, "the target's shape changed exactly once")
}

// TestAFailedMergeLeavesBothCartsAlone is the rollback.
//
// The merge writes to two carts and deletes one of them. A failure halfway
// through would leave the goods in neither: moved out of the source and not into
// the target.
func TestAFailedMergeLeavesBothCartsAlone(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)

	target := newCart(ctx, t, svc)
	source := newCart(ctx, t, svc)
	fill(ctx, t, svc, source.ID, variantA, 2)

	store.failCreateLineItem = errors.Internal("boom", "the write failed")

	_, err := svc.MergeCart(ctx, source.ID, target.ID)
	require.Error(t, err)

	assert.Empty(t, quantities(ctx, t, svc, target.ID), "nothing reached the target")
	assert.Equal(t, map[string]int64{variantA: 2}, quantities(ctx, t, svc, source.ID),
		"and nothing left the source")
}

// TestTheMergedQuantityCannotPassTheCeiling is AddLineItem's overflow guard,
// applied to the same sum.
func TestTheMergedQuantityCannotPassTheCeiling(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	target := newCart(ctx, t, svc)
	source := newCart(ctx, t, svc)
	fill(ctx, t, svc, target.ID, variantA, models.MaxQuantity)
	fill(ctx, t, svc, source.ID, variantA, 1)

	_, err := svc.MergeCart(ctx, source.ID, target.ID)

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}
