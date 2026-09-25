//go:build integration

package customer_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// wishlistRows counts a customer's wishlist rows in the table itself.
func wishlistRows(ctx context.Context, t *testing.T, customerID string) int {
	t.Helper()

	var n int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM customer_wishlist_item WHERE customer_id = $1`, customerID).Scan(&n))
	return n
}

// fillWishlist saves n distinct variants on the customer's wishlist.
func fillWishlist(ctx context.Context, t *testing.T, svc *service.Service, customerID string, n int) {
	t.Helper()

	for i := range n {
		_, err := svc.SaveToWishlist(ctx, customerID, fmt.Sprintf("variant_fill_%03d", i))
		require.NoError(t, err)
	}
}

// TestAVariantSavedTwiceIsOneRow shows the pair is the key: the second save
// writes nothing and returns the first save's moment.
func TestAVariantSavedTwiceIsOneRow(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	owner := newAccount(ctx, t, svc)

	first, err := svc.SaveToWishlist(ctx, owner.ID, "variant_A")
	require.NoError(t, err)
	again, err := svc.SaveToWishlist(ctx, owner.ID, "variant_A")
	require.NoError(t, err)

	assert.True(t, first.CreatedAt.Equal(again.CreatedAt), "saving again does not move the item")
	assert.Equal(t, 1, wishlistRows(ctx, t, owner.ID))
}

// TestAFullWishlistRefusesANewVariantAndKeepsASavedOne shows the cap in the
// database: a new variant is refused with the published code, and a variant
// already on the list is still answered.
func TestAFullWishlistRefusesANewVariantAndKeepsASavedOne(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	owner := newAccount(ctx, t, svc)
	fillWishlist(ctx, t, svc, owner.ID, models.MaxWishlistItems)

	_, err := svc.SaveToWishlist(ctx, owner.ID, "variant_one_too_many")
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, models.CodeWishlistFull, errors.CodeOf(err))

	_, err = svc.SaveToWishlist(ctx, owner.ID, "variant_fill_000")
	require.NoError(t, err, "a variant already saved is not a new one")
	assert.Equal(t, models.MaxWishlistItems, wishlistRows(ctx, t, owner.ID))
}

// TestASaveWaitsForTheLockBeforeItCounts is the reason the count is made under
// the customer row's lock. A rival transaction holds that lock and has written
// the last place, uncommitted; the save under test has to wait and then count
// the rival's row.
//
// The waiting is not the proof: the insert's foreign key takes KEY SHARE on the
// same row, so a save that counted without the lock would wait too, at its
// insert, having counted one place free. What carries the proof is the answer
// after waking: refused as full, with the table at the cap.
func TestASaveWaitsForTheLockBeforeItCounts(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	owner := newAccount(ctx, t, svc)
	fillWishlist(ctx, t, svc, owner.ID, models.MaxWishlistItems-1)

	conn, err := testPool.Pool().Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	var rivalPID int32
	require.NoError(t, tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&rivalPID))
	_, err = tx.Exec(ctx, `SELECT id FROM customer WHERE id = $1 FOR UPDATE`, owner.ID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx,
		`INSERT INTO customer_wishlist_item (customer_id, variant_id) VALUES ($1, 'variant_rival')`, owner.ID)
	require.NoError(t, err)

	result := make(chan error, 1)
	go func() {
		_, saveErr := svc.SaveToWishlist(ctx, owner.ID, "variant_late")
		result <- saveErr
	}()

	requireBlockedRequest(ctx, t, rivalPID)
	require.NoError(t, tx.Commit(ctx))

	var saveErr error
	select {
	case saveErr = <-result:
	case <-time.After(15 * time.Second):
		t.Fatal("the waiting save did not finish in time")
	}

	require.Error(t, saveErr, "the save counted before the rival's row was committed")
	assert.Equal(t, models.CodeWishlistFull, errors.CodeOf(saveErr))
	assert.Equal(t, models.MaxWishlistItems, wishlistRows(ctx, t, owner.ID))
}

// TestAnotherCustomersWishlistIsNotRead shows the listing reads one owner's
// rows, and a removal names its owner too.
func TestAnotherCustomersWishlistIsNotRead(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	owner, stranger := newAccount(ctx, t, svc), newAccount(ctx, t, svc)

	_, err := svc.SaveToWishlist(ctx, owner.ID, "variant_A")
	require.NoError(t, err)
	require.NoError(t, svc.RemoveFromWishlist(ctx, stranger.ID, "variant_A"))

	strangers, err := svc.ListWishlist(ctx, stranger.ID)
	require.NoError(t, err)
	assert.Empty(t, strangers)
	owners, err := svc.ListWishlist(ctx, owner.ID)
	require.NoError(t, err)
	require.Len(t, owners, 1, "the stranger's removal did not reach the owner's row")

	require.NoError(t, svc.RemoveFromWishlist(ctx, owner.ID, "variant_A"))
	assert.Zero(t, wishlistRows(ctx, t, owner.ID), "the owner's removal did")
}

// TestTheWishlistIsReadNewestFirstFromTheTable shows the order the query gives.
func TestTheWishlistIsReadNewestFirstFromTheTable(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	owner := newAccount(ctx, t, svc)

	for _, variant := range []string{"variant_B", "variant_A", "variant_C"} {
		_, err := svc.SaveToWishlist(ctx, owner.ID, variant)
		require.NoError(t, err)
	}

	items, err := svc.ListWishlist(ctx, owner.ID)
	require.NoError(t, err)
	variants := make([]string, 0, len(items))
	for _, item := range items {
		variants = append(variants, item.VariantID)
	}
	assert.Equal(t, []string{"variant_C", "variant_A", "variant_B"}, variants,
		"saved in the order B, A, C, so read C, A, B; alphabetical would be A, B, C")
}

// TestADeletedCustomerHasNoWishlistToWriteOrRead shows the wishlist goes with
// the customer's soft delete: its rows are unreachable, and kept until an
// erasure, as the addresses are.
func TestADeletedCustomerHasNoWishlistToWriteOrRead(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	owner := newAccount(ctx, t, svc)
	_, err := svc.SaveToWishlist(ctx, owner.ID, "variant_A")
	require.NoError(t, err)
	require.NoError(t, svc.DeleteCustomer(ctx, owner.ID))

	_, err = svc.SaveToWishlist(ctx, owner.ID, "variant_B")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "%v", err)
	err = svc.RemoveFromWishlist(ctx, owner.ID, "variant_A")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "%v", err)
	_, err = svc.ListWishlist(ctx, owner.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "%v", err)
}

// TestAnErasureDeletesTheWishlist shows the declared column emptied by deleting
// its rows, counted in the receipt, and a second erasure finding nothing.
func TestAnErasureDeletesTheWishlist(t *testing.T) {
	ctx := context.Background()
	svc := erasureService(t)
	created := personalRecord(ctx, t, svc, true)
	for _, variant := range []string{"variant_A", "variant_B"} {
		_, err := svc.SaveToWishlist(ctx, created.ID, variant)
		require.NoError(t, err)
	}

	first, err := svc.Erase(ctx, personaldata.Subject{CustomerID: created.ID})
	require.NoError(t, err)
	assert.Zero(t, wishlistRows(ctx, t, created.ID))
	assert.Equal(t, 1+1+2, first.Rows, "the customer, the address and the two wishlist rows")

	second, err := svc.Erase(ctx, personaldata.Subject{CustomerID: created.ID})
	require.NoError(t, err)
	assert.Zero(t, second.Rows, "a second erasure has nothing left to delete")
}

// TestADisclosureShowsTheWishlistInTheDatabase shows the saved variants in the
// person's file, read from the table.
func TestADisclosureShowsTheWishlistInTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc := erasureService(t)
	created := personalRecord(ctx, t, svc, true)
	for _, variant := range []string{"variant_A", "variant_B"} {
		_, err := svc.SaveToWishlist(ctx, created.ID, variant)
		require.NoError(t, err)
	}

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: created.ID})
	require.NoError(t, err)

	var wished []any
	for _, record := range disclosure.Records {
		if record.Table != service.TableWishlist {
			continue
		}
		for _, field := range record.Fields {
			wished = append(wished, field.Value)
		}
	}
	assert.ElementsMatch(t, []any{"variant_A", "variant_B"}, wished)
}

// TestABlankVariantIsRefusedByTheTable shows the CHECK behind the service's
// own refusal, for a writer that reaches the table around it.
func TestABlankVariantIsRefusedByTheTable(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	owner := newAccount(ctx, t, svc)

	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO customer_wishlist_item (customer_id, variant_id) VALUES ($1, '  ')`, owner.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "customer_wishlist_item_variant_check")
}
