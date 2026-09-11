package fulfilling_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/workflows/fulfilling"
)

// newFlow builds the flow over fakes.
func newFlow(t *testing.T, orders *fakeOrders, ful *fakeFulfillments, links *fakeLinks) *fulfilling.Workflows {
	t.Helper()

	flow, err := fulfilling.New(fulfilling.Deps{Orders: orders, Fulfillments: ful, Links: links})
	require.NoError(t, err)

	return flow
}

// TestOpeningAShipmentBindsItToTheOrder is the whole point of the flow.
func TestOpeningAShipmentBindsItToTheOrder(t *testing.T) {
	t.Parallel()

	links := newFakeLinks()
	flow := newFlow(t, &fakeOrders{}, &fakeFulfillments{id: "ful_1"}, links)

	result, err := flow.OpenForOrder(context.Background(), "order_1", "so_1", "key-1")
	require.NoError(t, err)

	assert.Equal(t, "ful_1", result.FulfillmentID)
	assert.False(t, result.AlreadyOpen)
	assert.Equal(t, []string{"ful_1"}, links.bound["order_1"],
		"the shipment was opened and not bound; nothing could say which order the parcel is for")
}

// TestAnUnknownOrderOpensNoParcel is the refusal only this flow can make.
//
// The fulfillment module never validates the reference it is handed, so a typo
// would open a real parcel bound to nothing — found only when the customer
// asked where it was.
func TestAnUnknownOrderOpensNoParcel(t *testing.T) {
	t.Parallel()

	ful := &fakeFulfillments{id: "ful_1"}
	flow := newFlow(t, &fakeOrders{err: coreerrors.NotFound("order_not_found", "no such order")},
		ful, newFakeLinks())

	_, err := flow.OpenForOrder(context.Background(), "order_missing", "so_1", "key-1")

	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "the refusal lost its kind: %v", err)
	assert.Zero(t, ful.calls, "a parcel was opened for an order that does not exist")
}

// TestASecondPressOpensNoSecondParcel is what the idempotency key buys, and the
// flow has to REPORT it rather than answer the same way either way.
func TestASecondPressOpensNoSecondParcel(t *testing.T) {
	t.Parallel()

	links := newFakeLinks()
	ful := &fakeFulfillments{id: "ful_1"}
	flow := newFlow(t, &fakeOrders{}, ful, links)
	ctx := context.Background()

	first, err := flow.OpenForOrder(ctx, "order_1", "so_1", "key-1")
	require.NoError(t, err)
	second, err := flow.OpenForOrder(ctx, "order_1", "so_1", "key-1")
	require.NoError(t, err)

	assert.False(t, first.AlreadyOpen)
	assert.True(t, second.AlreadyOpen,
		"the second press was reported as a new parcel; an operator would believe two exist")
	assert.Equal(t, []string{"ful_1"}, links.bound["order_1"])
}

// TestAMissingIdempotencyKeyIsRefused keeps the one input that cannot be
// defaulted from being defaulted.
func TestAMissingIdempotencyKeyIsRefused(t *testing.T) {
	t.Parallel()

	ful := &fakeFulfillments{id: "ful_1"}
	flow := newFlow(t, &fakeOrders{}, ful, newFakeLinks())

	_, err := flow.OpenForOrder(context.Background(), "order_1", "so_1", "  ")

	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "%v", err)
	assert.Zero(t, ful.calls)
}

// TestABindingFailureReportsTheParcelThatExists is the failure this flow can
// leave, and the message is the whole remedy.
//
// Saying only "it failed" would invite the operator to press again, and with a
// fresh key that opens a SECOND parcel.
func TestABindingFailureReportsTheParcelThatExists(t *testing.T) {
	t.Parallel()

	links := newFakeLinks()
	links.createErr = errors.New("the link table is unreachable")
	flow := newFlow(t, &fakeOrders{}, &fakeFulfillments{id: "ful_7"}, links)

	result, err := flow.OpenForOrder(context.Background(), "order_1", "so_1", "key-1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ful_7",
		"the error does not name the parcel that EXISTS; the operator cannot repair the "+
			"binding and will open a second one")
	assert.Equal(t, "ful_7", result.FulfillmentID,
		"the result dropped the shipment id the caller needs to recover")
}

// TestAnUnreadableStatusStillReportsTheShipment keeps one module's fault from
// hiding another module's facts.
func TestAnUnreadableStatusStillReportsTheShipment(t *testing.T) {
	t.Parallel()

	links := newFakeLinks()
	links.bound["order_1"] = []string{"ful_1"}
	flow := newFlow(t, &fakeOrders{},
		&fakeFulfillments{statusErr: errors.New("unreachable")}, links)

	shipments, err := flow.ShipmentsOfOrder(context.Background(), "order_1")
	require.NoError(t, err, "one unreadable status failed the whole listing")

	require.Len(t, shipments, 1)
	assert.Equal(t, "ful_1", shipments[0].FulfillmentID)
	assert.Empty(t, shipments[0].Status)
}

// fakeOrders stands in for the order module's surface.
type fakeOrders struct {
	err error
	// lines is what DispatchableLinesJSON answers; nil means an empty order.
	lines    []testLine
	linesErr error
}

// testLine is the order module's answer as a CONSUMER writes it.
//
// It is the flow's own struct copied by its JSON tags rather than imported, which
// is the shape every consumer of a JSON interop surface has to use — and writing it
// out here is what would catch a tag drifting on the producer's side.
type testLine struct {
	LineItemID string `json:"line_item_id"`
	Bought     int64  `json:"bought"`
	Canceled   int64  `json:"canceled"`
}

// OrderContactJSON reports whether the order exists.
func (f *fakeOrders) OrderContactJSON(context.Context, string) (json.RawMessage, error) {
	if f.err != nil {
		return nil, f.err
	}

	return json.RawMessage(`{}`), nil
}

// DispatchableLinesJSON answers what the order sold and what was written off.
func (f *fakeOrders) DispatchableLinesJSON(context.Context, string) (json.RawMessage, error) {
	if f.linesErr != nil {
		return nil, f.linesErr
	}

	return json.Marshal(f.lines)
}

// fakeFulfillments stands in for the fulfillment module's surface.
type fakeFulfillments struct {
	id    string
	calls int
	// status is what FulfillmentStatus answers; empty means "pending".
	status    string
	statusErr error
	// committed is what CommittedQuantities answers, per line.
	committed    map[string]int64
	committedErr error
	// committedCalls counts the questions, which is how a test proves an order with
	// no parcels is not asked at all.
	committedCalls int
}

// CommittedQuantities answers what the live parcels hold.
func (f *fakeFulfillments) CommittedQuantities(
	context.Context, []string,
) (map[string]int64, error) {
	f.committedCalls++
	if f.committedErr != nil {
		return nil, f.committedErr
	}

	return f.committed, nil
}

// CreateFulfillment returns the same id whatever the key, the way an idempotent
// provider does for a repeated key.
func (f *fakeFulfillments) CreateFulfillment(context.Context, string, string, string) (string, error) {
	f.calls++

	return f.id, nil
}

// FulfillmentStatus answers with a fixed status or the injected fault.
func (f *fakeFulfillments) FulfillmentStatus(context.Context, string) (string, error) {
	if f.statusErr != nil {
		return "", f.statusErr
	}
	if f.status != "" {
		return f.status, nil
	}

	return "pending", nil
}

// fakeLinks stands in for the core's link service.
type fakeLinks struct {
	bound     map[string][]string
	createErr error
	// listErr, when set, makes ListMany fail. Reading the parcels of an order is
	// what bounds a parcel's contents, so a failure there must not read as "no
	// parcels" (ADR 0135).
	listErr error
}

// newFakeLinks builds an empty link store.
func newFakeLinks() *fakeLinks { return &fakeLinks{bound: map[string][]string{}} }

// Create binds the pair; binding the same pair twice is a no-op.
func (f *fakeLinks) Create(_ context.Context, _, fromID, toID string) error {
	if f.createErr != nil {
		return f.createErr
	}
	for _, existing := range f.bound[fromID] {
		if existing == toID {
			return nil
		}
	}
	f.bound[fromID] = append(f.bound[fromID], toID)

	return nil
}

// ListMany returns what each id is bound to.
func (f *fakeLinks) ListMany(_ context.Context, _ string, fromIDs []string) (map[string][]string, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}

	out := map[string][]string{}
	for _, id := range fromIDs {
		out[id] = f.bound[id]
	}

	return out, nil
}

// TestAKeyThatNamesACanceledShipmentOpensNothing pins the defect this flow
// carried.
//
// # The chain, and where the lie was
//
// An idempotency key OUTLIVES the shipment it opened: the module's uniqueness
// is over every shipment, canceled ones included, so a repeated key resolves to
// the canceled parcel. The module is right to return it — its contract is "the
// same key returns the same shipment" and it says nothing about status.
//
// The lie was HERE. A cancel does not remove the order-to-shipment binding —
// nothing in this package deletes one — so AlreadyOpen was computed from a link
// that outlived the parcel, and the caller was told a canceled shipment was
// open. Nothing was going to ship and nothing said so.
func TestAKeyThatNamesACanceledShipmentOpensNothing(t *testing.T) {
	t.Parallel()

	fulfillments := &fakeFulfillments{id: "ful_1", status: "canceled"}
	flow := newFlow(t, &fakeOrders{}, fulfillments, newFakeLinks())

	_, err := flow.OpenForOrder(context.Background(), "order_1", "sopt_1", "key-1")

	require.Error(t, err, "a key naming a canceled shipment may not report an open one")
	assert.Equal(t, fulfilling.CodeShipmentCanceled, coreerrors.CodeOf(err))
	assert.Contains(t, err.Error(), "ful_1",
		"the refusal has to name the shipment, or a fresh key is a guess")
	assert.Contains(t, err.Error(), "NEW key",
		"the caller has to be told what WOULD work; a refusal with no way forward is a dead end")
}

// TestALiveShipmentStillOpens keeps the refusal from swallowing the ordinary
// path.
//
// The check is on the status and only on the status: a pending shipment, opened
// for the first time or returned for a repeated key, is answered exactly as it
// was before.
func TestALiveShipmentStillOpens(t *testing.T) {
	t.Parallel()

	fulfillments := &fakeFulfillments{id: "ful_1"}
	flow := newFlow(t, &fakeOrders{}, fulfillments, newFakeLinks())

	result, err := flow.OpenForOrder(context.Background(), "order_1", "sopt_1", "key-1")

	require.NoError(t, err)
	assert.Equal(t, "ful_1", result.FulfillmentID)
}

// TestAStatusThatCannotBeReadDoesNotPassAsOpen keeps the new read from failing
// open.
//
// The status is what separates a live parcel from a canceled one, so a status
// that could not be read leaves the question unanswered — and an unanswered
// question about whether goods will move is not a yes. The parcel EXISTS by
// then, so the error carries its id for the same reason the binding failure
// does: pressing the button again with a fresh key opens a second one.
func TestAStatusThatCannotBeReadDoesNotPassAsOpen(t *testing.T) {
	t.Parallel()

	fulfillments := &fakeFulfillments{
		id:        "ful_1",
		statusErr: coreerrors.Unavailable("db_down", "the database is unreachable"),
	}
	flow := newFlow(t, &fakeOrders{}, fulfillments, newFakeLinks())

	_, err := flow.OpenForOrder(context.Background(), "order_1", "sopt_1", "key-1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ful_1", "the opened shipment has to be named")
}
