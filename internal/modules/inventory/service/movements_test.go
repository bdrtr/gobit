// This file is English because ADR 0012 makes language a property of the FILE
// and every new file is English; service_test.go beside it stays Turkish.
package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// TestSettingTheCountRecordsWhatMoved is the ledger's basic claim: the row says
// how much moved and what the count became, not just that something happened.
//
// The fixture starts at 10 and sets 4, so the delta (-6) and the result (4)
// are different numbers with different signs. A row that recorded the WRITTEN
// value as the delta, or the delta as the result, would pass on any fixture
// where they happen to agree.
func TestSettingTheCountRecordsWhatMoved(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 0)

	_, err := svc.SetInventoryLevel(context.Background(), itemID, locA, 4)
	require.NoError(t, err)

	ledger := store.movementsFor(itemID)
	require.Len(t, ledger, 1)
	assert.Equal(t, models.MovementStockCount, ledger[0].Reason)
	assert.Equal(t, int64(-6), ledger[0].Delta, "the delta is the CHANGE, not the value written")
	assert.Equal(t, int64(4), ledger[0].StockedAfter, "stocked_after is the count the write produced")
	assert.Equal(t, locA, ledger[0].LocationID)
	assert.Empty(t, ledger[0].ReservationID, "only a sale names a reservation")
}

// TestOpeningALevelWithStockRecordsIt covers the branch that CREATES the level
// rather than updating one, which is the path an operator takes when they stock
// a warehouse for the first time.
func TestOpeningALevelWithStockRecordsIt(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLocation(locA)

	_, err := svc.SetInventoryLevel(context.Background(), itemID, locA, 7)
	require.NoError(t, err)

	ledger := store.movementsFor(itemID)
	require.Len(t, ledger, 1)
	assert.Equal(t, int64(7), ledger[0].Delta)
	assert.Equal(t, int64(7), ledger[0].StockedAfter)
}

// TestOpeningAnEmptyLevelRecordsNothing pins the one write that is not a
// movement: opening a level at zero says the item is now TRACKED here, and
// nothing arrived.
func TestOpeningAnEmptyLevelRecordsNothing(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLocation(locA)

	_, err := svc.SetInventoryLevel(context.Background(), itemID, locA, 0)
	require.NoError(t, err)

	assert.Empty(t, store.movementsFor(itemID),
		"a level opened empty moved no goods; a row here would be a movement of nothing")
}

// TestConfirmingTheCountYouAlreadyHadRecordsNothing is the case the integration
// suite found, and the rule was originally wrong about it.
//
// An operator who counts a shelf and writes the number that was already there
// has done something real — they confirmed it — and it moved nothing. An
// earlier draft treated the reason offered for that write as an inconsistency
// and FAILED THE CALL, turning a legitimate stock count into an error. The
// level's own updated_at is where a visit that moved no goods belongs.
func TestConfirmingTheCountYouAlreadyHadRecordsNothing(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 0)

	_, err := svc.SetInventoryLevel(context.Background(), itemID, locA, 10)

	require.NoError(t, err, "writing the count that is already there is not an error")
	assert.Empty(t, store.movementsFor(itemID), "nothing moved, so the ledger says nothing")
}

// TestOneArithmeticTwoReasons is why RestockInventory exists as its own entry
// point.
//
// Both calls add three units through the same code. The LEDGER has to tell them
// apart, because "an operator corrected the count" and "a customer sent goods
// back" are two entries an operator reads differently, and a positive delta
// says neither.
func TestOneArithmeticTwoReasons(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 0)

	ctx := context.Background()
	_, err := svc.AdjustInventory(ctx, itemID, locA, 3)
	require.NoError(t, err)
	_, err = svc.RestockInventory(ctx, itemID, locA, 3)
	require.NoError(t, err)

	ledger := store.movementsFor(itemID)
	require.Len(t, ledger, 2)
	assert.Equal(t, models.MovementAdjustment, ledger[0].Reason)
	assert.Equal(t, models.MovementReturnRestock, ledger[1].Reason)
	assert.Equal(t, int64(3), ledger[0].Delta)
	assert.Equal(t, int64(16), ledger[1].StockedAfter)
}

// TestARestockOfNothingIsRefused holds the rule that moved out of the
// cross-module surface and into the service, where the other quantity rules
// live.
func TestARestockOfNothingIsRefused(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 0)

	_, err := svc.RestockInventory(context.Background(), itemID, locA, 0)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInvalid))
	assert.Empty(t, store.movementsFor(itemID))
}

// TestAPromiseIsNotAMovement is question one of ADR 0068, held as a
// test rather than as a sentence.
//
// Reserving and releasing both write the level — the reserved half of it — and
// neither may leave a ledger row, because no unit went anywhere. The assertion
// is made after a FULL round trip so a release that recorded a "return" would
// be caught as loudly as a reserve that recorded a "sale".
func TestAPromiseIsNotAMovement(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 0)

	ctx := context.Background()
	reservation, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: itemID, LocationID: locA, Quantity: 4,
	})
	require.NoError(t, err)
	require.NoError(t, svc.ReleaseReservation(ctx, reservation.ID))

	assert.Empty(t, store.movementsFor(itemID),
		"a reservation changes what is AVAILABLE and not what is present; "+
			"inventory_reservations is already that record")
}

// TestConfirmingRecordsTheSaleThatTookTheUnits is the other half of question
// one: the confirm is the ONE reservation transition that moves goods, and it
// is the only row that can name the promise they went out against — the fact
// B7 says audit_log can never hold.
func TestConfirmingRecordsTheSaleThatTookTheUnits(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 0)

	ctx := context.Background()
	reservation, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: itemID, LocationID: locA, Quantity: 4,
	})
	require.NoError(t, err)
	require.NoError(t, svc.ConfirmReservation(ctx, reservation.ID))

	ledger := store.movementsFor(itemID)
	require.Len(t, ledger, 1)
	assert.Equal(t, models.MovementSale, ledger[0].Reason)
	assert.Equal(t, int64(-4), ledger[0].Delta)
	assert.Equal(t, int64(6), ledger[0].StockedAfter)
	assert.Equal(t, reservation.ID, ledger[0].ReservationID)
}

// TestGoodsSentToSettleAClaimAreNotRecordedAsASale is why the promise carries a
// purpose.
//
// The arithmetic of the two is identical — units set aside, then taken out of
// the count — and the FACT is not: nobody paid for these. A ledger that called
// them a sale would answer "what happened to this item" with the one word that
// is wrong, and an operator reconciling a month of stock against a month of
// revenue would find a gap with no name.
func TestGoodsSentToSettleAClaimAreNotRecordedAsASale(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 0)

	ctx := context.Background()
	reservation, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: itemID, LocationID: locA, Quantity: 4,
		Purpose: models.PurposeReplacement,
	})
	require.NoError(t, err)
	assert.Equal(t, models.PurposeReplacement, reservation.Purpose,
		"the promise has to KEEP what it was made for; the confirm reads it back")

	require.NoError(t, svc.ConfirmReservation(ctx, reservation.ID))

	ledger := store.movementsFor(itemID)
	require.Len(t, ledger, 1)
	assert.Equal(t, models.MovementReplacement, ledger[0].Reason)
	assert.Equal(t, int64(-4), ledger[0].Delta, "goods sent leave the count")
	assert.Equal(t, int64(6), ledger[0].StockedAfter)
	assert.Equal(t, reservation.ID, ledger[0].ReservationID,
		"units that leave against a promise NAME the promise, whatever the promise was for")
}

// TestAPromiseWithNoPurposeIsASale keeps every reservation written before the
// column existed readable.
//
// The checkout saga was the module's only caller, so 'sale' is not a default
// chosen for convenience: it is what those rows really were.
func TestAPromiseWithNoPurposeIsASale(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 0)

	ctx := context.Background()
	reservation, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: itemID, LocationID: locA, Quantity: 1,
	})
	require.NoError(t, err)
	require.NoError(t, svc.ConfirmReservation(ctx, reservation.ID))

	ledger := store.movementsFor(itemID)
	require.Len(t, ledger, 1)
	assert.Equal(t, models.MovementSale, ledger[0].Reason)
}

// TestAPurposeNobodyDefinedIsRefused keeps the column's vocabulary closed.
func TestAPurposeNobodyDefinedIsRefused(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 0)

	_, err := svc.Reserve(context.Background(), service.ReserveInput{
		InventoryItemID: itemID, LocationID: locA, Quantity: 1,
		Purpose: models.ReservationPurpose("gift"),
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Empty(t, store.movementsFor(itemID))
}

// TestAnAlreadyConfirmedReservationRecordsNothingTwice covers the idempotent
// path. A second confirm returns success and must not put a second sale in the
// ledger; a ledger that double-counts a retry is worse than no ledger, because
// its numbers look authoritative.
func TestAnAlreadyConfirmedReservationRecordsNothingTwice(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 4)
	store.seedReservation(resID, itemID, locA, 4, models.ReservationActive)

	ctx := context.Background()
	require.NoError(t, svc.ConfirmReservation(ctx, resID))
	require.NoError(t, svc.ConfirmReservation(ctx, resID))

	assert.Len(t, store.movementsFor(itemID), 1)
}

// TestAFailedTransactionLeavesNoMovement is the drift half of ADR 0068.
//
// The confirm writes the level, appends the movement and THEN stamps the
// reservation. Failing the last step is the case that matters: if the ledger
// row survived the rollback it would report units that never left, and the
// column it explains would say otherwise — which is exactly the divergence the
// decision accepts responsibility for by keeping the column authoritative.
func TestAFailedTransactionLeavesNoMovement(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 4)
	store.seedReservation(resID, itemID, locA, 4, models.ReservationActive)
	store.failSetReservationStatus = errors.Internal("boom", "the status could not be written")

	require.Error(t, svc.ConfirmReservation(context.Background(), resID))

	assert.Empty(t, store.movementsFor(itemID),
		"the movement has to roll back with the count it explains")
	assert.Equal(t, int64(10), store.level(itemID, locA).StockedQuantity,
		"the level itself has to roll back too, or the test proved nothing")
}

// TestAMovementCannotBeWrittenOutsideATransaction drives the refusal the store
// contract states, on the fake that mirrors it.
//
// The real refusal lives in the repository and is proved against a database in
// the integration test; this one keeps the property visible where the service's
// own rules are read.
func TestAMovementCannotBeWrittenOutsideATransaction(t *testing.T) {
	store := newFakeStore()

	_, err := store.AppendMovement(context.Background(), models.Movement{
		ID: "invmov_1", InventoryItemID: itemID, LocationID: locA,
		Reason: models.MovementAdjustment, Delta: 1, StockedAfter: 1,
	})

	require.Error(t, err,
		"a movement written outside the transaction that changed the count can commit "+
			"while that change rolls back")
}

// TestTheLedgerComesBackNewestFirst pins the order the listing's index serves.
//
// The three movements are seeded with DIFFERENT moments so the assertion is
// about the ordering rather than about insertion order; a listing that returned
// them in the order they were written would pass a same-moment fixture.
func TestTheLedgerComesBackNewestFirst(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedMovement("invmov_1", itemID, locA, models.MovementStockCount, 5, 5, hoursAgo(3))
	store.seedMovement("invmov_2", itemID, locB, models.MovementAdjustment, -2, 3, hoursAgo(2))
	store.seedMovement("invmov_3", itemID, locA, models.MovementReturnRestock, 1, 4, hoursAgo(1))

	got, err := svc.ListMovements(context.Background(), service.ListMovementsInput{
		InventoryItemID: itemID,
	})
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, []string{"invmov_3", "invmov_2", "invmov_1"},
		[]string{got[0].ID, got[1].ID, got[2].ID})
}

// TestTheLedgerPagesFromAPosition walks the listing the way a client does and
// proves the second page starts BELOW the first, with nothing dropped and
// nothing repeated.
func TestTheLedgerPagesFromAPosition(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedMovement("invmov_1", itemID, locA, models.MovementStockCount, 5, 5, hoursAgo(3))
	store.seedMovement("invmov_2", itemID, locA, models.MovementAdjustment, -2, 3, hoursAgo(2))
	store.seedMovement("invmov_3", itemID, locA, models.MovementAdjustment, 1, 4, hoursAgo(1))

	ctx := context.Background()
	first, err := svc.ListMovements(ctx, service.ListMovementsInput{
		InventoryItemID: itemID, Limit: 2,
	})
	require.NoError(t, err)
	require.Len(t, first, 2)

	last := first[len(first)-1]
	second, err := svc.ListMovements(ctx, service.ListMovementsInput{
		InventoryItemID: itemID, Limit: 2,
		After: page.Cursor{Time: last.CreatedAt, ID: last.ID},
	})
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.Equal(t, "invmov_1", second[0].ID)
}

// TestTheLedgerNarrowsToOneLocation covers the filter the listing offers
// without an index of its own.
func TestTheLedgerNarrowsToOneLocation(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedMovement("invmov_1", itemID, locA, models.MovementStockCount, 5, 5, hoursAgo(2))
	store.seedMovement("invmov_2", itemID, locB, models.MovementStockCount, 9, 9, hoursAgo(1))

	got, err := svc.ListMovements(context.Background(), service.ListMovementsInput{
		InventoryItemID: itemID, LocationID: locB,
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "invmov_2", got[0].ID)
}

// TestHalfAPositionIsRefused holds the pair the keyset rests on. A moment with
// no id is ambiguous between rows sharing it, and an id with no moment has
// nothing to compare against — both would silently return the wrong page.
func TestHalfAPositionIsRefused(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")

	_, err := svc.ListMovements(context.Background(), service.ListMovementsInput{
		InventoryItemID: itemID, After: page.Cursor{ID: "invmov_1"},
	})

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInvalid))
}

// TestAnUnknownItemHasNoHistoryAndSaysSo separates "nothing happened to this
// item" from "there is no such item"; a caller acts differently on the two.
func TestAnUnknownItemHasNoHistoryAndSaysSo(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.ListMovements(context.Background(), service.ListMovementsInput{
		InventoryItemID: unknown,
	})

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindNotFound))
}

// TestAReasonIsOneOfFour pins the closed set, in the type as well as in the
// schema's CHECK. An open string would let a caller write its own vocabulary
// into the one column an operator reads the table by.
func TestAReasonIsOneOfFour(t *testing.T) {
	valid := []models.MovementReason{
		models.MovementStockCount, models.MovementAdjustment,
		models.MovementSale, models.MovementReturnRestock,
	}
	for _, reason := range valid {
		assert.True(t, reason.Valid(), "%q is one of the four", reason)
	}

	assert.False(t, models.MovementReason("").Valid())
	assert.False(t, models.MovementReason("transfer").Valid())

	// The mapping the reader publishes: which reasons have a person behind them
	// and therefore a row in audit_log naming the caller.
	assert.True(t, models.MovementStockCount.FromAdminRequest())
	assert.True(t, models.MovementAdjustment.FromAdminRequest())
	assert.False(t, models.MovementSale.FromAdminRequest())
	assert.False(t, models.MovementReturnRestock.FromAdminRequest())
}

// hoursAgo is a moment in the past, so a fixture's ordering is deliberate
// rather than whatever the clock produced between two statements.
func hoursAgo(h int) time.Time {
	return time.Now().UTC().Add(-time.Duration(h) * time.Hour)
}
