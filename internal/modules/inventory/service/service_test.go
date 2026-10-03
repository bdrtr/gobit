package service_test

import (
	"context"
	"math"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// The fixed IDs the tests use.
const (
	itemID  = "invitem_TEST"
	locA    = "sloc_A"
	locB    = "sloc_B"
	resID   = "invres_TEST"
	unknown = "invitem_MISSING"
)

// TestAvailableQuantitySumsEveryLocation proves the sellable quantity is the sum
// of the (stocked - reserved) differences across ALL locations.
//
// The fixture is deliberate: the two locations' reserved quantities DIFFER, and
// the total physical quantity (30) is far from the total sellable quantity
// (18). An implementation that forgot to subtract the reserved part, or summed
// only one location, cannot hit this number.
func TestAvailableQuantitySumsEveryLocation(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 4)
	store.seedLevel(itemID, locB, 20, 8)

	available, err := svc.AvailableQuantity(context.Background(), itemID)

	require.NoError(t, err)
	assert.Equal(t, int64(18), available, "(10-4) + (20-8) has to be 18")
}

// TestAvailableQuantityOfAnItemWithNoLevelIsZero proves an item with no stock
// level at all returns zero; that is not an error.
func TestAvailableQuantityOfAnItemWithNoLevelIsZero(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")

	available, err := svc.AvailableQuantity(context.Background(), itemID)

	require.NoError(t, err)
	assert.Zero(t, available)
}

// TestAvailableQuantityOfAMissingItemIsNotFound proves a missing item returns
// NotFound, not zero: "it has no stock" and "it does not exist" are different
// situations and the caller has to be able to tell them apart.
func TestAvailableQuantityOfAMissingItemIsNotFound(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.AvailableQuantity(context.Background(), unknown)

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestLocationsWithStockReturnsOnlyTheLocationsThatSuffice proves a location
// that does not meet the threshold drops out of the list.
//
// The fixture is deliberate: BOTH LOCATIONS HOLD 10 PHYSICAL UNITS, and only
// their reserved quantities differ. An implementation that forgot to subtract
// the reserved part and looked at stocked_quantity would return both and fail
// this test. locA's sellable quantity is EXACTLY equal to the threshold (5); the
// bound has to be ">=" and not ">", because Reserve also allows setting aside
// exactly the last unit.
func TestLocationsWithStockReturnsOnlyTheLocationsThatSuffice(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 5)
	store.seedLevel(itemID, locB, 10, 6)

	locations, err := svc.LocationsWithStock(context.Background(), itemID, 5)

	require.NoError(t, err)
	assert.Equal(t, []string{locA}, locations,
		"locA has 10-5=5 (enough), locB has 10-6=4 (not enough)")
}

// TestLocationsWithStockAReservationLowersAvailability proves a location drops
// out of the list after a reservation.
//
// The real proof is the two assertions at the end: the list and
// [service.Service.Reserve] use THE SAME definition of "sellable", so a location
// missing from the list also gets a Conflict from Reserve. Had the two
// definitions drifted apart, a location returned as a candidate would blow up at
// the reservation and the saga could not find out why.
func TestLocationsWithStockAReservationLowersAvailability(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 0)

	locations, err := svc.LocationsWithStock(ctx, itemID, 5)
	require.NoError(t, err)
	require.Equal(t, []string{locA}, locations, "before the reservation it has to be a candidate")

	_, err = svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: itemID, LocationID: locA, Quantity: 6,
	})
	require.NoError(t, err)

	locations, err = svc.LocationsWithStock(ctx, itemID, 5)
	require.NoError(t, err)
	assert.Empty(t, locations, "the reservation lowered the sellable quantity to 4; no candidate is left for 5")

	_, err = svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: itemID, LocationID: locA, Quantity: 5,
	})
	require.Error(t, err)
	assert.Equal(t, service.CodeInsufficientStock, errors.CodeOf(err),
		"the list and Reserve have to use the same definition")
}

// TestLocationsWithStockOrderIsDeterministic proves the result comes back in
// ascending order of location ID.
//
// The fixture SEPARATES the store's order from the expected order (see
// [fakeStore.seedLevelWithID]): the store returns the levels in the order locC,
// locB, locA, and the result has to be locA, locB, locC. The order is the order
// of a stock fact; a preference order such as "most stocked first" would be a
// shipping policy, and that decision belongs to fulfillment — which is why the
// three locations' quantities DIFFER too, without affecting the result.
func TestLocationsWithStockOrderIsDeterministic(t *testing.T) {
	const locC = "sloc_C"

	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevelWithID("invlevel_3", itemID, locA, 10, 0)
	store.seedLevelWithID("invlevel_1", itemID, locC, 30, 0)
	store.seedLevelWithID("invlevel_2", itemID, locB, 20, 0)

	locations, err := svc.LocationsWithStock(context.Background(), itemID, 5)

	require.NoError(t, err)
	assert.Equal(t, []string{locA, locB, locC}, locations)
	assert.True(t, slices.IsSorted(locations), "the order has to be ascending by location ID")
}

// TestLocationsWithStockWithNoCandidateIsAnEmptySlice proves that when no
// location suffices an EMPTY, NON-nil slice comes back.
//
// "Not enough stock" is an answer, not a fault: returning an error would take
// away the caller's (the saga's) right to interpret the situation in its own
// context. Returning an empty slice rather than nil spares the caller from
// having to tell "none" apart from "never asked".
func TestLocationsWithStockWithNoCandidateIsAnEmptySlice(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")

	locations, err := svc.LocationsWithStock(ctx, itemID, 5)
	require.NoError(t, err)
	assert.Empty(t, locations, "an item with no level at all has no candidate")
	assert.NotNil(t, locations, "an empty slice has to come back, not nil")

	store.seedLevel(itemID, locA, 10, 8)

	locations, err = svc.LocationsWithStock(ctx, itemID, 5)
	require.NoError(t, err)
	assert.Empty(t, locations, "10-8=2 does not meet the threshold")
	assert.NotNil(t, locations, "an empty slice has to come back, not nil")
}

// TestLocationsWithStockNonPositiveQuantityIsInvalid proves a zero and a
// negative threshold are refused.
//
// Silently returning every location would list candidates for a quantity
// Reserve refuses OUTRIGHT; returning an empty list would hide the caller's
// mistake by making it look like "no stock". Both move the cause away from the
// caller.
func TestLocationsWithStockNonPositiveQuantityIsInvalid(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 0)

	for _, quantity := range []int64{0, -1} {
		_, err := svc.LocationsWithStock(ctx, itemID, quantity)

		require.Error(t, err)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
	}
}

// TestLocationsWithStockMissingItemIsNotFound proves a missing item returns
// NotFound, not an empty list; the same distinction as
// [service.Service.AvailableQuantity]: "it has no stock" and "it does not exist"
// are different situations for the caller.
func TestLocationsWithStockMissingItemIsNotFound(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.LocationsWithStock(context.Background(), unknown, 1)

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestAdjustInventoryRefusesNegativeStock proves a correction that would take
// the stock negative is refused with Conflict and NOTHING is written.
func TestAdjustInventoryRefusesNegativeStock(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 5, 0)

	_, err := svc.AdjustInventory(context.Background(), itemID, locA, -6)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeInsufficientStock, errors.CodeOf(err))
	assert.Equal(t, int64(5), store.level(itemID, locA).StockedQuantity,
		"a refused correction must not touch the stock")
}

// TestAdjustInventoryCanGoDownToZero proves the bound is exactly zero: -5 is
// accepted, -6 is not.
func TestAdjustInventoryCanGoDownToZero(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 5, 0)

	level, err := svc.AdjustInventory(context.Background(), itemID, locA, -5)

	require.NoError(t, err)
	assert.Zero(t, level.StockedQuantity)
	assert.Zero(t, level.Available())
}

// TestAdjustInventoryCannotGoBelowReserved proves a correction that goes below
// the reserved quantity is refused. The bound is NOT zero, it is the reserved
// quantity: on stock of 5 physical / 3 reserved, a correction of -3 would bring
// the physical quantity down to 2 and the sellable quantity would be -1.
func TestAdjustInventoryCannotGoBelowReserved(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 5, 3)

	_, err := svc.AdjustInventory(context.Background(), itemID, locA, -3)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, int64(5), store.level(itemID, locA).StockedQuantity)

	// Going down as far as the reserved quantity is allowed.
	level, err := svc.AdjustInventory(context.Background(), itemID, locA, -2)
	require.NoError(t, err)
	assert.Equal(t, int64(3), level.StockedQuantity)
	assert.Zero(t, level.Available())
}

// TestAdjustInventoryRaisesStock proves a positive correction raises the stock
// and does not touch the reserved quantity.
func TestAdjustInventoryRaisesStock(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 5, 2)

	level, err := svc.AdjustInventory(context.Background(), itemID, locA, 7)

	require.NoError(t, err)
	assert.Equal(t, int64(12), level.StockedQuantity)
	assert.Equal(t, int64(2), level.ReservedQuantity, "the reserved quantity must not change")
	assert.Equal(t, int64(10), level.Available())
}

// TestAdjustInventoryZeroDeltaIsInvalid proves a meaningless correction is not
// silently counted as a success.
func TestAdjustInventoryZeroDeltaIsInvalid(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 5, 0)

	_, err := svc.AdjustInventory(context.Background(), itemID, locA, 0)

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestAdjustInventoryWithoutALevelIsNotFound proves a correction for an (item,
// location) pair that has no level returns NotFound; the level is not created
// on its own.
func TestAdjustInventoryWithoutALevelIsNotFound(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")

	_, err := svc.AdjustInventory(context.Background(), itemID, locA, 5)

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestSetInventoryLevelCreates proves the level is created when there is none.
func TestSetInventoryLevelCreates(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLocation(locA)

	level, err := svc.SetInventoryLevel(context.Background(), itemID, locA, 12)

	require.NoError(t, err)
	assert.Equal(t, int64(12), level.StockedQuantity)
	assert.Zero(t, level.ReservedQuantity)
	assert.Equal(t, itemID, level.InventoryItemID)
	assert.Equal(t, locA, level.LocationID)
	assert.NotEmpty(t, level.ID)
}

// TestSetInventoryLevelUpdates proves an existing level's physical quantity is
// written as an absolute value and the reserved quantity is NOT TOUCHED.
func TestSetInventoryLevelUpdates(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 5, 3)

	level, err := svc.SetInventoryLevel(context.Background(), itemID, locA, 9)

	require.NoError(t, err)
	assert.Equal(t, int64(9), level.StockedQuantity)
	assert.Equal(t, int64(3), level.ReservedQuantity)
	assert.Equal(t, int64(6), level.Available())
}

// TestSetInventoryLevelCannotGoBelowReserved proves a stock count correction
// cannot destroy promised stock.
func TestSetInventoryLevelCannotGoBelowReserved(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 5, 3)

	_, err := svc.SetInventoryLevel(context.Background(), itemID, locA, 2)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, int64(5), store.level(itemID, locA).StockedQuantity)
}

// TestSetInventoryLevelNegativeQuantityIsInvalid proves a negative physical
// quantity is refused.
func TestSetInventoryLevelNegativeQuantityIsInvalid(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")

	_, err := svc.SetInventoryLevel(context.Background(), itemID, locA, -1)

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestSetInventoryLevelMissingItemIsNotFound proves no level can be opened for
// a missing item.
func TestSetInventoryLevelMissingItemIsNotFound(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.SetInventoryLevel(context.Background(), unknown, locA, 5)

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestReserveRaisesReservedQuantity proves a successful reservation raises the
// reserved quantity, does NOT TOUCH the physical quantity and leaves an active
// record.
func TestReserveRaisesReservedQuantity(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 2)

	res, err := svc.Reserve(context.Background(), service.ReserveInput{
		InventoryItemID: itemID,
		LocationID:      locA,
		Quantity:        3,
		LineItemID:      "li_1",
	})

	require.NoError(t, err)
	assert.Equal(t, models.ReservationActive, res.Status)
	assert.Equal(t, int64(3), res.Quantity)
	assert.Equal(t, "li_1", res.LineItemID)

	level := store.level(itemID, locA)
	assert.Equal(t, int64(10), level.StockedQuantity, "the physical quantity must not change")
	assert.Equal(t, int64(5), level.ReservedQuantity)
	assert.Equal(t, int64(5), level.Available())
}

// TestReserveInsufficientStockIsConflict proves more than the sellable quantity
// cannot be reserved and the refused request leaves no trace. The bound is NOT
// the physical quantity, it is the sellable one.
func TestReserveInsufficientStockIsConflict(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 8) // sellable: 2

	_, err := svc.Reserve(context.Background(), service.ReserveInput{
		InventoryItemID: itemID, LocationID: locA, Quantity: 3,
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeInsufficientStock, errors.CodeOf(err))
	assert.Equal(t, int64(8), store.level(itemID, locA).ReservedQuantity,
		"a refused reservation must not touch the reserved quantity")
}

// TestReserveExactlyTheLastUnit proves ALL of the sellable quantity can be
// reserved; the bound has to be "<=" and not "<".
func TestReserveExactlyTheLastUnit(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 8)

	_, err := svc.Reserve(context.Background(), service.ReserveInput{
		InventoryItemID: itemID, LocationID: locA, Quantity: 2,
	})

	require.NoError(t, err)
	level := store.level(itemID, locA)
	assert.Equal(t, int64(10), level.ReservedQuantity)
	assert.Zero(t, level.Available())
}

// TestReserveNonPositiveQuantityIsInvalid proves a zero and a negative quantity
// are refused.
func TestReserveNonPositiveQuantityIsInvalid(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 0)

	for _, qty := range []int64{0, -1} {
		_, err := svc.Reserve(context.Background(), service.ReserveInput{
			InventoryItemID: itemID, LocationID: locA, Quantity: qty,
		})
		require.Error(t, err, "a quantity of %d has to be refused", qty)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	}
}

// TestReserveRollsBackStockWhenTheReservationCannotBeWritten proves that when
// the reservation record cannot be created, the level update is rolled back
// too: the stock and the reservation record live in the same transaction, and
// neither can remain without the other.
func TestReserveRollsBackStockWhenTheReservationCannotBeWritten(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 0)
	store.failCreateReservation = errors.Internal("test_hata", "the reservation could not be written")

	_, err := svc.Reserve(context.Background(), service.ReserveInput{
		InventoryItemID: itemID, LocationID: locA, Quantity: 4,
	})

	require.Error(t, err)
	assert.Zero(t, store.level(itemID, locA).ReservedQuantity,
		"the transaction was rolled back, so the reserved quantity must not grow")
}

// TestReleaseReservationIdempotent proves the compensation can be called twice
// and the second call does NOT TOUCH the stock.
//
// A saga compensation has to be safe to run again; if the second call returned
// an error, the workflow's rollback path would blow up. That the second call
// does not lower the reserved quantity once more matters at least as much — had
// it done so, stock would have been created out of nothing.
func TestReleaseReservationIdempotent(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 3)
	store.seedReservation(resID, itemID, locA, 3, models.ReservationActive)

	require.NoError(t, svc.ReleaseReservation(context.Background(), resID))
	assert.Zero(t, store.level(itemID, locA).ReservedQuantity)
	assert.Equal(t, models.ReservationReleased, store.reservation(resID).Status)

	writes := store.updateLevelCalls

	require.NoError(t, svc.ReleaseReservation(context.Background(), resID),
		"the second call must not return an error")
	assert.Zero(t, store.level(itemID, locA).ReservedQuantity,
		"the second call must not lower the reserved quantity once more")
	assert.Equal(t, int64(10), store.level(itemID, locA).StockedQuantity)
	assert.Equal(t, writes, store.updateLevelCalls,
		"the second call must not write to the stock level at all")
}

// TestReleaseReservationUnknownIDIsNotFound proves idempotency does not mean
// "swallow everything": an ID that never existed is an error.
func TestReleaseReservationUnknownIDIsNotFound(t *testing.T) {
	svc, _ := newService(t)

	err := svc.ReleaseReservation(context.Background(), "invres_MISSING")

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestReleaseReservationConfirmedIsConflict proves a confirmed reservation
// cannot be taken back; the stock has been physically deducted.
func TestReleaseReservationConfirmedIsConflict(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 7, 0)
	store.seedReservation(resID, itemID, locA, 3, models.ReservationConfirmed)

	err := svc.ReleaseReservation(context.Background(), resID)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeReservationNotActive, errors.CodeOf(err))
	assert.Equal(t, int64(7), store.level(itemID, locA).StockedQuantity)
}

// TestReleaseReservationInconsistentStateIsInternal proves that on corrupt data,
// where the reserved quantity is smaller than the reservation, the quantity is
// not silently clipped to zero and an error comes back. Clipping would hide the
// inconsistency for good.
func TestReleaseReservationInconsistentStateIsInternal(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 1)
	store.seedReservation(resID, itemID, locA, 5, models.ReservationActive)

	err := svc.ReleaseReservation(context.Background(), resID)

	require.Error(t, err)
	assert.Equal(t, errors.KindInternal, errors.KindOf(err))
	assert.Equal(t, service.CodeInconsistentState, errors.CodeOf(err))
	assert.Equal(t, models.ReservationActive, store.reservation(resID).Status,
		"on an error the reservation's status must not change")
}

// TestConfirmReservationDeductsStock proves the confirmation lowers both the
// physical and the reserved quantity and DOES NOT CHANGE the sellable quantity.
func TestConfirmReservationDeductsStock(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 4)
	store.seedReservation(resID, itemID, locA, 4, models.ReservationActive)
	availableBefore := store.level(itemID, locA).Available()

	require.NoError(t, svc.ConfirmReservation(context.Background(), resID, testSaleOrderID))

	level := store.level(itemID, locA)
	assert.Equal(t, int64(6), level.StockedQuantity)
	assert.Zero(t, level.ReservedQuantity)
	assert.Equal(t, availableBefore, level.Available(),
		"the confirmation must not change the sellable quantity; the units had already been promised")
	assert.Equal(t, models.ReservationConfirmed, store.reservation(resID).Status)
}

// TestConfirmReservationIdempotent proves the confirmation can be called a
// second time and does not deduct the stock a second time.
func TestConfirmReservationIdempotent(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 4)
	store.seedReservation(resID, itemID, locA, 4, models.ReservationActive)

	require.NoError(t, svc.ConfirmReservation(context.Background(), resID, testSaleOrderID))
	writes := store.updateLevelCalls

	require.NoError(t, svc.ConfirmReservation(context.Background(), resID, testSaleOrderID))
	assert.Equal(t, int64(6), store.level(itemID, locA).StockedQuantity)
	assert.Equal(t, writes, store.updateLevelCalls)
}

// TestConfirmReservationReleasedIsConflict proves a released reservation cannot
// be confirmed.
func TestConfirmReservationReleasedIsConflict(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 0)
	store.seedReservation(resID, itemID, locA, 4, models.ReservationReleased)

	err := svc.ConfirmReservation(context.Background(), resID, testSaleOrderID)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, int64(10), store.level(itemID, locA).StockedQuantity)
}

// TestReserveReleaseReserveCycle proves the stock really is sellable again
// after the compensation. This cycle happens when a saga fails and is retried
// in Phase 6.
func TestReserveReleaseReserveCycle(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 1, 0)

	first, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: itemID, LocationID: locA, Quantity: 1,
	})
	require.NoError(t, err)

	_, err = svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: itemID, LocationID: locA, Quantity: 1,
	})
	require.Error(t, err, "with the last unit set aside there must be no second reservation")

	require.NoError(t, svc.ReleaseReservation(ctx, first.ID))

	second, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: itemID, LocationID: locA, Quantity: 1,
	})
	require.NoError(t, err, "after the compensation the unit has to be sellable again")
	assert.NotEqual(t, first.ID, second.ID)
	assert.Equal(t, int64(1), store.level(itemID, locA).ReservedQuantity)
}

// TestDeleteInventoryItemWithActiveReservationIsConflict proves an item with
// promised stock cannot be deleted.
func TestDeleteInventoryItemWithActiveReservationIsConflict(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 2)
	store.seedReservation(resID, itemID, locA, 2, models.ReservationActive)

	err := svc.DeleteInventoryItem(context.Background(), itemID)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeItemHasReservations, errors.CodeOf(err))

	_, getErr := svc.GetInventoryItem(context.Background(), itemID)
	require.NoError(t, getErr, "the item must not have been deleted")
}

// TestDeleteInventoryItemDeletesItsLevels proves finished reservations do not
// block the deletion and the item is deleted together with its levels.
func TestDeleteInventoryItemDeletesItsLevels(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 0)
	store.seedReservation(resID, itemID, locA, 2, models.ReservationReleased)

	require.NoError(t, svc.DeleteInventoryItem(ctx, itemID))

	_, err := svc.GetInventoryItem(ctx, itemID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Empty(t, store.level(itemID, locA).ID, "the levels have to be deleted too")
}

// TestCreateInventoryItemRequiresShippingByDefault proves that when the field is
// not sent, the item is assumed to require shipping.
func TestCreateInventoryItemRequiresShippingByDefault(t *testing.T) {
	svc, _ := newService(t)

	item, err := svc.CreateInventoryItem(context.Background(), service.CreateInventoryItemInput{
		SKU: " SKU-BOSLUKLU ",
	})

	require.NoError(t, err)
	assert.True(t, item.RequiresShipping)
	assert.Equal(t, "SKU-BOSLUKLU", item.SKU, "the spaces around the sku have to be trimmed")
	assert.Contains(t, item.ID, models.InventoryItemIDPrefix)
}

// TestCreateInventoryItemShippingCanBeTurnedOff proves an explicit false
// overrides the default.
func TestCreateInventoryItemShippingCanBeTurnedOff(t *testing.T) {
	svc, _ := newService(t)
	noShipping := false

	item, err := svc.CreateInventoryItem(context.Background(), service.CreateInventoryItemInput{
		SKU: "DIJITAL-1", RequiresShipping: &noShipping,
	})

	require.NoError(t, err)
	assert.False(t, item.RequiresShipping)
}

// TestCreateInventoryItemEmptySKUIsInvalid proves an empty SKU is refused.
func TestCreateInventoryItemEmptySKUIsInvalid(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.CreateInventoryItem(context.Background(), service.CreateInventoryItemInput{SKU: "   "})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestCreateStockLocationValidatesCountryCode proves the country code is
// normalized to two letters and an invalid one is refused.
func TestCreateStockLocationValidatesCountryCode(t *testing.T) {
	svc, _ := newService(t)

	loc, err := svc.CreateStockLocation(context.Background(), service.CreateStockLocationInput{
		Name: "Merkez Depo", CountryCode: "tr", City: "Istanbul",
	})
	require.NoError(t, err)
	assert.Equal(t, "TR", loc.CountryCode)
	assert.Contains(t, loc.ID, models.StockLocationIDPrefix)

	_, err = svc.CreateStockLocation(context.Background(), service.CreateStockLocationInput{
		Name: "Faulty", CountryCode: "TUR",
	})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestCreateStockLocationEmptyNameIsInvalid proves a location without a name is
// refused.
func TestCreateStockLocationEmptyNameIsInvalid(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.CreateStockLocation(context.Background(), service.CreateStockLocationInput{Name: " "})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestListPaginationLimits tests the limit/offset validation and the default.
func TestListPaginationLimits(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	for _, id := range []string{"sloc_1", "sloc_2", "sloc_3"} {
		store.locations[id] = models.StockLocation{ID: id, Name: id}
	}

	page, count, err := svc.ListStockLocations(ctx, service.ListStockLocationsInput{Page: service.Page{Limit: 2}})
	require.NoError(t, err)
	assert.Len(t, page, 2)
	assert.Equal(t, int64(3), count, "count has to report the total, not the page")

	page, _, err = svc.ListStockLocations(ctx, service.ListStockLocationsInput{Page: service.Page{Limit: 2, Offset: 2}})
	require.NoError(t, err)
	assert.Len(t, page, 1)

	_, _, err = svc.ListStockLocations(ctx, service.ListStockLocationsInput{Page: service.Page{Limit: service.MaxLimit + 1}})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, _, err = svc.ListStockLocations(ctx, service.ListStockLocationsInput{Page: service.Page{Offset: -1}})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestListInventoryItemsFilters tests the SKU and shipping filters.
func TestListInventoryItemsFilters(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	store.seedItem("invitem_1", "SKU-1")
	store.seedItem("invitem_2", "SKU-2")

	sku := "SKU-2"
	items, count, err := svc.ListInventoryItems(ctx, service.ListInventoryItemsInput{SKU: &sku})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "invitem_2", items[0].ID)
	assert.Equal(t, int64(1), count)

	noShipping := false
	items, _, err = svc.ListInventoryItems(ctx, service.ListInventoryItemsInput{RequiresShipping: &noShipping})
	require.NoError(t, err)
	assert.Empty(t, items)
}

// TestAdjustInventoryCatchesOverflow proves a correction past the int64 limit
// produces an error instead of silently wrapping. Had it wrapped, the result
// would turn negative and slip past every quantity check.
func TestAdjustInventoryCatchesOverflow(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, math.MaxInt64, 0)

	_, err := svc.AdjustInventory(context.Background(), itemID, locA, 1)

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, int64(math.MaxInt64), store.level(itemID, locA).StockedQuantity)
}

// TestConfirmReservationInsufficientPhysicalStockIsInternal proves the
// confirmation writes nothing in the corrupt state where it would take the
// physical stock negative.
func TestConfirmReservationInsufficientPhysicalStockIsInternal(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 2, 5)
	store.seedReservation(resID, itemID, locA, 3, models.ReservationActive)

	err := svc.ConfirmReservation(context.Background(), resID, testSaleOrderID)

	require.Error(t, err)
	assert.Equal(t, errors.KindInternal, errors.KindOf(err))
	assert.Equal(t, service.CodeInconsistentState, errors.CodeOf(err))
	assert.Equal(t, int64(2), store.level(itemID, locA).StockedQuantity)
}

// TestConfirmReservationInsufficientReservedQuantityIsInternal proves the
// confirmation is refused in the corrupt state where the reserved quantity is
// smaller than the reservation. The physical stock IS ENOUGH here; only the
// reserved quantity causes the error.
func TestConfirmReservationInsufficientReservedQuantityIsInternal(t *testing.T) {
	svc, store := newService(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 1)
	store.seedReservation(resID, itemID, locA, 5, models.ReservationActive)

	err := svc.ConfirmReservation(context.Background(), resID, testSaleOrderID)

	require.Error(t, err)
	assert.Equal(t, errors.KindInternal, errors.KindOf(err))
	assert.Equal(t, models.ReservationActive, store.reservation(resID).Status)
	assert.Equal(t, int64(10), store.level(itemID, locA).StockedQuantity)
}

// TestLockOrderItemBeforeLevel proves EVERY flow that touches a level takes the
// locks in the same order — the item first, then the level.
//
// The order is a concurrency contract: if a flow locked the level before the
// item, then on meeting a flow that locks the item first, the database would
// detect the deadlock and kill one of the transactions. Against the real
// database a violation shows only under a RACE; here it is read directly.
func TestLockOrderItemBeforeLevel(t *testing.T) {
	flows := []struct {
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
			return svc.ConfirmReservation(ctx, resID, testSaleOrderID)
		}},
	}

	for _, flow := range flows {
		t.Run(flow.name, func(t *testing.T) {
			svc, store := newService(t)
			store.seedItem(itemID, "SKU-1")
			store.seedLevel(itemID, locA, 10, 5)
			store.seedReservation(resID, itemID, locA, 5, models.ReservationActive)

			require.NoError(t, flow.call(context.Background(), svc))

			locks := store.lockOrder()
			require.Contains(t, locks, "item", "the flow never took the item lock: %v", locks)
			require.Contains(t, locks, "level", "the flow never took the level lock: %v", locks)
			assert.Less(t, slices.Index(locks, "item"), slices.Index(locks, "level"),
				"the item lock has to be taken BEFORE the level lock, order taken: %v", locks)
		})
	}
}
