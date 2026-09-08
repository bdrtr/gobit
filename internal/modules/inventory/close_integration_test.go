//go:build integration

// The closing of a stock location against a real PostgreSQL (ADR 0055).
//
// The unit tests prove the DECISIONS on a fake store. What can only be proved
// here is the ground they stand on: that closed_at exists and is written, that
// a closed location is still read while it leaves the listing, and above all
// that the location row really is the rendezvous point — a close and a stock
// write racing for the same location cannot both succeed.
package inventory_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/repository"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// newService builds a service over the shared test pool.
//
// This file carries its own fixtures because the package's existing ones do the
// same work under Turkish names, and this file is English (ADR 0012, decision 3
// — language is a property of the FILE): reaching for them would drag the old
// names into a translated file and the language check would fail, correctly.
func newService(t *testing.T) *service.Service {
	t.Helper()

	return service.New(repository.New(testPool.Pool()), nil)
}

// addItem creates an inventory item with a unique SKU.
func addItem(ctx context.Context, t *testing.T, svc *service.Service) models.InventoryItem {
	t.Helper()

	item, err := svc.CreateInventoryItem(ctx, service.CreateInventoryItemInput{
		SKU:   "SKU-" + models.NewInventoryItemID(),
		Title: t.Name(),
	})
	require.NoError(t, err)

	return item
}

// addLocation creates a stock location.
func addLocation(ctx context.Context, t *testing.T, svc *service.Service) models.StockLocation {
	t.Helper()

	loc, err := svc.CreateStockLocation(ctx, service.CreateStockLocationInput{
		Name:        "Warehouse " + t.Name(),
		CountryCode: "TR",
	})
	require.NoError(t, err)

	return loc
}

// TestALocationClosesEmptyAndStaysReadable walks the whole decision on real SQL.
func TestALocationClosesEmptyAndStaysReadable(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	item, loc := addItem(ctx, t, svc), addLocation(ctx, t, svc)
	_, err := svc.SetInventoryLevel(ctx, item.ID, loc.ID, 6)
	require.NoError(t, err)

	_, err = svc.CloseStockLocation(ctx, loc.ID)
	require.Error(t, err, "a location holding stock cannot be closed")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeLocationNotEmpty, errors.CodeOf(err))

	_, err = svc.SetInventoryLevel(ctx, item.ID, loc.ID, 0)
	require.NoError(t, err)

	closed, err := svc.CloseStockLocation(ctx, loc.ID)
	require.NoError(t, err)
	require.NotNil(t, closed.ClosedAt, "closed_at is the column the schema now carries")

	// The write refusal is what keeps it empty after the fact.
	_, err = svc.SetInventoryLevel(ctx, item.ID, loc.ID, 4)
	require.Error(t, err)
	assert.Equal(t, service.CodeLocationClosed, errors.CodeOf(err))

	_, err = svc.AdjustInventory(ctx, item.ID, loc.ID, 4)
	require.Error(t, err)
	assert.Equal(t, service.CodeLocationClosed, errors.CodeOf(err))

	// It left the listing and it is still read.
	read, err := svc.GetStockLocation(ctx, loc.ID)
	require.NoError(t, err)
	assert.True(t, read.Closed())

	assert.NotContains(t, locationIDs(ctx, t, svc, false), loc.ID,
		"a closed location is not one an operator can pick")
	assert.Contains(t, locationIDs(ctx, t, svc, true), loc.ID,
		"asking for the closed ones has to find it: the levels and reservations "+
			"of the past name it, and their reader has to be able to look it up")

	again, err := svc.CloseStockLocation(ctx, loc.ID)
	require.NoError(t, err, "a retried decommission has to be able to finish")
	require.NotNil(t, again.ClosedAt)
	assert.Equal(t, *closed.ClosedAt, *again.ClosedAt,
		"the second close must not overwrite the first moment")
}

// TestACloseAndAStockWriteCannotBothWin is the reason the location lock exists.
//
// The close counts what the location holds and then stamps it. Without the row
// as a rendezvous point, a stock write committing between the two would leave
// units standing in a closed location — and no availability read joins
// stock_locations, so nobody would ever see them. Whichever transaction gets
// there first, the other one has to lose: either the write commits and the
// close refuses over the stock, or the close commits and the write refuses
// against a closed location.
func TestACloseAndAStockWriteCannotBothWin(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	item := addItem(ctx, t, svc)
	loc := addLocation(ctx, t, svc)

	var (
		wg                 sync.WaitGroup
		closeErr, writeErr error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, closeErr = svc.CloseStockLocation(ctx, loc.ID)
	}()
	go func() {
		defer wg.Done()
		_, writeErr = svc.SetInventoryLevel(ctx, item.ID, loc.ID, 5)
	}()
	wg.Wait()

	require.True(t, (closeErr == nil) != (writeErr == nil),
		"exactly one of the two has to lose; close=%v write=%v", closeErr, writeErr)

	final, err := svc.GetStockLocation(ctx, loc.ID)
	require.NoError(t, err)

	available, err := svc.AvailableQuantity(ctx, item.ID)
	require.NoError(t, err)

	if final.Closed() {
		assert.Zero(t, available, "a closed location cannot be left holding stock")
		return
	}
	assert.Equal(t, int64(5), available, "the write won, so the stock is there and the location is open")
}

// locationIDs lists the location ids the service returns.
func locationIDs(ctx context.Context, t *testing.T, svc *service.Service, includeClosed bool) []string {
	t.Helper()

	locations, _, err := svc.ListStockLocations(ctx, service.ListStockLocationsInput{
		IncludeClosed: includeClosed,
		Page:          service.Page{Limit: service.MaxLimit},
	})
	require.NoError(t, err)

	ids := make([]string, 0, len(locations))
	for i := range locations {
		ids = append(ids, locations[i].ID)
	}
	return ids
}
