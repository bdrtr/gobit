package ordercancel_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// The tests here are ADR 0420: a cancellation counts the order's live parcels
// the way the fulfillment module holds a new one to its order, by the reference
// it stores and under the order's dispatch lock, rather than through the
// order's link, which a parcel whose link write failed does not have (D264).

// TestAWriteOffCountsAParcelTheOrdersLinkDoesNotName is a line write-off on an
// order whose five units are all in a parcel the link does not name. Counted
// through the link, nothing was in a box and two units went back on the shelf
// while they sat in the parcel; counted by reference, none do.
func TestAWriteOffCountsAParcelTheOrdersLinkDoesNotName(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	delete(h.links[linkOrderFulfillment], testOrderID)
	h.committed[testLineItemID] = 5

	require.NoError(t, h.handle(t, canceledEvent(5, 0, 2)))

	assert.Zero(t, h.inventory.returned, "the units in the unlinked parcel stay in it")
	assert.Empty(t, h.inventory.calls, "a return of nothing must not reach the inventory module")
}

// TestAParcelCancelCountsTheOrdersUnlinkedParcels is a parcel cancel on an
// order with three of five units written off: the canceled parcel is the one
// the link names, and another live parcel holding three units has no link.
// Only two units may come back, the ones no live parcel holds; counted through
// the link, the unlinked parcel was missed and all three went back.
func TestAParcelCancelCountsTheOrdersUnlinkedParcels(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.lines = orderLinesJSON(5, 3)
	h.held[testLineItemID] = 3
	h.committedOf = map[string]map[string]int64{
		testFulfillmentID: {},
		"ful_unlinked":    {testLineItemID: 3},
	}

	require.NoError(t, h.handleParcel(t, parcelEvent(testFulfillmentID)))

	assert.Equal(t, int64(2), h.inventory.returned,
		"bought 5, three in a live parcel the link does not name: two belong on the shelf")
}

// TestAWriteOffThenTheBoxCancelPutsAnUnlinkedParcelsUnitsBack is ADR 0135's
// prescribed path for a parcel whose link write failed: an order of two units,
// both in that parcel, both written off, then the box canceled. The write-off
// counts the parcel and puts nothing back; the cancel finds no order through
// the link and acts on the parcel's reference, so both units end on the shelf.
func TestAWriteOffThenTheBoxCancelPutsAnUnlinkedParcelsUnitsBack(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	delete(h.links[linkOrderFulfillment], testOrderID)
	h.committedOf = map[string]map[string]int64{testFulfillmentID: {testLineItemID: 2}}

	require.NoError(t, h.handle(t, canceledEvent(2, 0, 2)))
	assert.Zero(t, h.inventory.returned, "the written-off units are in the box")

	h.committedOf = map[string]map[string]int64{}
	h.held = map[string]int64{testLineItemID: 2}
	h.lines = orderLinesJSON(2, 2)

	require.NoError(t, h.handleParcel(t, parcelEvent(testFulfillmentID)))
	assert.Equal(t, int64(2), h.inventory.returned, "the canceled box's units are back on the shelf")
}

// TestAReturnParcelsCancelPutsNothingBack is a canceled parcel that was
// bringing one unit of a return back: no link names an order, the event names
// its return, and the order bought five, keeps two in a live outgoing parcel
// and wrote three off. A return parcel holds none of the order's outgoing
// units, so the act does not run: the count is not asked and nothing is put
// back (ADR 0420).
func TestAReturnParcelsCancelPutsNothingBack(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	delete(h.links[linkOrderFulfillment], testOrderID)
	h.held = map[string]int64{testLineItemID: 1}
	h.lines = orderLinesJSON(5, 3)
	h.committedByRef = map[string]map[string]int64{testOrderID: {testLineItemID: 2}}
	e := parcelEvent(testFulfillmentID)
	e.Data["return_id"] = "ret_COMINGBACK"

	require.NoError(t, h.handleParcel(t, e))

	assert.Empty(t, h.heldAsked, "the order's outgoing parcels are not counted for a return parcel")
	assert.Zero(t, h.inventory.returned)
	assert.Empty(t, h.inventory.calls)
}

// TestAWriteOffCountsTheOrderItWritesOff pins the reference the write-off asks
// about: the order whose line was canceled.
func TestAWriteOffCountsTheOrderItWritesOff(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.committedByRef = map[string]map[string]int64{testOrderID: {testLineItemID: 5}}

	require.NoError(t, h.handle(t, canceledEvent(5, 0, 2)))

	assert.Equal(t, []string{testOrderID}, h.heldAsked)
	assert.Zero(t, h.inventory.returned, "every unit is in the order's parcels")
}

// TestEachBoundOrderIsCountedByItsOwnReference is a canceled parcel bound to
// its order and to an addition (ADR 0197), holding a line of each: every line
// is put back against what its own order's live parcels hold.
//
// The order sold five of its line, wrote off five, and keeps two in another
// parcel: three come back. The addition sold two, wrote off two, and keeps one:
// one comes back. Counting either line against the other order gives five or
// two instead.
func TestEachBoundOrderIsCountedByItsOwnReference(t *testing.T) {
	t.Parallel()

	const addition = "order_00ADDITIONJOINED00" // sorts before testOrderID
	const additionLine = "oli_ADDITION"
	h := newHarness(t)
	h.links[linkOrderFulfillment] = map[string][]string{
		testOrderID: {testFulfillmentID},
		addition:    {testFulfillmentID},
	}
	h.held = map[string]int64{testLineItemID: 3, additionLine: 1}
	h.linesByOrder = map[string]string{
		addition: `[{"line_item_id":"` + additionLine + `","bought":2,"canceled":2,"variant_id":"` +
			testVariantID + `"}]`,
		testOrderID: orderLinesJSON(5, 5),
	}
	h.committedByRef = map[string]map[string]int64{
		testOrderID: {testLineItemID: 2},
		addition:    {additionLine: 1},
	}

	require.NoError(t, h.handleParcel(t, parcelEvent(testFulfillmentID)))

	assert.Equal(t, int64(3+1), h.inventory.returned)
	assert.ElementsMatch(t, []string{testOrderID, addition}, h.heldAsked, "each order is asked about once")
}

// TestABusyCountIsAskedAgainUntilTheParcelCommits is the module answering that
// a parcel of the order is being opened (ADR 0420): the flow waits, holding
// nothing, and asks again, and the answer it acts on is the one after.
func TestABusyCountIsAskedAgainUntilTheParcelCommits(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.flow.SetBusyWaits([]time.Duration{time.Millisecond, time.Millisecond, time.Millisecond})
	h.flow.SetDraw(func(n int64) int64 { return n })
	h.busyFor = 2
	h.committed[testLineItemID] = 3

	require.NoError(t, h.handle(t, canceledEvent(5, 0, 2)))

	assert.Len(t, h.heldAsked, 3, "asked twice while busy and once more")
	assert.Equal(t, int64(2), h.inventory.returned, "bought 5, 3 in parcels: the 2 written off come back")
}

// TestABusyCountThatOutlastsTheWaitsGoesBackToTheBus is the module still busy
// after the flow's last wait: the handler fails with a fault the bus tries
// again (ADR 0240), puts nothing back, and a later delivery counts correctly.
func TestABusyCountThatOutlastsTheWaitsGoesBackToTheBus(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.flow.SetBusyWaits([]time.Duration{time.Millisecond})
	h.flow.SetDraw(func(n int64) int64 { return n })
	h.busyFor = 2
	h.committed[testLineItemID] = 3

	err := h.handle(t, canceledEvent(5, 0, 2))
	require.Error(t, err)
	assert.Equal(t, coreerrors.KindUnavailable, coreerrors.KindOf(err), "the bus tries an unavailable fault again")
	assert.Zero(t, h.inventory.returned, "nothing was put back on a count it could not read")

	require.NoError(t, h.handle(t, canceledEvent(5, 0, 2)), "the bus's next delivery")
	assert.Equal(t, int64(2), h.inventory.returned)
}
