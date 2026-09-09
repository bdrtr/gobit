//go:build integration

// The tests here run against a real PostgreSQL; to run them:
// make test-integration
//
// What they cover is the half a fake cannot: two merges running the OTHER WAY
// ROUND from each other. The merge takes two row locks, and taking them in the
// order the caller named would let each transaction hold the row the other waits
// for — a deadlock a single-threaded fake can never produce.
package cart_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// stock adds a line to the cart.
func stock(ctx context.Context, t *testing.T, svc *service.Service, cartID, variant string, quantity int64) {
	t.Helper()

	_, err := svc.AddLineItem(ctx, cartID, service.AddLineItemInput{
		VariantID: variant, Title: variant, Quantity: quantity, UnitPrice: 1000,
	})
	require.NoError(t, err)
}

// TestTwoMergesRunningOppositeWaysDoNotDeadlock is the reason the lock is taken
// by identifier.
//
// A phone folding into a laptop while the laptop folds into the phone is not a
// hypothetical: two tabs, two sign-ins, one shopper. Locking in the order the
// caller named would have each transaction holding the row the other waits for,
// and PostgreSQL would settle it by killing one with SQLSTATE 40P01.
//
// It runs several ROUNDS because a deadlock that lands one time in three passes
// a single-round test whenever it feels like it (ADR 0091 measured that shape).
func TestTwoMergesRunningOppositeWaysDoNotDeadlock(t *testing.T) {
	const rounds = 5

	ctx := context.Background()
	svc := newService(t)

	for round := range rounds {
		one := newCart(ctx, t, svc)
		two := newCart(ctx, t, svc)
		stock(ctx, t, svc, one.ID, "variant_A", 1)
		stock(ctx, t, svc, two.ID, "variant_B", 1)

		var start, finish sync.WaitGroup
		errs := make([]error, 2)

		start.Add(1)
		finish.Add(2)

		go func() {
			defer finish.Done()
			start.Wait()
			_, errs[0] = svc.MergeCart(ctx, one.ID, two.ID)
		}()
		go func() {
			defer finish.Done()
			start.Wait()
			_, errs[1] = svc.MergeCart(ctx, two.ID, one.ID)
		}()

		start.Done()
		finish.Wait()

		// One of the two wins and the other finds its cart deleted: whichever
		// merge runs second is asked to fold a cart that no longer exists. What
		// must NOT happen is a deadlock, which arrives as a conflict about a
		// concurrent transaction rather than a missing cart.
		for i, err := range errs {
			if err == nil {
				continue
			}
			assert.Truef(t, errors.IsNotFound(err),
				"round %d, merge %d: the loser finds a cart that is gone, got %v", round, i, err)
		}
		assert.False(t, errs[0] == nil && errs[1] == nil,
			"round %d: both merges cannot succeed — one of the carts is gone", round)
	}
}

// TestTheMergeSurvivesTheRealUniqueIndex is the constraint the fold could have
// tripped.
//
// `cart_line_items_cart_variant_uniq` refuses a second living line for the same
// variant of one cart. A merge that INSERTED the source's line instead of adding
// its quantity to the existing one would hit it, and the fake cannot say which
// of the two happened because it enforces the same rule from Go.
func TestTheMergeSurvivesTheRealUniqueIndex(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	target := newCart(ctx, t, svc)
	source := newCart(ctx, t, svc)
	stock(ctx, t, svc, target.ID, "variant_A", 2)
	stock(ctx, t, svc, source.ID, "variant_A", 3)

	_, err := svc.MergeCart(ctx, source.ID, target.ID)
	require.NoError(t, err)

	detail, err := svc.GetCart(ctx, target.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1, "one variant is one line")
	assert.Equal(t, int64(5), detail.Items[0].Quantity)
}

// TestTheMergedSourceIsGoneFromTheDatabase reads the row itself.
//
// The source's cart and its lines are soft deleted, and "soft" is exactly the
// word that makes this worth reading directly: a row that is still there and
// invisible looks the same through the service as a row that was never written.
func TestTheMergedSourceIsGoneFromTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	target := newCart(ctx, t, svc)
	source := newCart(ctx, t, svc)
	stock(ctx, t, svc, source.ID, "variant_A", 2)

	_, err := svc.MergeCart(ctx, source.ID, target.ID)
	require.NoError(t, err)

	var deletedCarts, livingLines int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM carts WHERE id = $1 AND deleted_at IS NOT NULL`,
		source.ID).Scan(&deletedCarts))
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM cart_line_items WHERE cart_id = $1 AND deleted_at IS NULL`,
		source.ID).Scan(&livingLines))

	assert.Equal(t, int64(1), deletedCarts, "the source is stamped, not erased")
	assert.Zero(t, livingLines, "and it holds no line that could be bought again")

	detail, err := svc.GetCart(ctx, target.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)
	assert.Equal(t, int64(2), detail.Items[0].Quantity, "the goods are in the surviving cart")
}
