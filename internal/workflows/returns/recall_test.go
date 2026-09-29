package returns

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
)

// sentHarness is the box harness after a dispatch in parcel ful_1: the box's
// towel and soaps and the other line each hold a confirmed promise.
func sentHarness(t *testing.T) *harness {
	t.Helper()
	h := boxDispatchHarness(t)
	h.orders.replacement.Status = statusReplacementDispatched
	h.orders.replacement.FulfillmentID = testFulfillmentID
	h.orders.replacement.Lines[0].Parts[0].ReservationID = "invres_towel"
	h.orders.replacement.Lines[0].Parts[1].ReservationID = "invres_soap"
	h.orders.replacement.Lines[1].ReservationID = "invres_line"
	h.orders.parcelReplacement = testReplacementID
	return h
}

// canceledParcel is the event the fulfillment module publishes.
func canceledParcel(fulfillmentID string) eventbus.Event {
	return eventbus.Event{Name: topicFulfillmentCanceled, Data: map[string]any{fieldFulfillmentID: fulfillmentID}}
}

// TestACanceledParcelRecallsItsReplacement is ADR 0239's flow: every promise
// the parcel's goods were taken out under goes back, and then the record is
// sent back to waiting.
func TestACanceledParcelRecallsItsReplacement(t *testing.T) {
	h := sentHarness(t)

	require.NoError(t, h.wf.HandleFulfillmentCanceled(context.Background(), canceledParcel(testFulfillmentID)))

	assert.Equal(t, []string{"invres_towel", "invres_soap", "invres_line"}, h.inventory.recalled)
	assert.Equal(t, []dispatchCall{{replacementID: testReplacementID, fulfillmentID: testFulfillmentID}},
		h.orders.recalls)
}

// TestAParcelWithNoReplacementIsLeftAlone hears a sale's canceled parcel and
// does nothing: its units are the cancellation flow's.
func TestAParcelWithNoReplacementIsLeftAlone(t *testing.T) {
	h := sentHarness(t)
	h.orders.parcelReplacement = ""

	require.NoError(t, h.wf.HandleFulfillmentCanceled(context.Background(), canceledParcel("ful_SALE")))

	assert.Empty(t, h.inventory.recalled)
	assert.Empty(t, h.orders.recalls)
}

// TestAParcelTheReplacementLeftIsLeftAlone answers a late event: the
// replacement left again in another parcel, and its new promises stay held.
func TestAParcelTheReplacementLeftIsLeftAlone(t *testing.T) {
	h := sentHarness(t)
	h.orders.replacement.FulfillmentID = "ful_2"

	out, err := h.wf.RecallReplacement(context.Background(), testFulfillmentID)
	require.NoError(t, err)

	assert.Empty(t, out.ReplacementID)
	assert.Empty(t, h.inventory.recalled)
	assert.Empty(t, h.orders.recalls)
}

// TestTheUnitsGoBackBeforeTheRecordIsRecalled holds the order: a put-back that
// fails leaves the record saying the goods left, where the next delivery of
// the event finds it.
func TestTheUnitsGoBackBeforeTheRecordIsRecalled(t *testing.T) {
	h := sentHarness(t)
	h.inventory.recallErr = coreerrors.Unavailable("inventory_down", "the inventory is unreachable")

	err := h.wf.HandleFulfillmentCanceled(context.Background(), canceledParcel(testFulfillmentID))

	require.Error(t, err)
	assert.Equal(t, CodeStockNotReleased, coreerrors.CodeOf(err))
	assert.Empty(t, h.orders.recalls, "nothing says the goods are waiting while their units are counted gone")
}

// TestARecallIsFinishedByTheNextDelivery is the repetition: units already back
// are not counted again, and the record is still recalled.
func TestARecallIsFinishedByTheNextDelivery(t *testing.T) {
	h := sentHarness(t)
	h.inventory.alreadyBack = map[string]bool{"invres_towel": true, "invres_soap": true, "invres_line": true}

	out, err := h.wf.RecallReplacement(context.Background(), testFulfillmentID)
	require.NoError(t, err)

	assert.Equal(t, testReplacementID, out.ReplacementID)
	assert.Zero(t, out.RecalledPromises)
	assert.Len(t, h.orders.recalls, 1)
}

// TestTheNextParcelNamesTheRecall opens a recalled replacement's next parcel
// under a key of its own, since the first key resolves to the canceled parcel
// (ADR 0088).
func TestTheNextParcelNamesTheRecall(t *testing.T) {
	h := dispatchHarness(t)
	h.orders.replacement.Recalls = 2

	_, err := h.wf.DispatchReplacement(context.Background(), testReplacementID)
	require.NoError(t, err)

	require.Len(t, h.shipping.calls, 1)
	assert.Contains(t, h.shipping.calls[0].request, `"replacement-`+testReplacementID+`-2"`)
}

// TestTheFlowLogsWhereTheApplicationLogs is D162: built without a logger, as
// its wiring builds it, the flow wrote its "a human has to finish it" lines to
// a discard handler. It now writes them where the application logs.
func TestTheFlowLogsWhereTheApplicationLogs(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	h := sentHarness(t)
	w, err := New(Deps{
		Orders: h.orders, Inventory: h.inventory, Payments: h.payments, Links: h.links, Shipping: h.shipping,
	})
	require.NoError(t, err)
	h.inventory.recallErr = errors.New("inventory down")

	require.Error(t, w.HandleFulfillmentCanceled(context.Background(), canceledParcel(testFulfillmentID)))

	assert.Contains(t, buf.String(), "could not be recalled")
	assert.Contains(t, buf.String(), testFulfillmentID)
}

// recordingBus keeps the handlers a flow subscribes, by topic.
type recordingBus struct{ handlers map[string]eventbus.Handler }

// Subscribe records the handler.
func (b *recordingBus) Subscribe(eventName string, h eventbus.Handler) error {
	b.handlers[eventName] = h

	return nil
}

// TestTheWiredFlowHearsACanceledParcel is the subscription ADR 0239 depends
// on: the flow its wiring builds is subscribed to fulfillment.canceled, and
// the handler it subscribed recalls the replacement.
func TestTheWiredFlowHearsACanceledParcel(t *testing.T) {
	h := sentHarness(t)
	bus := &recordingBus{handlers: map[string]eventbus.Handler{}}
	c := container.New(nil)
	require.NoError(t, c.Provide(ServiceOrder, h.orders))
	require.NoError(t, c.Provide(ServiceInventory, h.inventory))
	require.NoError(t, c.Provide(ServicePayment, h.payments))
	require.NoError(t, c.Provide(ServiceLink, h.links))
	require.NoError(t, c.Provide(ServiceShipping, h.shipping))
	require.NoError(t, c.Provide(ServiceEventBus, bus))

	_, err := FromContainer(c)
	require.NoError(t, err)

	handler, subscribed := bus.handlers["fulfillment.canceled"]
	require.True(t, subscribed, "a flow built and not subscribed hears no canceled parcel")
	require.NoError(t, handler(context.Background(), canceledParcel(testFulfillmentID)))
	assert.Len(t, h.orders.recalls, 1)
}
