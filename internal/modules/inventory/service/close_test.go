package service_test

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// This file holds the closing of a stock location (ADR 0055).
//
// The decision it tests is one invariant in two halves: a location closes only
// while it is EMPTY, and a closed location takes no stock. Both halves are
// needed for the same reason — availability sums inventory_levels with NO join
// to stock_locations, so a closed location that held units would go on selling
// them while the operator saw a retired warehouse.

// newService builds a service over a fresh fake store.
//
// The package's existing helper does exactly this under a Turkish name, and this
// file is English (ADR 0012, decision 3 — language is a property of the FILE):
// reaching for it would drag the old name into a translated file and the
// language check would fail, correctly. admin_test.go's newAdmin makes the same
// choice for the same reason.
func newService(t *testing.T) (*service.Service, *fakeStore) {
	t.Helper()

	store := newFakeStore()

	return service.New(store, nil), store
}

// TestALocationClosesOnlyWhenItIsEmpty proves the close refuses over stock and
// succeeds once the stock is gone.
//
// The close does not move the stock and does not zero it. Moving is a transfer
// this module has no primitive for, and zeroing would destroy a physical count
// silently — the exact thing SetInventoryLevel refuses to do. So the close
// waits for the operator instead of deciding for them.
func TestALocationClosesOnlyWhenItIsEmpty(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 0)

	_, err := svc.CloseStockLocation(ctx, locA)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeLocationNotEmpty, errors.CodeOf(err))

	_, err = svc.SetInventoryLevel(ctx, itemID, locA, 0)
	require.NoError(t, err)

	closed, err := svc.CloseStockLocation(ctx, locA)

	require.NoError(t, err)
	require.NotNil(t, closed.ClosedAt, "a closed location carries the moment it closed")
	assert.True(t, closed.Closed())
	assert.Equal(t, int64(0), store.level(itemID, locA).StockedQuantity,
		"the close does not delete the level; the emptied row stays where it is")
}

// TestAnActiveReservationRefusesTheClose covers the check the stock sum cannot
// make.
//
// The fixture is deliberately inconsistent — an active reservation with no
// level row behind it — because that is the state this second check exists for.
// In the running shop a promise always shows up as reserved quantity, and it
// does so because the item deletion refuses while a reservation is active. That
// rule lives in another flow, and a close that leaned on it would be depending
// on a sentence it does not contain.
func TestAnActiveReservationRefusesTheClose(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedReservation(resID, itemID, locA, 3, models.ReservationActive)

	_, err := svc.CloseStockLocation(context.Background(), locA)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeLocationNotEmpty, errors.CodeOf(err))
}

// TestAClosedLocationTakesNoStock is the second half of the invariant.
//
// Refusing the close over stock keeps a location empty at the moment it closes.
// Refusing the WRITE keeps it empty afterwards; without it, "closed" would be
// true for one instant.
func TestAClosedLocationTakesNoStock(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 0, 0)
	store.seedClosedLocation(locA)

	_, err := svc.SetInventoryLevel(ctx, itemID, locA, 5)
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeLocationClosed, errors.CodeOf(err))

	_, err = svc.AdjustInventory(ctx, itemID, locA, 5)
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeLocationClosed, errors.CodeOf(err))

	assert.Equal(t, int64(0), store.level(itemID, locA).StockedQuantity)
}

// TestClosingTwiceKeepsTheFirstMoment holds the idempotence a decommission
// needs: a retried close has to be able to finish, and the moment it reports
// has already passed.
func TestClosingTwiceKeepsTheFirstMoment(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	store.seedLocation(locA)

	first, err := svc.CloseStockLocation(ctx, locA)
	require.NoError(t, err)
	require.NotNil(t, first.ClosedAt)

	second, err := svc.CloseStockLocation(ctx, locA)

	require.NoError(t, err)
	require.NotNil(t, second.ClosedAt)
	assert.Equal(t, *first.ClosedAt, *second.ClosedAt,
		"the second close must not overwrite the first moment")
}

// TestAClosedLocationLeavesTheListingAndStaysReadable is what the operator sees.
//
// Two questions, two answers. The list is what a warehouse is PICKED from and a
// closed one cannot be picked, so it leaves the list by default and comes back
// on request; the single-row read always answers, because the levels and
// reservations of the shop's past name that row and reservations are never
// deleted.
func TestAClosedLocationLeavesTheListingAndStaysReadable(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	store.seedLocation(locA)
	store.seedClosedLocation(locB)

	open, count, err := svc.ListStockLocations(ctx, service.ListStockLocationsInput{})
	require.NoError(t, err)
	require.Len(t, open, 1)
	assert.Equal(t, locA, open[0].ID)
	assert.Equal(t, int64(1), count, "the count reports the matching rows, not the page")

	all, count, err := svc.ListStockLocations(ctx, service.ListStockLocationsInput{IncludeClosed: true})
	require.NoError(t, err)
	assert.Len(t, all, 2)
	assert.Equal(t, int64(2), count)

	closed, err := svc.GetStockLocation(ctx, locB)

	require.NoError(t, err)
	assert.Equal(t, locB, closed.ID)
	assert.True(t, closed.Closed(), "a closed location stays readable and reads as closed")
}

// TestTheLocationLockComesFirst proves the lock order the close rests on.
//
// The close counts what a location holds and then stamps it, and the two flows
// that put stock in take the same row SHARED before they touch an item or a
// level. A flow that took the location lock later — or not at all — could
// commit its stock between the count and the stamp, and the location would
// close over units no availability read ever joins. In a real database the
// violation appears only under a race; here the order is read directly.
//
// The three reservation flows are in the table too, asserting the OPPOSITE: they
// take no location lock. That is the sentence the Store's lock order section
// makes, and a table holding only the flows that take the lock would leave the
// half that is a deliberate omission resting on prose. A reservation cannot put
// stock into a location, so it has nothing to keep out of a close; the row it
// does touch, it touches through a foreign key, out of the order and safely
// (see the lock order section on service.Store).
func TestTheLocationLockComesFirst(t *testing.T) {
	stockWrites := []struct {
		name string
		call func(ctx context.Context, svc *service.Service) error
	}{
		{"SetInventoryLevel", func(ctx context.Context, svc *service.Service) error {
			_, err := svc.SetInventoryLevel(ctx, itemID, locA, 12)
			return err
		}},
		{"AdjustInventory", func(ctx context.Context, svc *service.Service) error {
			_, err := svc.AdjustInventory(ctx, itemID, locA, 1)
			return err
		}},
	}

	for _, flow := range stockWrites {
		t.Run(flow.name, func(t *testing.T) {
			svc, store := newService(t)
			seedForLockOrder(store)

			require.NoError(t, flow.call(context.Background(), svc))

			locks := store.kilitSirasi()
			require.Contains(t, locks, "location", "the flow never took the location lock: %v", locks)
			require.Contains(t, locks, "item", "the flow never took the item lock: %v", locks)
			assert.Less(t, slices.Index(locks, "location"), slices.Index(locks, "item"),
				"the location lock comes BEFORE the item lock, order taken: %v", locks)
		})
	}

	reservationFlows := []struct {
		name string
		call func(ctx context.Context, svc *service.Service) error
	}{
		{"Reserve", func(ctx context.Context, svc *service.Service) error {
			_, err := svc.Reserve(ctx, service.ReserveInput{
				InventoryItemID: itemID, LocationID: locA, Quantity: 1,
			})
			return err
		}},
		{"ReleaseReservation", func(ctx context.Context, svc *service.Service) error {
			return svc.ReleaseReservation(ctx, resID)
		}},
		{"ConfirmReservation", func(ctx context.Context, svc *service.Service) error {
			return svc.ConfirmReservation(ctx, resID)
		}},
	}

	for _, flow := range reservationFlows {
		t.Run(flow.name, func(t *testing.T) {
			svc, store := newService(t)
			seedForLockOrder(store)

			require.NoError(t, flow.call(context.Background(), svc))

			assert.NotContains(t, store.kilitSirasi(), "location",
				"a reservation flow takes no location lock; the omission is the decision")
		})
	}

	t.Run("CloseStockLocation", func(t *testing.T) {
		svc, store := newService(t)
		store.seedLocation(locA)

		_, err := svc.CloseStockLocation(context.Background(), locA)

		require.NoError(t, err)
		assert.Equal(t, []string{"location"}, store.kilitSirasi(),
			"the close locks the location and nothing else; locking levels would reverse the order")
	})
}

// seedForLockOrder puts an item, a level and an active reservation in place, so
// that every flow in the table above reaches its locks instead of failing early.
func seedForLockOrder(store *fakeStore) {
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 5)
	store.seedReservation(resID, itemID, locA, 5, models.ReservationActive)
}
