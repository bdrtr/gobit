package ordercancel_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// This file holds ADR 0392 at the cancellation flow: a backordered line's units
// leave only when its claim is filled, so both acts withdraw from the claim what
// will not leave and credit the shelf only with what the line's stock lost, at
// the warehouse the claim was filled from.

// filledShelf is the warehouse a claim is filled at in these tests; the order's
// other line sold the item at testLocationID, which is the shelf D242 credited.
const filledShelf = "sloc_01CANCELSTOCKFILLED0"

// TestTheNetOffTheShelfIsTheSameInAnyOrder is the property: whatever order a
// parcel opening, write-offs, the parcel's cancellation and the claim's fill
// arrive in, what the line's stock lost less what went back is the units that
// will not come back — the ones still bought and the ones a live parcel holds —
// and the shelf never gains more than the line lost.
func TestTheNetOffTheShelfIsTheSameInAnyOrder(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		bought := rapid.Int64Range(1, 8).Draw(rt, "bought")
		backordered := rapid.Bool().Draw(rt, "backordered")

		type act struct {
			kind     string
			quantity int64
		}
		var acts []act
		for range rapid.IntRange(1, 3).Draw(rt, "write-offs") {
			acts = append(acts, act{"write off", rapid.Int64Range(1, bought).Draw(rt, "written off")})
		}
		if rapid.Bool().Draw(rt, "a parcel") {
			acts = append(acts, act{"open", rapid.Int64Range(1, bought).Draw(rt, "parcel")}, act{kind: "cancel"})
		}
		if backordered {
			acts = append(acts, act{kind: "fill"})
		}
		acts = rapid.Permutation(acts).Draw(rt, "order")

		h := newHarness(t)
		var deducted, canceled, committed int64
		parcelOpen := false
		claim := &fakeClaim{itemID: testItemID, quantity: bought}
		if backordered {
			h.inventory.claims = map[string]*fakeClaim{testLineItemID: claim}
		} else {
			deducted = bought
		}

		for _, a := range acts {
			switch a.kind {
			case "write off":
				quantity := min(a.quantity, bought-canceled)
				if quantity == 0 {
					continue
				}
				before := canceled
				canceled += quantity
				h.lines = orderLinesJSON(bought, canceled)
				h.committed = map[string]int64{testLineItemID: committed}
				require.NoError(rt, h.flow.HandleLineCanceled(t.Context(), canceledEvent(bought, before, quantity)))
			case "open":
				// A parcel takes no more than is still owed (ADR 0135).
				quantity := min(a.quantity, bought-canceled-committed)
				if parcelOpen || quantity <= 0 {
					continue
				}
				committed, parcelOpen = quantity, true
			case "cancel":
				if !parcelOpen {
					continue
				}
				h.held = map[string]int64{testLineItemID: committed}
				committed, parcelOpen = 0, false
				h.committed = map[string]int64{}
				h.lines = orderLinesJSON(bought, canceled)
				require.NoError(rt, h.flow.HandleFulfillmentCanceled(t.Context(), parcelEvent(testFulfillmentID)))
			case "fill":
				if !claim.filled && claim.withdrawn < claim.quantity {
					deducted += claim.quantity - claim.withdrawn
					claim.filled, claim.filledAt = true, filledShelf
				}
			}

			require.LessOrEqual(rt, h.inventory.returnedPerItem[testItemID], deducted,
				"the shelf never gains more than the line's stock lost")
		}

		back := h.inventory.returnedPerItem[testItemID]
		require.Equal(rt, max(bought-canceled, committed), deducted-back,
			"what left and stayed gone is what is still bought or in a live parcel")
		if backordered {
			require.Zero(rt, h.inventory.returnedAt[testLocationID],
				"nothing goes to the shelf another line of the order sold from")
		}
	})
}

// TestAWrittenOffLineUnderAParcelKeepsItsClaim: five owed, all five in a live
// parcel, two written off — nothing will come back, so the claim is not
// settled and keeps waiting for five.
func TestAWrittenOffLineUnderAParcelKeepsItsClaim(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	claim := &fakeClaim{itemID: testItemID, quantity: 5}
	h.inventory.claims = map[string]*fakeClaim{testLineItemID: claim}
	h.committed = map[string]int64{testLineItemID: 5}
	h.lines = orderLinesJSON(5, 2)

	require.NoError(t, h.handle(t, canceledEvent(5, 0, 2)))

	assert.Empty(t, h.inventory.settles, "units a live parcel holds leave when they arrive")
	assert.Zero(t, claim.withdrawn)
	assert.Empty(t, h.inventory.calls)
}

// TestAWriteOffUnderALiveParcelWithdrawsOnlyWhatWillNotLeave: five owed, three
// in a live parcel, four written off — two will not leave, so two are
// withdrawn and the parcel's three are still owed.
func TestAWriteOffUnderALiveParcelWithdrawsOnlyWhatWillNotLeave(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	claim := &fakeClaim{itemID: testItemID, quantity: 5}
	h.inventory.claims = map[string]*fakeClaim{testLineItemID: claim}
	h.committed = map[string]int64{testLineItemID: 3}
	h.lines = orderLinesJSON(5, 4)

	require.NoError(t, h.handle(t, canceledEvent(5, 0, 4)))

	assert.Equal(t, []int64{2}, h.inventory.settles)
	assert.Equal(t, int64(2), claim.withdrawn)
}

// TestACanceledParcelBesideALiveOneWithdrawsOnlyWhatWillNotLeave: four of five
// written off, a parcel canceled while another still holds two — three will not
// leave, and the claim gives up three, not the four written off.
func TestACanceledParcelBesideALiveOneWithdrawsOnlyWhatWillNotLeave(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	claim := &fakeClaim{itemID: testItemID, quantity: 5}
	h.inventory.claims = map[string]*fakeClaim{testLineItemID: claim}
	h.held = map[string]int64{testLineItemID: 1}
	h.committed = map[string]int64{testLineItemID: 2}
	h.lines = orderLinesJSON(5, 4)

	require.NoError(t, h.handleParcel(t, parcelEvent(testFulfillmentID)))

	assert.Equal(t, []int64{3}, h.inventory.settles)
	assert.Equal(t, int64(3), claim.withdrawn)
}

// TestAFilledClaimPutsBackOnlyWhatLeft: five bought, two withdrawn before the
// fill, three filled; with three written off in all, one of them had left.
func TestAFilledClaimPutsBackOnlyWhatLeft(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.inventory.claims = map[string]*fakeClaim{testLineItemID: {
		itemID: testItemID, quantity: 5, withdrawn: 2, filled: true, filledAt: filledShelf,
	}}
	h.lines = orderLinesJSON(5, 3)

	require.NoError(t, h.handle(t, canceledEvent(5, 2, 1)))

	assert.Equal(t, []int64{3}, h.inventory.settles, "the window is the line's three units that will not leave")
	assert.Equal(t, int64(1), h.inventory.returned)
}

// TestAFilledClaimGoesBackWhereItWasFilled: the order's other line sold the item
// at one warehouse and the claim was filled at another; the written-off units go
// back to the second.
func TestAFilledClaimGoesBackWhereItWasFilled(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.inventory.claims = map[string]*fakeClaim{testLineItemID: {
		itemID: testItemID, quantity: 5, filled: true, filledAt: filledShelf,
	}}
	h.lines = orderLinesJSON(5, 2)

	require.NoError(t, h.handle(t, canceledEvent(5, 0, 2)))

	assert.Equal(t, int64(2), h.inventory.returnedAt[filledShelf])
	assert.Zero(t, h.inventory.returnedAt[testLocationID], "not the shelf the order's sale left from")
}

// TestAWaitingClaimPutsNothingBack is D242: a written-off line whose claim has
// not filled lost no unit, so the shelf the order's other line sold from gains
// none.
func TestAWaitingClaimPutsNothingBack(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	claim := &fakeClaim{itemID: testItemID, quantity: 5}
	h.inventory.claims = map[string]*fakeClaim{testLineItemID: claim}
	h.lines = orderLinesJSON(5, 5)

	require.NoError(t, h.handle(t, canceledEvent(5, 0, 5)))

	assert.Equal(t, int64(5), claim.withdrawn, "the order will take none of the five")
	assert.Empty(t, h.inventory.calls, "no unit of the line ever left")
}

// TestASettlementThatFailsPutsNothingBack: without the module's answer the flow
// cannot know what the line never lost, so it writes nothing and the bus tries
// again.
func TestASettlementThatFailsPutsNothingBack(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.inventory.settleErr = errors.New("the database is unreachable")

	err := h.handle(t, canceledEvent(5, 0, 2))

	require.Error(t, err)
	assert.Empty(t, h.inventory.calls)
}

// TestTheShelfIsReadBeforeTheClaim: the order's other line sold the item, and
// the checkout's last step finishes between the act's two reads — the claim,
// its own settlement of the write-off, then the confirm of that other line. A
// sale seen means the claim is there, so the act reads the shelf first and the
// line, whose stock lost nothing, credits nothing.
func TestTheShelfIsReadBeforeTheClaim(t *testing.T) {
	t.Parallel()

	for name, act := range map[string]func(h *harness) error{
		"a write-off": func(h *harness) error { return h.handle(t, canceledEvent(5, 0, 5)) },
		"a canceled parcel": func(h *harness) error {
			h.held = map[string]int64{testLineItemID: 5}
			return h.handleParcel(t, parcelEvent(testFulfillmentID))
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.lines = orderLinesJSON(5, 5)
			h.inventory.locations = map[string]string{}
			h.inventory.afterFirstRead = func(f *fakeInventory) {
				f.claims = map[string]*fakeClaim{testLineItemID: {itemID: testItemID, quantity: 5, withdrawn: 5}}
				f.locations = map[string]string{testItemID: testLocationID}
			}

			require.NoError(t, act(h))

			assert.Empty(t, h.inventory.calls, "no unit of the line ever left")
		})
	}
}

// TestAFillThatFailsAfterTheWithdrawalStillPutsBack: the module settled the line
// and then failed to fill a claim of it — a bundle's other part, say. The answer
// stands, so the part whose claim was filled goes back, and the error reaches the
// bus so the fill is tried again.
func TestAFillThatFailsAfterTheWithdrawalStillPutsBack(t *testing.T) {
	t.Parallel()

	for name, act := range map[string]func(h *harness) error{
		"a write-off": func(h *harness) error { return h.handle(t, canceledEvent(5, 0, 2)) },
		"a canceled parcel": func(h *harness) error {
			h.held = map[string]int64{testLineItemID: 2}
			return h.handleParcel(t, parcelEvent(testFulfillmentID))
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.lines = orderLinesJSON(5, 2)
			h.inventory.claims = map[string]*fakeClaim{testLineItemID: {
				itemID: testItemID, quantity: 5, filled: true, filledAt: filledShelf,
			}}
			h.inventory.drainErr = errors.New("the fill of the line's other part failed")

			err := act(h)

			require.Error(t, err, "the bus tries the fill again")
			assert.Contains(t, err.Error(), "the fill of the line's other part failed")
			assert.Equal(t, int64(2), h.inventory.returnedAt[filledShelf], "the answer stood")
		})
	}
}
