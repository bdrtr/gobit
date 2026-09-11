package ordercancel_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/internal/workflows/ordercancel"
)

// The tests here are about the ARITHMETIC and about which failures the bus should
// retry. What the SQL does — the unique reference, the sale movement's location,
// the committed sums — is each module's own integration lane.

const (
	testOrderID        = "order_01CANCELSTOCKORDER000"
	testLineItemID     = "oli_01CANCELSTOCKLINE0000"
	testVariantID      = "variant_01CANCELSTOCK0000"
	testItemID         = "invitem_01CANCELSTOCK000"
	testLocationID     = "sloc_01CANCELSTOCKSHELF00"
	testCancellationID = "olc_01CANCELSTOCKROW0000"
	testFulfillmentID  = "ful_01CANCELSTOCKPARCEL0"
)

// TestUnshippedUnitsGoBackAndShippedOnesDoNot is the rule in one table.
//
// Stock was deducted for every unit bought. Units a live parcel holds have left. So
// what can come back is the window between them, and a second cancellation must not
// put back what the first already did.
func TestUnshippedUnitsGoBackAndShippedOnesDoNot(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                           string
		bought, committed, before, now int64
		want                           int64
	}{
		{name: "nothing shipped, two canceled", bought: 5, committed: 0, before: 0, now: 2, want: 2},
		{name: "everything shipped", bought: 5, committed: 5, before: 0, now: 2, want: 0},
		{
			name:   "three shipped of five, two canceled fit the window",
			bought: 5, committed: 3, before: 0, now: 2, want: 2,
		},
		{
			name:   "three shipped of five, three canceled overrun it",
			bought: 5, committed: 3, before: 0, now: 3, want: 2,
		},
		{
			name:   "a SECOND cancellation inside the window",
			bought: 5, committed: 0, before: 2, now: 2, want: 2,
		},
		{
			name:   "a second cancellation that overruns what the first left",
			bought: 5, committed: 3, before: 2, now: 2, want: 0,
		},
		{
			name:   "more shipped than bought, which a bad parcel would make",
			bought: 2, committed: 3, before: 0, now: 1, want: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.committed[testLineItemID] = tc.committed

			err := h.handle(t, canceledEvent(tc.bought, tc.before, tc.now))
			require.NoError(t, err)

			assert.Equal(t, tc.want, h.inventory.returned,
				"bought %d, in a parcel %d, canceled before %d, canceling %d",
				tc.bought, tc.committed, tc.before, tc.now)
			if tc.want == 0 {
				assert.Empty(t, h.inventory.calls,
					"a return of nothing must not reach the inventory module at all")
			}
		})
	}
}

// TestTheUNITSAreSummedOverEveryCancellationOfTheLine is the window's whole point.
//
// Two cancellations of three and three units on a line of five with nothing shipped
// put back five, not six. The second one is capped by what the first already took,
// and the cap is derived from the event rather than from a record of past restocks.
func TestTheUNITSAreSummedOverEveryCancellationOfTheLine(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	require.NoError(t, h.handle(t, canceledEvent(5, 0, 3)))
	require.NoError(t, h.handle(t, canceledEvent(5, 3, 3)))

	assert.Equal(t, int64(5), h.inventory.returned,
		"the line sold five units, so five is everything there is to give back")
}

// TestASecondDeliveryPutsNothingBackTwice is idempotence, at the seam.
//
// The ledger refuses the duplicate and reports it as "already back"; what this
// checks is that the flow treats that as success rather than as a fault the bus
// should retry forever.
func TestASecondDeliveryPutsNothingBackTwice(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.inventory.alreadyBack = true

	require.NoError(t, h.handle(t, canceledEvent(5, 0, 2)),
		"a redelivered event is not an error; returning one would retry it forever")
}

// TestAVariantThatTracksNoStockIsNotAFailure keeps the bus out of a retry loop
// over an event that can never succeed.
func TestAVariantThatTracksNoStockIsNotAFailure(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.links[linkVariantInventory] = map[string][]string{}

	require.NoError(t, h.handle(t, canceledEvent(5, 0, 2)))
	assert.Empty(t, h.inventory.calls, "a service or a digital good has no shelf")
}

// TestAnOrderWithNoSaleMovementIsNotAFailure is the saga that never got to its
// last step.
//
// Its reservations were released by the compensation, so nothing was deducted and
// there is nothing to put back. It is warned about rather than retried, because no
// number of retries will make a sale movement appear.
func TestAnOrderWithNoSaleMovementIsNotAFailure(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.inventory.locations = map[string]string{}

	require.NoError(t, h.handle(t, canceledEvent(5, 0, 2)))
	assert.Empty(t, h.inventory.calls)
}

// TestAFaultThatMayPASSIsRetried is the other half of the same judgement.
//
// A module being unreachable is a failure the bus should try again, and folding it
// into the silent branch would lose the stock for good.
func TestAFaultThatMayPASSIsRetried(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		break_ func(*harness)
	}{
		{name: "the parcels cannot be read", break_: func(h *harness) { h.linkErr = errors.New("down") }},
		{name: "the shipped units cannot be read", break_: func(h *harness) { h.committedErr = errors.New("down") }},
		{name: "the shelf cannot be read", break_: func(h *harness) { h.inventory.locationErr = errors.New("down") }},
		{name: "the units cannot be put back", break_: func(h *harness) { h.inventory.returnErr = errors.New("down") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tc.break_(h)

			require.Error(t, h.handle(t, canceledEvent(5, 0, 2)),
				"a fault that may pass has to reach the bus, or the stock is lost silently")
		})
	}
}

// TestAnEventThatCannotBeActedOnIsRefused holds the payload contract.
//
// Every count travels as a decimal STRING, which is the bus's own rule: JSON has
// one number type, so an int64 put in arrives as a float64 on the Redis backend and
// as an int64 in memory.
func TestAnEventThatCannotBeActedOnIsRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		data map[string]any
	}{
		{name: "no cancellation id", data: withoutField("cancellation_id")},
		{name: "no order", data: withoutField("order_id")},
		{name: "no line", data: withoutField("order_line_item_id")},
		{name: "no variant", data: withoutField("variant_id")},
		{name: "a count that is not a string", data: withField("canceled_quantity", 2)},
		{name: "a count that is not a number", data: withField("canceled_quantity", "two")},
		{name: "a negative count", data: withField("canceled_before", "-1")},
		{name: "nothing canceled", data: withField("canceled_quantity", "0")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)

			err := h.handle(t, eventbus.Event{
				ID: "e", Name: "order.line_canceled", Data: tc.data,
			})

			require.Error(t, err)
			assert.Contains(t, err.Error(), ordercancel.CodeEventUnusable)
			assert.Empty(t, h.inventory.calls)
		})
	}
}

// linkVariantInventory is the link name the flow uses, repeated here for the
// reason the flow repeats it: neither side imports the other.
const linkVariantInventory = "product_variant_inventory"

// linkOrderFulfillment is the other link name the flow uses, repeated for the
// same reason.
const linkOrderFulfillment = "order_fulfillment"

// canceledEvent builds a payload the way the order module does.
func canceledEvent(bought, before, now int64) eventbus.Event {
	return eventbus.Event{
		ID:   "order.line_canceled:" + testCancellationID,
		Name: "order.line_canceled",
		Data: map[string]any{
			"order_id":           testOrderID,
			"cancellation_id":    testCancellationID,
			"order_line_item_id": testLineItemID,
			"variant_id":         testVariantID,
			"canceled_quantity":  strconv.FormatInt(now, 10),
			"canceled_before":    strconv.FormatInt(before, 10),
			"bought_quantity":    strconv.FormatInt(bought, 10),
			"canceled_at":        "2026-09-11T10:00:00Z",
		},
	}
}

// withoutField is a good payload with one key removed.
func withoutField(key string) map[string]any {
	out := map[string]any{}
	for k, v := range canceledEvent(5, 0, 2).Data {
		if k != key {
			out[k] = v
		}
	}

	return out
}

// withField is a good payload with one key replaced.
func withField(key string, value any) map[string]any {
	out := map[string]any{}
	for k, v := range canceledEvent(5, 0, 2).Data {
		out[k] = v
	}
	out[key] = value

	return out
}

// harness is the flow over fakes.
type harness struct {
	flow         *ordercancel.Workflow
	inventory    *fakeInventory
	committed    map[string]int64
	committedErr error
	links        map[string]map[string][]string
	linkErr      error
	// held is what ONE parcel carries, per line, and it answers for a canceled
	// parcel too — the seam the parcel handler reads.
	held    map[string]int64
	heldErr error
	// lines is the order module's answer, verbatim JSON, so the test exercises
	// the same decoding production does.
	lines    string
	linesErr error
}

// newHarness wires a flow whose every seam answers the ordinary thing.
func newHarness(t *testing.T) *harness {
	t.Helper()

	h := &harness{
		inventory: &fakeInventory{
			locations: map[string]string{testItemID: testLocationID},
		},
		committed: map[string]int64{},
		held:      map[string]int64{},
		links: map[string]map[string][]string{
			linkOrderFulfillment: {testOrderID: {testFulfillmentID}},
			linkVariantInventory: {testVariantID: {testItemID}},
		},
	}
	h.flow = ordercancel.New(h.inventory, h, h, h, slog.New(slog.DiscardHandler))

	return h
}

// handle runs the subscriber.
func (h *harness) handle(t *testing.T, e eventbus.Event) error {
	t.Helper()

	return h.flow.HandleLineCanceled(t.Context(), e)
}

// CommittedQuantities answers what a live parcel holds.
func (h *harness) CommittedQuantities(context.Context, []string) (map[string]int64, error) {
	if h.committedErr != nil {
		return nil, h.committedErr
	}

	return h.committed, nil
}

// ListMany answers the links.
func (h *harness) ListMany(_ context.Context, name string, _ []string) (map[string][]string, error) {
	if h.linkErr != nil {
		return nil, h.linkErr
	}

	return h.links[name], nil
}

// QuantitiesOfFulfillment answers what ONE parcel was holding.
func (h *harness) QuantitiesOfFulfillment(context.Context, string) (map[string]int64, error) {
	if h.heldErr != nil {
		return nil, h.heldErr
	}

	return h.held, nil
}

// ListManyByTo answers the links in reverse, which is how a parcel finds its
// order. The fixture inverts the SAME map rather than carrying a second one: two
// maps could disagree, and a test whose seams disagree proves nothing.
func (h *harness) ListManyByTo(
	_ context.Context, name string, toIDs []string,
) (map[string][]string, error) {
	if h.linkErr != nil {
		return nil, h.linkErr
	}

	out := map[string][]string{}
	for from, tos := range h.links[name] {
		for _, to := range tos {
			if slices.Contains(toIDs, to) {
				out[to] = append(out[to], from)
			}
		}
	}

	return out, nil
}

// DispatchableLinesJSON answers what the order sold and wrote off.
func (h *harness) DispatchableLinesJSON(context.Context, string) (json.RawMessage, error) {
	if h.linesErr != nil {
		return nil, h.linesErr
	}

	return json.RawMessage(h.lines), nil
}

// handleParcel runs the SECOND subscriber.
func (h *harness) handleParcel(t *testing.T, e eventbus.Event) error {
	t.Helper()

	return h.flow.HandleFulfillmentCanceled(t.Context(), e)
}

// fakeInventory records what the flow asked it to do.
type fakeInventory struct {
	mu          sync.Mutex
	locations   map[string]string
	locationErr error
	returnErr   error
	alreadyBack bool
	returned    int64
	calls       []int64
	// references records what the ledger was asked to hold unique, in order. It
	// is the field the idempotency argument is made of, so it is recorded rather
	// than discarded.
	references []string
}

// SaleLocations answers where the units left from.
func (f *fakeInventory) SaleLocations(context.Context, string) (map[string]string, error) {
	if f.locationErr != nil {
		return nil, f.locationErr
	}

	return f.locations, nil
}

// ReturnCanceled records the units.
func (f *fakeInventory) ReturnCanceled(
	_ context.Context, _, _ string, quantity int64, reference string,
) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.returnErr != nil {
		return false, f.returnErr
	}
	f.calls = append(f.calls, quantity)
	f.references = append(f.references, reference)
	if f.alreadyBack {
		return true, nil
	}
	f.returned += quantity

	return false, nil
}
