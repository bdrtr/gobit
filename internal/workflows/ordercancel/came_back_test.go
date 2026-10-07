package ordercancel_test

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/internal/workflows/ordercancel"
)

// The tests here are ADR 0423: a parcel that came back undelivered holds its
// units only as far as a return or a replacement speaks for them, and the
// fulfillment module applies that rule to what a cancellation passes it. The
// flow's part is to pass that figure from the order's lines; the module's tests
// hold the rule.

// orderLinesSpokenForJSON is [orderLinesJSON] with what a return or a
// replacement speaks for on the line.
func orderLinesSpokenForJSON(bought, canceled, spokenFor int64) string {
	return fmt.Sprintf(
		`[{"line_item_id":%q,"bought":%d,"canceled":%d,"spoken_for":%d,"variant_id":%q}]`,
		testLineItemID, bought, canceled, spokenFor, testVariantID)
}

// TestAWriteOffPassesTheLinesReturnsToTheCount: the write-off reads the order's
// lines and hands the module what a return or a replacement speaks for, so
// units that came back with neither behind them are counted as owed again and
// go back on the shelf.
func TestAWriteOffPassesTheLinesReturnsToTheCount(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.lines = orderLinesSpokenForJSON(5, 0, 2)

	require.NoError(t, h.handle(t, canceledEvent(5, 0, 2)))

	require.Len(t, h.spokenAsked, 1)
	assert.Equal(t, map[string]int64{testLineItemID: 2}, h.spokenAsked[0],
		"the count is asked with what a return or a replacement speaks for")
}

// TestAWriteOffThatCannotReadTheLinesIsTriedAgain: without the order's lines
// the count cannot be asked, so the act fails as a fault that may pass and puts
// nothing back; the bus calls it again.
func TestAWriteOffThatCannotReadTheLinesIsTriedAgain(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.linesErr = coreerrors.Unavailable("order_unavailable", "the order module is restarting")

	err := h.handle(t, canceledEvent(5, 0, 2))
	require.Error(t, err)
	assert.Equal(t, coreerrors.KindUnavailable, coreerrors.KindOf(err), "%v", err)
	assert.Empty(t, h.heldAsked, "no count is asked without what speaks for the units")
	assert.Zero(t, h.inventory.returned)
}

// TestAParcelCancelPassesTheLinesReturnsToTheCount: the parcel cancel already
// reads the order's lines and passes what a return or a replacement speaks for
// too.
func TestAParcelCancelPassesTheLinesReturnsToTheCount(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.lines = orderLinesSpokenForJSON(5, 3, 1)
	h.held[testLineItemID] = 3
	h.committedOf = map[string]map[string]int64{testFulfillmentID: {}}

	require.NoError(t, h.handleParcel(t, parcelEvent(testFulfillmentID)))

	require.NotEmpty(t, h.spokenAsked)
	assert.Equal(t, map[string]int64{testLineItemID: 1}, h.spokenAsked[0])
}

// cameBackEvent builds the event the fulfillment module publishes when a parcel
// is marked come back undelivered (ADR 0423).
func cameBackEvent(fulfillmentID, returnID string) eventbus.Event {
	return eventbus.Event{
		ID:   "fulfillment.returned:" + fulfillmentID,
		Name: "fulfillment.returned",
		Data: map[string]any{
			"fulfillment_id": fulfillmentID,
			"reference":      testOrderID,
			"return_id":      returnID,
			"returned_at":    "2026-10-07T12:00:00Z",
		},
	}
}

// TestAParcelThatComesBackPutsBackWhatTheWriteOffCouldNot is ADR 0423: one of
// three units was written off while the parcel holding all three was on the
// way, so nothing went back; the parcel comes back with no return or
// replacement behind it, and the flow brings the line up to its target, the one
// written-off unit, not the three the parcel held.
func TestAParcelThatComesBackPutsBackWhatTheWriteOffCouldNot(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.lines = orderLinesJSON(3, 1)
	h.held = map[string]int64{testLineItemID: 3}
	h.committed = map[string]int64{}

	require.NoError(t, h.flow.HandleFulfillmentReturned(t.Context(), cameBackEvent(testFulfillmentID, "")))

	assert.Equal(t, int64(1), h.inventory.returned,
		"the target is the written-off unit; the other two are owed again and stay deducted")
	require.Len(t, h.heldAsked, 1, "the count is read under the order's lock")
}

// TestAReturnParcelThatComesBackPutsNothingBack: a parcel bringing a return
// back is bound to no order and holds none of its outgoing units, so its coming
// back puts nothing back, as its cancel does not (ADR 0420). The same event
// without the return falls back to the reference and does.
func TestAReturnParcelThatComesBackPutsNothingBack(t *testing.T) {
	t.Parallel()

	for name, returnID := range map[string]string{"a return parcel": "oret_1", "an unlinked outgoing parcel": ""} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.lines = orderLinesJSON(3, 3)
			h.held = map[string]int64{testLineItemID: 3}
			h.committed = map[string]int64{}

			require.NoError(t, h.flow.HandleFulfillmentReturned(t.Context(), cameBackEvent("ful_unlinked", returnID)))

			if returnID != "" {
				assert.Zero(t, h.inventory.returned, "a parcel bringing a return back holds no order's units")
				assert.Empty(t, h.heldAsked)
			} else {
				assert.Equal(t, int64(3), h.inventory.returned, "the reference names the order")
			}
		})
	}
}

// TestTheFlowHearsAParcelComeBack is the production wiring: the flow its
// container builds is subscribed to fulfillment.returned.
func TestTheFlowHearsAParcelComeBack(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.lines = orderLinesJSON(3, 3)
	h.held = map[string]int64{testLineItemID: 3}
	h.committed = map[string]int64{}
	bus := eventbus.NewInMemory(slog.New(slog.DiscardHandler))
	c := container.New(nil)
	for name, service := range map[string]any{
		ordercancel.ServiceEventBus:    bus,
		ordercancel.ServiceInventory:   h.inventory,
		ordercancel.ServiceFulfillment: h,
		ordercancel.ServiceOrder:       h,
		ordercancel.ServiceLink:        h,
	} {
		require.NoError(t, c.Provide(name, service))
	}
	_, err := ordercancel.FromContainer(c, slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	require.NoError(t, bus.Publish(context.Background(), cameBackEvent(testFulfillmentID, "")))

	assert.Eventually(t, func() bool {
		h.inventory.mu.Lock()
		defer h.inventory.mu.Unlock()

		return h.inventory.returned == 3
	}, 5*time.Second, 10*time.Millisecond, "the three written-off units go back once the parcel came back")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, c.Shutdown(ctx))
}
