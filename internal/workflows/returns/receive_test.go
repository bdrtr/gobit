package returns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// Test fixture identifiers.
const (
	testReturnID   = "ret_1"
	testOrderID    = "order_1"
	testLocationID = "sloc_main"
	testVariantA   = "var_a"
	testVariantB   = "var_b"
	testItemA      = "invitem_a"
	testItemB      = "invitem_b"

	testReplacementID = "orepl_1"
	testFulfillmentID = "ful_1"
	testOptionID      = "so_1"
)

// stubOrders is the scriptable order surface.
type stubOrders struct {
	detail    returnDetail
	detailErr error
	receiveFn func(ctx context.Context, returnID, locationID string) error

	receivedReturn   string
	receivedLocation string
	receiveCalls     int

	claim         claimDetail
	claimErr      error
	completeErr   error
	completeCalls int
	// completedKind and completedID say WHICH record the flow closed. The
	// counter alone cannot: a flow settling the wrong kind would still settle
	// once.
	completedKind   string
	completedID     string
	summaryErr      error
	summaryCalls    int
	summaryOrder    string
	summaryPaid     int64
	summaryRefunded int64

	// The replacement half of the surface.
	replacement      replacementDetail
	replacementErr   error
	reservationErr   error
	reservationCalls []reservationCall
	dispatchErr      error
	dispatchCalls    []dispatchCall
}

// reservationCall is one RecordReplacementReservation call.
type reservationCall struct {
	replacementID string
	itemID        string
	reservationID string
}

// dispatchCall is one MarkReplacementDispatched call.
type dispatchCall struct {
	replacementID string
	fulfillmentID string
}

// ReplacementDetailJSON returns the scripted replacement.
func (s *stubOrders) ReplacementDetailJSON(_ context.Context, _ string) (json.RawMessage, error) {
	if s.replacementErr != nil {
		return nil, s.replacementErr
	}

	return json.Marshal(s.replacement)
}

// RecordReplacementReservation records the promise and applies the scripted
// behavior.
func (s *stubOrders) RecordReplacementReservation(
	_ context.Context, replacementID, itemID, reservationID string,
) error {
	s.reservationCalls = append(s.reservationCalls, reservationCall{
		replacementID: replacementID, itemID: itemID, reservationID: reservationID,
	})

	return s.reservationErr
}

// MarkReplacementDispatched records the stamp and applies the scripted
// behavior.
func (s *stubOrders) MarkReplacementDispatched(
	_ context.Context, replacementID, fulfillmentID string,
) error {
	s.dispatchCalls = append(s.dispatchCalls, dispatchCall{
		replacementID: replacementID, fulfillmentID: fulfillmentID,
	})

	return s.dispatchErr
}

// ReturnDetailJSON returns the scripted detail.
func (s *stubOrders) ReturnDetailJSON(_ context.Context, _ string) (json.RawMessage, error) {
	if s.detailErr != nil {
		return nil, s.detailErr
	}

	return json.Marshal(s.detail)
}

// SetOrderSummaryTotals records what the order was told.
func (s *stubOrders) SetOrderSummaryTotals(
	_ context.Context, orderID string, paidTotal, refundedTotal int64,
) error {
	s.summaryCalls++
	s.summaryOrder, s.summaryPaid, s.summaryRefunded = orderID, paidTotal, refundedTotal

	return s.summaryErr
}

// ClaimDetailJSON returns the scripted claim.
func (s *stubOrders) ClaimDetailJSON(_ context.Context, _ string) (json.RawMessage, error) {
	if s.claimErr != nil {
		return nil, s.claimErr
	}

	return json.Marshal(s.claim)
}

// CompleteClaim records the stamp and applies the scripted behavior.
func (s *stubOrders) CompleteClaim(_ context.Context, id string) error {
	s.completeCalls++
	s.completedKind, s.completedID = "claim", id

	return s.completeErr
}

// CompleteExchange records the stamp and applies the scripted behavior.
//
// It shares the counter with the claim's settlement on purpose: the tests that
// count settlements are counting how many times the SOURCE was closed, and which
// verb closed it is asserted separately by [stubOrders.completedKind]. Two
// counters would let a test that expected one settlement pass while the flow made
// the other.
func (s *stubOrders) CompleteExchange(_ context.Context, id string) error {
	s.completeCalls++
	s.completedKind, s.completedID = "exchange", id

	return s.completeErr
}

// ReceiveReturn records the stamp and applies the scripted behavior.
func (s *stubOrders) ReceiveReturn(ctx context.Context, returnID, locationID string) error {
	s.receiveCalls++
	s.receivedReturn, s.receivedLocation = returnID, locationID
	if s.receiveFn == nil {
		return nil
	}

	return s.receiveFn(ctx, returnID, locationID)
}

// restockCall is one Restock call.
type restockCall struct {
	itemID     string
	locationID string
	quantity   int64
}

// stubInventory is the scriptable inventory surface.
type stubInventory struct {
	err   error
	calls []restockCall

	// The going-out half of the surface.
	reserveID      string
	reserveErr     error
	reserveCalls   []reserveCall
	confirmErr     error
	confirmed      []string
	releaseErr     error
	released       []string
	reserveCounter int
}

// reserveCall is one ReserveForReplacement call.
type reserveCall struct {
	itemID     string
	locationID string
	quantity   int64
	lineItemID string
}

// Restock records the call and applies the scripted behavior.
func (s *stubInventory) Restock(_ context.Context, itemID, locationID string, quantity int64) error {
	s.calls = append(s.calls, restockCall{itemID: itemID, locationID: locationID, quantity: quantity})

	return s.err
}

// ReserveForReplacement records the call and answers with a promise id.
//
// The id is a COUNTER rather than a constant: two lines set aside under the
// same promise would make a flow that confirms one of them twice look correct.
func (s *stubInventory) ReserveForReplacement(
	_ context.Context, itemID, locationID string, quantity int64, lineItemID string,
) (string, error) {
	s.reserveCalls = append(s.reserveCalls, reserveCall{
		itemID: itemID, locationID: locationID, quantity: quantity, lineItemID: lineItemID,
	})
	if s.reserveErr != nil {
		return "", s.reserveErr
	}
	s.reserveCounter++
	if s.reserveID != "" {
		return s.reserveID, nil
	}

	return fmt.Sprintf("invres_%d", s.reserveCounter), nil
}

// ConfirmReservation records the confirm and applies the scripted behavior.
func (s *stubInventory) ConfirmReservation(_ context.Context, reservationID string) error {
	s.confirmed = append(s.confirmed, reservationID)

	return s.confirmErr
}

// ReleaseReservation records the release and applies the scripted behavior.
func (s *stubInventory) ReleaseReservation(_ context.Context, reservationID string) error {
	s.released = append(s.released, reservationID)

	return s.releaseErr
}

// stubShipping is the scriptable surface of the fulfilling flow.
type stubShipping struct {
	fulfillmentID string
	alreadyOpen   bool
	err           error
	calls         []shippingCall
}

// shippingCall is one OpenForOrder call.
type shippingCall struct {
	orderID string
	request string
}

// OpenForOrder records the request and answers with the scripted parcel.
func (s *stubShipping) OpenForOrder(
	_ context.Context, orderID string, request json.RawMessage,
) (fulfillmentID string, alreadyOpen bool, err error) {
	s.calls = append(s.calls, shippingCall{orderID: orderID, request: string(request)})
	if s.err != nil {
		return "", false, s.err
	}

	return s.fulfillmentID, s.alreadyOpen, nil
}

// stubLinks is the scriptable link surface.
type stubLinks struct {
	links map[string][]string
	err   error
	calls int
}

// ListMany returns the scripted links.
func (s *stubLinks) ListMany(
	_ context.Context, _ string, fromIDs []string,
) (map[string][]string, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}

	out := make(map[string][]string, len(fromIDs))
	for _, id := range fromIDs {
		if linked, ok := s.links[id]; ok {
			out[id] = linked
		}
	}

	return out, nil
}

// refundCall is one RefundCollection call.
type refundCall struct {
	collectionID string
	amount       int64
	reason       string
}

// stubPayments is the scriptable payment surface.
type stubPayments struct {
	refunded    int64
	refundErr   error
	captured    int64
	totalRefund int64
	readErr     error

	refundCalls []refundCall
}

// RefundCollection records the call and returns the scripted outcome.
func (s *stubPayments) RefundCollection(
	_ context.Context, collectionID string, amount int64, reason string,
) (int64, error) {
	s.refundCalls = append(s.refundCalls,
		refundCall{collectionID: collectionID, amount: amount, reason: reason})

	return s.refunded, s.refundErr
}

// Collection returns the scripted amounts.
//
// The six results mirror the payment module's own surface, whose signature is
// long for ADR 0006's reason: a consumer that cannot import that package cannot
// name a shared struct, so the amounts cross as separate primitives.
//
//nolint:gocritic // The shape is the cross-module contract's, not a choice made here.
func (s *stubPayments) Collection(_ context.Context, _ string) (string, int64, int64, int64, int64, error) {
	if s.readErr != nil {
		return "", 0, 0, 0, 0, s.readErr
	}

	return "captured", s.captured, 0, s.captured, s.totalRefund, nil
}

// harness wires the flow over scriptable surfaces.
type harness struct {
	orders    *stubOrders
	inventory *stubInventory
	payments  *stubPayments
	links     *stubLinks
	shipping  *stubShipping
	wf        *Workflows
}

// newHarness builds a return of two lines whose variants both have items.
func newHarness(t *testing.T) *harness {
	t.Helper()

	h := &harness{
		orders: &stubOrders{detail: returnDetail{
			ReturnID: testReturnID,
			OrderID:  testOrderID,
			Status:   "requested",
			Lines: []returnLine{
				{OrderLineItemID: "oli_a", VariantID: testVariantA, Quantity: 2},
				{OrderLineItemID: "oli_b", VariantID: testVariantB, Quantity: 1},
			},
		}},
		inventory: &stubInventory{},
		payments:  &stubPayments{},
		links: &stubLinks{links: map[string][]string{
			testVariantA: {testItemA},
			testVariantB: {testItemB},
		}},
		shipping: &stubShipping{fulfillmentID: testFulfillmentID},
	}

	wf, err := New(Deps{
		Orders:    h.orders,
		Inventory: h.inventory,
		Payments:  h.payments,
		Links:     h.links,
		Shipping:  h.shipping,
	})
	require.NoError(t, err)
	h.wf = wf

	return h
}

// TestReceivingPutsTheStockBack is what the after-sales records could not do.
//
// They could be created, read and listed, and nothing ever acted on them: the
// module said so in writing and deferred acting to phases that never came.
func TestReceivingPutsTheStockBack(t *testing.T) {
	h := newHarness(t)

	out, err := h.wf.ReceiveReturn(context.Background(), testReturnID, testLocationID)
	require.NoError(t, err)

	assert.Empty(t, out.Warnings)
	assert.Equal(t, 2, out.RestockedLines)
	assert.Equal(t, int64(3), out.RestockedUnits)

	require.Len(t, h.inventory.calls, 2)
	assert.Equal(t, restockCall{itemID: testItemA, locationID: testLocationID, quantity: 2},
		h.inventory.calls[0])
	assert.Equal(t, restockCall{itemID: testItemB, locationID: testLocationID, quantity: 1},
		h.inventory.calls[1])
}

// TestTheStockGoesWHERETheGoodsARRIVED holds the destination rule.
//
// The location is the caller's because nothing else knows it: the order carries
// none, and the warehouse that shipped is not necessarily the one the customer
// returned to.
func TestTheStockGoesWHERETheGoodsARRIVED(t *testing.T) {
	h := newHarness(t)

	_, err := h.wf.ReceiveReturn(context.Background(), testReturnID, "sloc_other")
	require.NoError(t, err)

	assert.Equal(t, "sloc_other", h.orders.receivedLocation)
	for _, call := range h.inventory.calls {
		assert.Equal(t, "sloc_other", call.locationID)
	}
}

// TestTheRecordIsStampedBEFORETheStockMoves pins the order of the two halves.
//
// Neither can undo the other, so what matters is which failure a person can
// finish by hand. A stamped return whose stock was not restored names exactly
// what is missing and where it should have gone. Stock added with nothing
// saying why leaves a count nobody can explain.
func TestTheRecordIsStampedBEFORETheStockMoves(t *testing.T) {
	h := newHarness(t)
	h.orders.receiveFn = func(_ context.Context, _, _ string) error {
		assert.Empty(t, h.inventory.calls, "no stock may move before the record is written")

		return nil
	}

	_, err := h.wf.ReceiveReturn(context.Background(), testReturnID, testLocationID)
	require.NoError(t, err)
	assert.Equal(t, 1, h.orders.receiveCalls)
}

// TestAnUnstampedReturnDoesNotMoveStock is the other half of that order.
//
// If the record cannot be written the goods stay unaccounted for, and adding
// stock anyway would put the warehouse ahead of the paperwork.
func TestAnUnstampedReturnDoesNotMoveStock(t *testing.T) {
	h := newHarness(t)
	h.orders.receiveFn = func(_ context.Context, _, _ string) error {
		return errors.New("the order module is unreachable")
	}

	_, err := h.wf.ReceiveReturn(context.Background(), testReturnID, testLocationID)

	require.Error(t, err)
	assert.Empty(t, h.inventory.calls)
}

// TestAReturnCannotBeReceivedTwice is what makes a NON-idempotent restock safe.
//
// The order module would treat a second receive as a no-op; the warehouse would
// not. Two calls would add goods that arrived once.
func TestAReturnCannotBeReceivedTwice(t *testing.T) {
	h := newHarness(t)
	h.orders.detail.Status = statusReceived

	_, err := h.wf.ReceiveReturn(context.Background(), testReturnID, testLocationID)

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))
	assert.Empty(t, h.inventory.calls, "nothing may be added for goods that arrived once")
	assert.Equal(t, 0, h.orders.receiveCalls)
}

// TestAFailedRestockIsAWarningNotAFailure keeps the receipt from being denied
// while the operator is holding the parcel.
//
// Returning an error would say the receipt did not happen — and the record the
// operator has to work from would not exist.
func TestAFailedRestockIsAWarningNotAFailure(t *testing.T) {
	h := newHarness(t)
	h.inventory.err = errors.New("the inventory module is unreachable")

	out, err := h.wf.ReceiveReturn(context.Background(), testReturnID, testLocationID)

	require.NoError(t, err)
	assert.Equal(t, 1, h.orders.receiveCalls, "the goods still arrived and the record says so")
	assert.Zero(t, out.RestockedLines)
	assert.Len(t, out.Warnings, 2, "every line that did not go back needs a human")
}

// TestOneUnrestockableLineDoesNotStopTheOthers keeps a single fault from
// becoming several.
//
// The lines are separate products in separate bins; refusing to put the second
// one back because the first has no inventory item would multiply the damage.
func TestOneUnrestockableLineDoesNotStopTheOthers(t *testing.T) {
	h := newHarness(t)
	h.links.links = map[string][]string{testVariantB: {testItemB}}

	out, err := h.wf.ReceiveReturn(context.Background(), testReturnID, testLocationID)
	require.NoError(t, err)

	assert.Equal(t, 1, out.RestockedLines)
	assert.Equal(t, int64(1), out.RestockedUnits)
	require.Len(t, out.Warnings, 1)
	assert.Contains(t, out.Warnings[0], testVariantA)

	require.Len(t, h.inventory.calls, 1)
	assert.Equal(t, testItemB, h.inventory.calls[0].itemID)
}

// TestTheInventoryItemsAreResolvedInONEQuery keeps an N+1 out of a path that
// walks every line of a return.
func TestTheInventoryItemsAreResolvedInONEQuery(t *testing.T) {
	h := newHarness(t)

	_, err := h.wf.ReceiveReturn(context.Background(), testReturnID, testLocationID)
	require.NoError(t, err)

	assert.Equal(t, 1, h.links.calls)
}

// TestALocationIsRequired refuses a receipt that could not be restocked.
func TestALocationIsRequired(t *testing.T) {
	h := newHarness(t)

	_, err := h.wf.ReceiveReturn(context.Background(), testReturnID, "")

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err))
	assert.Equal(t, 0, h.orders.receiveCalls)
}

// TestAMissingSurfaceIsRefusedAtWiring keeps a half-built flow from being
// discovered on the first return.
func TestAMissingSurfaceIsRefusedAtWiring(t *testing.T) {
	_, err := New(Deps{
		Inventory: &stubInventory{},
		Payments:  &stubPayments{},
		Links:     &stubLinks{},
		Shipping:  &stubShipping{},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), ServiceOrder)
}
