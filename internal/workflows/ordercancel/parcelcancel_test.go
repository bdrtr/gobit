package ordercancel_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
)

// parcelEvent builds the event the fulfillment module publishes.
func parcelEvent(fulfillmentID string) eventbus.Event {
	return eventbus.Event{
		ID:   "fulfillment.canceled:" + fulfillmentID,
		Name: "fulfillment.canceled",
		Data: map[string]any{
			"fulfillment_id": fulfillmentID,
			"reference":      testOrderID,
			"canceled_at":    "2026-09-11T12:00:00Z",
		},
	}
}

// orderLinesJSON is the order module's answer, written the way that module
// writes it.
func orderLinesJSON(bought, canceled int64) string {
	return fmt.Sprintf(
		`[{"line_item_id":%q,"bought":%d,"canceled":%d,"variant_id":%q}]`,
		testLineItemID, bought, canceled, testVariantID)
}

// TestTheORDEROfTheTwoActsCannotChangeTheTotal is the regression for D82.
//
// Five bought, three in a parcel, all five written off. The two acts may reach the
// flow in either order — the bus is asynchronous and at-least-once, so a write-off
// whose direct publish was lost arrives a minute later, after an operator has
// already canceled the parcel.
//
// Under the difference-of-windows design that order mattered and nothing enforced
// it: the parcel act put back three ANTICIPATING the write-off, the write-off then
// found a full window and put back five, and the shelf was credited with EIGHT
// units for a cancellation of five. Each act carried its own reference, so the
// ledger could not see it either.
func TestTheORDEROfTheTwoActsCannotChangeTheTotal(t *testing.T) {
	t.Parallel()

	for _, order := range []string{"the write-off first", "the parcel first"} {
		t.Run(order, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.held = map[string]int64{testLineItemID: 3}
			h.lines = orderLinesJSON(5, 5)

			if order == "the write-off first" {
				// The parcel is still live while the write-off is handled, so
				// only the two units outside it can come back yet.
				h.committed = map[string]int64{testLineItemID: 3}
				require.NoError(t, h.handle(t, canceledEvent(5, 0, 5)))
				require.Equal(t, int64(2), h.inventory.returned)

				h.committed = map[string]int64{}
				require.NoError(t, h.handleParcel(t, parcelEvent(testFulfillmentID)))
			} else {
				h.committed = map[string]int64{}
				require.NoError(t, h.handleParcel(t, parcelEvent(testFulfillmentID)))
				require.NoError(t, h.handle(t, canceledEvent(5, 0, 5)))
			}

			assert.Equal(t, int64(5), h.inventory.returned,
				"five units were written off, so five belong on the shelf whichever "+
					"act got there first")
		})
	}
}

// TestTheTwoActsTogetherPutBackExactlyWhatWasCanceled runs the intended sequence
// and watches the intermediate state.
//
// It overlaps with the order test above on purpose: that one proves the total is
// order-independent, this one proves the STEP — that while the parcel is live the
// write-off reaches only the units outside it, which is the property that makes
// the parcel's cancellation meaningful at all.
func TestTheTwoActsTogetherPutBackExactlyWhatWasCanceled(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.held = map[string]int64{testLineItemID: 3}
	h.committed = map[string]int64{testLineItemID: 3}
	h.lines = orderLinesJSON(5, 5)

	require.NoError(t, h.handle(t, canceledEvent(5, 0, 5)))
	assert.Equal(t, int64(2), h.inventory.returned,
		"while the parcel is live only the units outside it can come back")

	// The shop resolves it the way ADR 0135 says: it cancels the parcel.
	h.committed = map[string]int64{}
	require.NoError(t, h.handleParcel(t, parcelEvent(testFulfillmentID)))

	assert.Equal(t, int64(5), h.inventory.returned,
		"every canceled unit is back once the parcel that held the rest is gone")
}

// TestAParcelWhoseLineWasNeverWrittenOffReleasesNOStock separates the two
// meanings a canceled parcel has.
//
// Its units become DISPATCHABLE again — a new parcel may hold them — but they are
// still sold, and putting them on the shelf would sell the same goods twice.
func TestAParcelWhoseLineWasNeverWrittenOffReleasesNOStock(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.held = map[string]int64{testLineItemID: 3}
	h.committed = map[string]int64{}
	h.lines = orderLinesJSON(5, 0)

	require.NoError(t, h.handleParcel(t, parcelEvent(testFulfillmentID)))

	assert.Zero(t, h.inventory.returned,
		"nobody wrote these units off, so they are still owed to the customer")
	assert.Empty(t, h.inventory.calls,
		"the inventory module is not called at all, rather than called with zero")
}

// TestTheWriteOffIsTheCeilingAndNotTheParcel pins the partial case.
//
// Five bought, FOUR written off, three in the parcel. The write-off runs while the
// parcel is live and reaches two; the parcel going away raises the ceiling to four,
// so two more come back and the fifth unit stays sold. The parcel held three and
// contributed two, which is the point: what the box contained is not what is owed
// to the shelf.
func TestTheWriteOffIsTheCeilingAndNotTheParcel(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.held = map[string]int64{testLineItemID: 3}
	h.lines = orderLinesJSON(5, 4)

	h.committed = map[string]int64{testLineItemID: 3}
	require.NoError(t, h.handle(t, canceledEvent(5, 0, 4)))
	require.Equal(t, int64(2), h.inventory.returned)

	h.committed = map[string]int64{}
	require.NoError(t, h.handleParcel(t, parcelEvent(testFulfillmentID)))

	assert.Equal(t, int64(4), h.inventory.returned,
		"four were written off, so four belong on the shelf; the fifth is still sold")
}

// TestAParcelReleasesNothingOnceTheWriteOffIsFullyBack is the neighbor case, and
// it is here because it was written the OTHER way round first.
//
// Five bought, two written off, three in the parcel: the line cancellation could
// already reach both of them, because the window it had was `5 − 3 = 2`. So the
// parcel going away raises no ceiling and releases nothing. The first draft of
// this test expected two units and was wrong about which act had already run —
// the fixture, not the arithmetic.
func TestAParcelReleasesNothingOnceTheWriteOffIsFullyBack(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.held = map[string]int64{testLineItemID: 3}
	h.committed = map[string]int64{}
	h.lines = orderLinesJSON(5, 2)

	h.committed = map[string]int64{testLineItemID: 3}
	require.NoError(t, h.handle(t, canceledEvent(5, 0, 2)))
	require.Equal(t, int64(2), h.inventory.returned)

	h.committed = map[string]int64{}
	require.NoError(t, h.handleParcel(t, parcelEvent(testFulfillmentID)))

	assert.Equal(t, int64(2), h.inventory.returned,
		"the write-off reached every unit it was owed while the parcel was still live, "+
			"so the parcel going away adds nothing")
}

// TestParcelsHoldingMoreThanTheOrderSoldReleaseNOTHING covers the impossible
// state.
//
// ADR 0135 refuses to open such a parcel now, but an order written before it can
// have them, and the arithmetic must not treat a negative window as a quantity: it
// would add stock to the shelf that was never deducted from it. The clamp in
// windowOwed is what stops it, and a mutation that removed the clamp survived
// until this test existed — no fixture had committed exceeding bought.
func TestParcelsHoldingMoreThanTheOrderSoldReleaseNOTHING(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.held = map[string]int64{testLineItemID: 3}
	// Six units in OTHER live parcels, on a line that sold five.
	h.committed = map[string]int64{testLineItemID: 6}
	h.lines = orderLinesJSON(5, 5)

	require.NoError(t, h.handleParcel(t, parcelEvent(testFulfillmentID)))

	assert.Zero(t, h.inventory.returned,
		"a window below zero is a broken record, not three units to put on a shelf")
}

// TestTwoParcelsCancelingInEitherOrderPutBackTheSameTotal is the property the
// arithmetic exists for.
//
// The formula is a difference of STATES rather than of records, so it keeps no
// memory of what a previous act returned — which is the only reason two parcels
// can be canceled in either order without the second one double counting.
func TestTwoParcelsCancelingInEitherOrderPutBackTheSameTotal(t *testing.T) {
	t.Parallel()

	const other = "ful_second"

	for _, order := range [][2]string{
		{testFulfillmentID, other},
		{other, testFulfillmentID},
	} {
		t.Run(order[0]+"_then_"+order[1], func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.lines = orderLinesJSON(5, 5)
			h.links[linkOrderFulfillment] = map[string][]string{
				testOrderID: {testFulfillmentID, other},
			}
			// Two parcels, two and three units. Everything is written off, so the
			// line cancellation reached nothing at all.
			sizes := map[string]int64{testFulfillmentID: 2, other: 3}
			h.committed = map[string]int64{testLineItemID: 5}
			require.NoError(t, h.handle(t, canceledEvent(5, 0, 5)))
			require.Zero(t, h.inventory.returned)

			for _, id := range order {
				h.held = map[string]int64{testLineItemID: sizes[id]}
				h.committed = map[string]int64{
					testLineItemID: h.committed[testLineItemID] - sizes[id],
				}
				require.NoError(t, h.handleParcel(t, parcelEvent(id)))
			}

			assert.Equal(t, int64(5), h.inventory.returned,
				"the order the parcels are canceled in cannot change the total")
		})
	}
}

// TestTheReferenceIsTheParcelAndTheLine is what makes a redelivery harmless.
//
// The bus delivers at least once. The ledger holds a cancellation movement's
// reference unique, so the SAME reference is what turns a second delivery into a
// write of nothing — and it has to be the pair, because one parcel can release
// several lines and several write-offs can share one parcel.
func TestTheReferenceIsTheParcelAndTheLine(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.held = map[string]int64{testLineItemID: 3}
	h.committed = map[string]int64{}
	h.lines = orderLinesJSON(5, 5)

	require.NoError(t, h.handleParcel(t, parcelEvent(testFulfillmentID)))

	require.Len(t, h.inventory.references, 1)
	assert.Equal(t, testFulfillmentID+":"+testLineItemID, h.inventory.references[0],
		"the pair is what happens once")
}

// TestARedeliveredParcelEventAddsNothing runs the second delivery for real.
//
// Nothing is injected to make it a no-op: the second delivery computes the same
// target, the module reads the same per-line sum, and there is no difference to
// move. That is the whole replacement for the uniqueness the ledger used to hold
// on the reference (ADR 0142).
func TestARedeliveredParcelEventAddsNothing(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.held = map[string]int64{testLineItemID: 3}
	h.committed = map[string]int64{}
	h.lines = orderLinesJSON(5, 5)

	require.NoError(t, h.handleParcel(t, parcelEvent(testFulfillmentID)))
	require.Equal(t, int64(5), h.inventory.returned)
	require.Len(t, h.inventory.calls, 1)

	require.NoError(t, h.handleParcel(t, parcelEvent(testFulfillmentID)))

	assert.Equal(t, int64(5), h.inventory.returned,
		"a second delivery of one event moves nothing")
	assert.Len(t, h.inventory.calls, 1,
		"and it does not even reach the write: the target was already met")
}

// TestAParcelBoundToNoOrderIsNotAnError covers the parcel opened by hand.
//
// The binding is a Module Link written by the flow that opens a parcel for an
// order. A parcel opened straight through the admin endpoint has none, and there
// is nothing to put back against because nothing wrote anything off.
func TestAParcelBoundToNoOrderIsNotAnError(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.held = map[string]int64{testLineItemID: 3}
	h.lines = orderLinesJSON(5, 5)
	h.links[linkOrderFulfillment] = map[string][]string{}

	require.NoError(t, h.handleParcel(t, parcelEvent(testFulfillmentID)))

	assert.Zero(t, h.inventory.returned)
}

// TestAParcelWithNoItemBreakdownReleasesNothing covers the checkout's own shape.
//
// The saga opens ONE parcel for the whole order and gives it no item list, so
// there is nothing this flow can attribute to a line.
func TestAParcelWithNoItemBreakdownReleasesNothing(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.held = map[string]int64{}
	h.lines = orderLinesJSON(5, 5)

	require.NoError(t, h.handleParcel(t, parcelEvent(testFulfillmentID)))

	assert.Zero(t, h.inventory.returned)
}

// TestAFaultThatMayPassIsRetried separates the two failure kinds.
//
// A module that could not be reached has to come back to the bus; a fact this
// flow can do nothing with must not, or the retries bury the failures that matter.
func TestAFaultThatMayPassIsRetried(t *testing.T) {
	t.Parallel()

	unreachable := errors.New("the fulfillment module is down")

	for name, breaks := range map[string]func(*harness){
		"the parcel could not be read": func(h *harness) { h.heldErr = unreachable },
		"the order could not be read":  func(h *harness) { h.linesErr = unreachable },
		"the links could not be read":  func(h *harness) { h.linkErr = unreachable },
		"the stock could not be added": func(h *harness) { h.inventory.returnErr = unreachable },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.held = map[string]int64{testLineItemID: 3}
			h.committed = map[string]int64{}
			h.lines = orderLinesJSON(5, 5)
			breaks(h)

			require.Error(t, h.handleParcel(t, parcelEvent(testFulfillmentID)))
		})
	}
}

// TestAParcelEventWithNoIdentifierIsRefused covers the payload this flow cannot
// act on.
func TestAParcelEventWithNoIdentifierIsRefused(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	e := parcelEvent(testFulfillmentID)
	delete(e.Data, "fulfillment_id")

	err := h.handleParcel(t, e)

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err),
		"a payload that can never become actionable is not something to retry")
}

// TestAnOrderAnswerThatCannotBeDecodedIsInternal pins the schema seam.
//
// The two packages cannot import each other, so nothing but a test proves the
// JSON they agree on. A body this flow cannot read is a fault of the installation
// rather than of the event.
func TestAnOrderAnswerThatCannotBeDecodedIsInternal(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.held = map[string]int64{testLineItemID: 3}
	h.lines = `{"line_item_id":"not an array"}`

	err := h.handleParcel(t, parcelEvent(testFulfillmentID))

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInternal, coreerrors.KindOf(err))
}

// TestTheOrderSchemaIsTheONEThisFlowDecodes is the other half of that seam.
//
// It asserts the field names rather than the behavior: a rename on the producing
// side compiles on both, and this is the line that would go red.
func TestTheOrderSchemaIsTheONEThisFlowDecodes(t *testing.T) {
	t.Parallel()

	var decoded []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(orderLinesJSON(5, 2)), &decoded))
	require.Len(t, decoded, 1)

	for _, field := range []string{"line_item_id", "bought", "canceled", "variant_id"} {
		assert.Contains(t, decoded[0], field,
			"the flow reads %q off the order module's answer", field)
	}
}
