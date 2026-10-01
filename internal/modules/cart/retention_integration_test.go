//go:build integration

package cart_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/cart/repository"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// retentionNow is the moment the retention tests run against: far before any
// cart the shared database holds, so only the carts a test moved back in time
// are old enough.
var retentionNow = time.Date(2000, 6, 1, 0, 0, 0, 0, time.UTC)

// retainingService is the service with a thirty-day period (ADR 0301).
func retainingService(t *testing.T) *service.Service {
	t.Helper()

	svc, err := service.New(service.Options{
		Repo: repository.New(testPool.Pool()), Events: eventbus.NewInMemory(nil), RetentionDays: 30,
	})
	require.NoError(t, err)

	return svc
}

// lastChangedAt moves a cart's last change to the moment given.
func lastChangedAt(ctx context.Context, t *testing.T, cartID string, at time.Time) {
	t.Helper()

	_, err := testPool.Pool().Exec(ctx, `UPDATE carts SET updated_at = $2 WHERE id = $1`, cartID, at)
	require.NoError(t, err)
}

// cartRows counts a cart's own row and its children's, deleted or not.
func cartRows(ctx context.Context, t *testing.T, cartID string) (carts, children int) {
	t.Helper()

	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM carts WHERE id = $1`, cartID).Scan(&carts))
	require.NoError(t, testPool.Pool().QueryRow(ctx, `SELECT
		(SELECT count(*) FROM cart_line_items WHERE cart_id = $1) +
		(SELECT count(*) FROM cart_addresses WHERE cart_id = $1)`, cartID).Scan(&children))

	return carts, children
}

// TestAnAbandonedCartIsDeletedForGood is ADR 0301 on the real query: an open
// cart untouched for the period goes with its lines and address, a
// soft-deleted one too; a completed cart and a recent one stay.
func TestAnAbandonedCartIsDeletedForGood(t *testing.T) {
	ctx := context.Background()
	svc := retainingService(t)
	old := retentionNow.Add(-31 * 24 * time.Hour)

	abandoned := newCart(ctx, t, svc)
	_, err := svc.AddLineItem(ctx, abandoned.ID, service.AddLineItemInput{
		VariantID: "variant_RETENTION", Title: "Shirt", Quantity: 1,
	})
	require.NoError(t, err)
	_, err = svc.SetShippingAddress(ctx, abandoned.ID, service.AddressInput{FirstName: "Ada", City: "Ankara"})
	require.NoError(t, err)
	lastChangedAt(ctx, t, abandoned.ID, old)

	stamped := newCart(ctx, t, svc)
	require.NoError(t, svc.DeleteCart(ctx, stamped.ID))
	lastChangedAt(ctx, t, stamped.ID, old)

	completed := newCart(ctx, t, svc)
	_, err = testPool.Pool().Exec(ctx, `UPDATE carts SET completed_at = $2 WHERE id = $1`, completed.ID, old)
	require.NoError(t, err)
	lastChangedAt(ctx, t, completed.ID, old)

	recent := newCart(ctx, t, svc)
	lastChangedAt(ctx, t, recent.ID, retentionNow.Add(-29*24*time.Hour))

	deleted, err := svc.DeleteAbandonedCarts(ctx, retentionNow, 100)
	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted)

	for name, id := range map[string]string{"abandoned": abandoned.ID, "soft-deleted": stamped.ID} {
		carts, children := cartRows(ctx, t, id)
		assert.Zero(t, carts, "the %s cart is gone", name)
		assert.Zero(t, children, "the %s cart's lines and address went with it", name)
	}
	for name, id := range map[string]string{"completed": completed.ID, "recent": recent.ID} {
		carts, _ := cartRows(ctx, t, id)
		assert.Equal(t, 1, carts, "the %s cart stays", name)
	}
}

// TestTheRetentionDeletesTheOldestFirstAndSkipsALockedCart: the limit takes
// the oldest, and a cart a write holds locked is left for the next run rather
// than waited for.
func TestTheRetentionDeletesTheOldestFirstAndSkipsALockedCart(t *testing.T) {
	ctx := context.Background()
	svc := retainingService(t)

	first := newCart(ctx, t, svc)
	lastChangedAt(ctx, t, first.ID, retentionNow.Add(-120*24*time.Hour))
	second := newCart(ctx, t, svc)
	lastChangedAt(ctx, t, second.ID, retentionNow.Add(-100*24*time.Hour))
	deleted, err := svc.DeleteAbandonedCarts(ctx, retentionNow, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
	carts, _ := cartRows(ctx, t, first.ID)
	assert.Zero(t, carts, "the oldest goes first")
	carts, _ = cartRows(ctx, t, second.ID)
	assert.Equal(t, 1, carts, "the limit leaves the younger one")
	_, err = svc.DeleteAbandonedCarts(ctx, retentionNow, 1)
	require.NoError(t, err)

	oldest := newCart(ctx, t, svc)
	lastChangedAt(ctx, t, oldest.ID, retentionNow.Add(-90*24*time.Hour))
	older := newCart(ctx, t, svc)
	lastChangedAt(ctx, t, older.ID, retentionNow.Add(-60*24*time.Hour))

	tx, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SELECT id FROM carts WHERE id = $1 FOR UPDATE`, oldest.ID)
	require.NoError(t, err)

	deleted, err = svc.DeleteAbandonedCarts(ctx, retentionNow, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
	carts, _ = cartRows(ctx, t, oldest.ID)
	assert.Equal(t, 1, carts, "the locked cart is skipped, not waited for")
	carts, _ = cartRows(ctx, t, older.ID)
	assert.Zero(t, carts, "the next oldest is taken in its place")

	require.NoError(t, tx.Rollback(ctx))
	deleted, err = svc.DeleteAbandonedCarts(ctx, retentionNow, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
	carts, _ = cartRows(ctx, t, oldest.ID)
	assert.Zero(t, carts, "the next run takes it once the lock is gone")
}
