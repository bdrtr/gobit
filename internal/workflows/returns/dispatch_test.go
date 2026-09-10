package returns

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// dispatchHarness builds a replacement of two lines, both waiting to be sent.
//
// The quantities differ so a flow that sent the same line twice, or counted one
// line twice, cannot produce the right total by accident.
func dispatchHarness(t *testing.T) *harness {
	t.Helper()

	h := newHarness(t)
	h.orders.replacement = replacementDetail{
		ReplacementID:    testReplacementID,
		SourceKind:       "claim",
		SourceID:         testClaimID,
		SourceStatus:     statusRequested,
		SourceOpen:       true,
		SourceSettleable: true,
		OrderID:          testOrderID,
		Status:           statusReplacementRequested,
		ShippingOptionID: testOptionID,
		LocationID:       testLocationID,
		Lines: []replacementLine{
			{
				ReplacementItemID: "oreplitem_a", OrderLineItemID: "oli_a",
				VariantID: testVariantA, Quantity: 2,
			},
			{
				ReplacementItemID: "oreplitem_b", OrderLineItemID: "oli_b",
				VariantID: testVariantB, Quantity: 1,
			},
		},
	}

	return h
}

// TestDispatchingSendsWhatWasPromised is the capability the record was waiting
// for.
//
// Until this flow existed a claim could say what to send and nothing could send
// it: the settle verb refused a claim of that kind, and the refusal was the
// only thing in the framework that mentioned it.
func TestDispatchingSendsWhatWasPromised(t *testing.T) {
	h := dispatchHarness(t)

	result, err := h.wf.DispatchReplacement(context.Background(), testReplacementID)
	require.NoError(t, err)

	assert.Equal(t, testFulfillmentID, result.FulfillmentID)
	assert.Equal(t, int64(3), result.SentUnits, "two units of one line and one of the other")
	assert.False(t, result.AlreadySent)

	require.Len(t, h.inventory.reserveCalls, 2)
	assert.Equal(t, testItemA, h.inventory.reserveCalls[0].itemID)
	assert.Equal(t, testLocationID, h.inventory.reserveCalls[0].locationID,
		"the units are set aside where the record says they are sent FROM")
	assert.Equal(t, int64(2), h.inventory.reserveCalls[0].quantity)
	assert.Equal(t, "oli_a", h.inventory.reserveCalls[0].lineItemID)
	assert.Equal(t, testItemB, h.inventory.reserveCalls[1].itemID)

	require.Len(t, h.orders.reservationCalls, 2, "every promise has to be written on its line")
	assert.Equal(t, "oreplitem_a", h.orders.reservationCalls[0].itemID)
	assert.Equal(t, "invres_1", h.orders.reservationCalls[0].reservationID)
	assert.Equal(t, "oreplitem_b", h.orders.reservationCalls[1].itemID)
	assert.Equal(t, "invres_2", h.orders.reservationCalls[1].reservationID)

	require.Len(t, h.shipping.calls, 1)
	assert.Equal(t, testOrderID, h.shipping.calls[0].orderID)
	assert.Contains(t, h.shipping.calls[0].request, testOptionID)
	assert.Contains(t, h.shipping.calls[0].request, "replacement-"+testReplacementID,
		"the key is DERIVED from the record, so a retry names the same parcel")

	assert.Equal(t, []string{"invres_1", "invres_2"}, h.inventory.confirmed,
		"the units leave the count only after the parcel exists")

	require.Len(t, h.orders.dispatchCalls, 1)
	assert.Equal(t, testReplacementID, h.orders.dispatchCalls[0].replacementID)
	assert.Equal(t, testFulfillmentID, h.orders.dispatchCalls[0].fulfillmentID)
	assert.Equal(t, 1, h.orders.completeCalls, "sending the goods settles the claim")
}

// TestALineAlreadyHeldIsNotHeldAgain is what makes the dispatch retryable.
//
// The promise is written on the line by the attempt that made it, so the next
// attempt reads it back instead of setting the same units aside a second time.
func TestALineAlreadyHeldIsNotHeldAgain(t *testing.T) {
	h := dispatchHarness(t)
	h.orders.replacement.Lines[0].ReservationID = "invres_earlier"

	result, err := h.wf.DispatchReplacement(context.Background(), testReplacementID)
	require.NoError(t, err)

	require.Len(t, h.inventory.reserveCalls, 1, "only the line with no promise is set aside")
	assert.Equal(t, testItemB, h.inventory.reserveCalls[0].itemID)
	assert.Equal(t, []string{"invres_earlier", "invres_1"}, h.inventory.confirmed,
		"the promise the earlier attempt made is the one confirmed")
	assert.Equal(t, int64(3), result.SentUnits)
}

// TestTheUnitsAreNotTakenBeforeTheParcelExists holds the order of the two
// irreversible halves.
//
// A confirmed reservation cannot be released. Confirming before the parcel is
// open would risk units gone with nothing carrying them; this way the failure
// leaves units HELD for goods that never shipped, which a person can release.
func TestTheUnitsAreNotTakenBeforeTheParcelExists(t *testing.T) {
	h := dispatchHarness(t)
	h.shipping.err = errors.New("the carrier module is down")

	_, err := h.wf.DispatchReplacement(context.Background(), testReplacementID)

	require.Error(t, err)
	assert.Equal(t, CodeParcelNotOpened, coreerrors.CodeOf(err))
	assert.Empty(t, h.inventory.confirmed, "nothing may leave the count without a parcel")
	assert.Empty(t, h.orders.dispatchCalls)
	assert.Equal(t, 0, h.orders.completeCalls)
	assert.Empty(t, h.inventory.released,
		"the promises STAY: the next attempt reuses them instead of setting the units aside twice")
}

// TestAPromiseThatCannotBeRecordedIsReleased closes the one gap the retry
// cannot: stock held under a promise no record names.
func TestAPromiseThatCannotBeRecordedIsReleased(t *testing.T) {
	h := dispatchHarness(t)
	h.orders.reservationErr = errors.New("the record could not be written")

	_, err := h.wf.DispatchReplacement(context.Background(), testReplacementID)

	require.Error(t, err)
	assert.Equal(t, CodeStockNotHeld, coreerrors.CodeOf(err))
	assert.Equal(t, []string{"invres_1"}, h.inventory.released)
	assert.Empty(t, h.shipping.calls, "no parcel is opened for goods that are not held")
	assert.Empty(t, h.inventory.confirmed)
}

// TestUnitsThatCannotBeSetAsideStopTheDispatch is the honest refusal a shop
// needs: there is nothing in the warehouse to send.
func TestUnitsThatCannotBeSetAsideStopTheDispatch(t *testing.T) {
	h := dispatchHarness(t)
	h.inventory.reserveErr = coreerrors.Conflict("inventory_insufficient_stock",
		"not enough stock")

	_, err := h.wf.DispatchReplacement(context.Background(), testReplacementID)

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err),
		"an empty shelf is a conflict rather than a fault of the request")
	assert.Equal(t, CodeStockNotHeld, coreerrors.CodeOf(err))
	assert.Empty(t, h.shipping.calls)
	assert.Empty(t, h.orders.dispatchCalls)
}

// TestAVariantWithNoInventoryItemIsRefused is where dispatching parts from
// receiving.
//
// Receiving warns and carries on, because the goods are already in the
// building. Sending has the opposite shape: goods no warehouse can be asked for
// cannot leave it, and a parcel claiming they did would be the record that is
// wrong.
func TestAVariantWithNoInventoryItemIsRefused(t *testing.T) {
	h := dispatchHarness(t)
	h.links.links = map[string][]string{testVariantA: {testItemA}}

	_, err := h.wf.DispatchReplacement(context.Background(), testReplacementID)

	require.Error(t, err)
	assert.Equal(t, CodeNoInventoryItem, coreerrors.CodeOf(err))
	assert.Empty(t, h.shipping.calls)
	assert.Empty(t, h.orders.dispatchCalls)
	assert.Equal(t, 0, h.orders.completeCalls)
}

// TestASentReplacementSendsNothingAgain answers the second press of the button.
func TestASentReplacementSendsNothingAgain(t *testing.T) {
	h := dispatchHarness(t)
	h.orders.replacement.Status = statusReplacementDispatched
	h.orders.replacement.FulfillmentID = "ful_earlier"
	h.orders.replacement.SourceStatus = "completed"
	h.orders.replacement.SourceOpen = false

	result, err := h.wf.DispatchReplacement(context.Background(), testReplacementID)
	require.NoError(t, err)

	assert.True(t, result.AlreadySent)
	assert.Equal(t, "ful_earlier", result.FulfillmentID,
		"the answer names the parcel the goods really left in")
	assert.Zero(t, result.SentUnits, "nothing moved this time")
	assert.Empty(t, h.inventory.reserveCalls)
	assert.Empty(t, h.inventory.confirmed)
	assert.Empty(t, h.shipping.calls)
	assert.Equal(t, 0, h.orders.completeCalls, "a settled claim is not settled again")
}

// TestASentReplacementStillSettlesAnOpenClaim finishes the work of an attempt
// that died between the two.
//
// The dispatch is recorded and the claim is not: that is the state a crash
// after MarkReplacementDispatched leaves, and running the flow again is how it
// is meant to be finished. Skipping the settle because the goods have gone
// would leave the claim open forever.
func TestASentReplacementStillSettlesAnOpenClaim(t *testing.T) {
	h := dispatchHarness(t)
	h.orders.replacement.Status = statusReplacementDispatched
	h.orders.replacement.FulfillmentID = testFulfillmentID

	result, err := h.wf.DispatchReplacement(context.Background(), testReplacementID)
	require.NoError(t, err)

	assert.True(t, result.AlreadySent)
	assert.Equal(t, 1, h.orders.completeCalls, "the claim the goods answered is closed")
	assert.Empty(t, h.inventory.confirmed, "and nothing leaves the count a second time")
}

// TestAnExchangeSourcedDispatchSettlesTheExchange is the same flow one record
// over: the verb follows the source the replacement names.
func TestAnExchangeSourcedDispatchSettlesTheExchange(t *testing.T) {
	h := dispatchHarness(t)
	exchangeSource(h, 1000)

	_, err := h.wf.DispatchReplacement(context.Background(), testReplacementID)
	require.NoError(t, err)

	assert.Equal(t, 1, h.orders.completeCalls, "the source is closed once")
	assert.Equal(t, "exchange", h.orders.completedKind,
		"the EXCHANGE is settled, not a claim; the counter alone could not tell")
	assert.Equal(t, "exch_1", h.orders.completedID)
}

// exchangeSource turns the harness's replacement into one an EXCHANGE sourced,
// with its difference collected and still held.
//
// The last part is what the fixture has to answer now: the dispatch asks the
// payment module whether the money it was funded with is still there, twice —
// before the goods move and again before the record is closed (ADR 0124). A
// fixture that only set a status would send the flow into a payment read with
// nothing to read, which is what the compiler could not have told anyone.
func exchangeSource(h *harness, difference int64) {
	h.orders.replacement.SourceKind = "exchange"
	h.orders.replacement.SourceID = "exch_1"
	h.orders.funding = fundingOf(difference, "TRY", "paycol_1")
	h.payments.amount = difference
	h.payments.captured = difference
	h.payments.totalRefund = 0
}

// TestAnExchangeThatOwesMoneyIsLeftOpen keeps a dispatch that really sent the
// goods from failing over a state that is correct.
//
// The order module answers source_settleable, and this flow does not
// second-guess it: asking anyway would earn a conflict AFTER the parcel left.
func TestAnExchangeThatOwesMoneyIsLeftOpen(t *testing.T) {
	h := dispatchHarness(t)
	exchangeSource(h, 1000)
	h.orders.replacement.SourceSettleable = false

	result, err := h.wf.DispatchReplacement(context.Background(), testReplacementID)
	require.NoError(t, err, "the goods left and that is not an error")

	assert.NotEmpty(t, result.FulfillmentID, "the parcel was opened")
	assert.Equal(t, 0, h.orders.completeCalls,
		"the exchange stays open; the money it owes has not been collected")
}

// TestAFundedExchangeIsSettledByTheGoods proves the flow decides on the order
// module's ANSWER and not on a status it recognizes.
//
// This is gap D59 in one assertion. The guard used to read
// `source_status != "requested"`, which is a copy of a vocabulary this flow does
// not own; ADR 0120 added "funded" to it and the copy went on excluding it, so
// an exchange whose difference an operator had really collected stayed open with
// its goods already gone. Nothing failed and nothing was logged — being quiet is
// the guard's whole job — and every unit test here was green, because the
// fixture only ever produced the one status the copy knew.
func TestAFundedExchangeIsSettledByTheGoods(t *testing.T) {
	h := dispatchHarness(t)
	exchangeSource(h, 1000)
	// A status this flow has never heard of, and it must not need to: what it
	// decides on is the pair of booleans the order module answered.
	h.orders.replacement.SourceStatus = "funded"
	h.orders.replacement.SourceOpen = true
	h.orders.replacement.SourceSettleable = true

	result, err := h.wf.DispatchReplacement(context.Background(), testReplacementID)
	require.NoError(t, err)

	assert.NotEmpty(t, result.FulfillmentID, "the parcel was opened")
	assert.Equal(t, 1, h.orders.completeCalls,
		"the goods close a FUNDED exchange; leaving it open is the defect D59 records")
	assert.Equal(t, "exch_1", h.orders.completedID)
}

// TestAWithdrawnReplacementIsNotSent keeps goods from leaving against a promise
// somebody took back.
func TestAWithdrawnReplacementIsNotSent(t *testing.T) {
	h := dispatchHarness(t)
	h.orders.replacement.Status = "canceled"

	_, err := h.wf.DispatchReplacement(context.Background(), testReplacementID)

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))
	assert.Equal(t, CodeReplacementNotOpen, coreerrors.CodeOf(err))
	assert.Empty(t, h.inventory.reserveCalls)
	assert.Empty(t, h.shipping.calls)
}

// TestADispatchNeedsARecord refuses the call that names nothing.
func TestADispatchNeedsARecord(t *testing.T) {
	h := dispatchHarness(t)

	_, err := h.wf.DispatchReplacement(context.Background(), "")

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err))
	assert.Empty(t, h.shipping.calls)
}

// TestAReplacementWithNoLinesIsRefused stops a dispatch of nothing.
//
// The record cannot be created without a line, so this is a record that lost
// them. Opening a parcel for it would put an empty box in the post.
func TestAReplacementWithNoLinesIsRefused(t *testing.T) {
	h := dispatchHarness(t)
	h.orders.replacement.Lines = nil

	_, err := h.wf.DispatchReplacement(context.Background(), testReplacementID)

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))
	assert.Empty(t, h.shipping.calls)
}

// TestAnUnreadableReplacementStopsBeforeAnythingMoves is the mirror of the
// return flow's own read failure.
func TestAnUnreadableReplacementStopsBeforeAnythingMoves(t *testing.T) {
	h := dispatchHarness(t)
	h.orders.replacementErr = errors.New("the order module is down")

	_, err := h.wf.DispatchReplacement(context.Background(), testReplacementID)

	require.Error(t, err)
	assert.Equal(t, CodeReplacementUnreadable, coreerrors.CodeOf(err))
	assert.Empty(t, h.inventory.reserveCalls)
	assert.Empty(t, h.shipping.calls)
}

// TestADrainedExchangeIsNotSettledOnTheRetryPath keeps the RECORD honest after
// the goods are already gone.
//
// The pre-flight refusal cannot reach this: it runs before the parcel, and here
// the parcel left on an earlier attempt that died before settling. Between that
// attempt and this one the difference went back through the payment module's own
// route, so what the exchange holds is nothing.
//
// Refusing is not on the table — the goods are with the customer — and neither
// is completing: a record saying an exchange was settled while its collection
// holds nothing is exactly what ADR 0119 forbids. It stays open and it is
// logged, which leaves a human with both facts and a decision.
func TestADrainedExchangeIsNotSettledOnTheRetryPath(t *testing.T) {
	h := dispatchHarness(t)
	exchangeSource(h, 1000)
	h.orders.replacement.SourceStatus = "funded"
	h.orders.replacement.SourceOpen = true
	h.orders.replacement.SourceSettleable = true
	// The parcel left on an earlier attempt.
	h.orders.replacement.Status = statusReplacementDispatched
	h.orders.replacement.FulfillmentID = "ful_earlier"
	// And the money went back since.
	h.payments.totalRefund = 1000

	result, err := h.wf.DispatchReplacement(context.Background(), testReplacementID)

	require.NoError(t, err,
		"the goods are already with the customer; failing the call would ask the caller "+
			"to retry something that has happened")
	assert.True(t, result.AlreadySent)
	assert.Equal(t, 0, h.orders.completeCalls,
		"the exchange must NOT be marked settled while its collection holds nothing")
	assert.Empty(t, h.shipping.calls, "no second parcel")
}
