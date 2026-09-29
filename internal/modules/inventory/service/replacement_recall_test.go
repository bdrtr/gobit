package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// TestACanceledParcelsUnitsGoBackOnce is ADR 0239's put-back: a confirmed
// replacement promise's units return to the shelf they left, as a
// cancellation naming the promise, and a second call finds it and writes
// nothing.
func TestACanceledParcelsUnitsGoBackOnce(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 0)
	ctx := context.Background()
	reservation, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: itemID, LocationID: locA, Quantity: 4, Purpose: models.PurposeReplacement,
	})
	require.NoError(t, err)
	require.NoError(t, svc.ConfirmReservation(ctx, reservation.ID, ""))

	level, err := svc.RecallReplacementUnits(ctx, reservation.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(10), level.StockedQuantity)

	_, err = svc.RecallReplacementUnits(ctx, reservation.ID)
	require.ErrorIs(t, err, models.ErrMovementAlreadyRecorded)

	ledger := store.movementsFor(itemID)
	require.Len(t, ledger, 2, "the replacement out, the recall back, and nothing more")
	assert.Equal(t, models.MovementCancellation, ledger[1].Reason)
	assert.Equal(t, int64(4), ledger[1].Delta)
	assert.Equal(t, reservation.ID, ledger[1].Reference)
	assert.Empty(t, ledger[1].ReservationID, "a cancellation names no promise it consumed")
}

// TestOnlyAConfirmedReplacementPromiseIsRecalled refuses what would add units
// to a shelf that never lost them: a promise still held, and a sale's.
func TestOnlyAConfirmedReplacementPromiseIsRecalled(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 0)
	ctx := context.Background()

	held, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: itemID, LocationID: locA, Quantity: 2, Purpose: models.PurposeReplacement,
	})
	require.NoError(t, err)
	_, err = svc.RecallReplacementUnits(ctx, held.ID)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "a held promise took nothing out of the count: %v", err)

	sold, err := svc.Reserve(ctx, service.ReserveInput{InventoryItemID: itemID, LocationID: locA, Quantity: 3})
	require.NoError(t, err)
	require.NoError(t, svc.ConfirmReservation(ctx, sold.ID, testSaleOrderID))
	_, err = svc.RecallReplacementUnits(ctx, sold.ID)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "a sale's units go back through its line: %v", err)

	for _, mv := range store.movementsFor(itemID) {
		assert.NotEqual(t, models.MovementCancellation, mv.Reason, "nothing was put back")
	}
}
