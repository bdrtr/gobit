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

// oneItem is a parcel's list naming one line, the shape the tests about opening
// a parcel use so that the default to what the order owes stays out of them
// (ADR 0409).
var oneItem = []fulfilling.OpenItem{{LineItemID: "li_1", Quantity: 1}}

// newFlow builds the flow over fakes.
func newFlow(t *testing.T, orders *fakeOrders, ful *fakeFulfillments, links *fakeLinks) *fulfilling.Workflows {
	t.Helper()

	flow, err := fulfilling.New(fulfilling.Deps{
		Orders: orders, Fulfillments: ful, Links: links, Payments: &fakePayments{},
	})
	require.NoError(t, err)

	return flow
}

// TestOpeningAShipmentReportsTheParcelItOpened is what is left of the flow's job.
//
// The BINDING moved. This flow used to write the "order_fulfillment" link after
// the module opened the parcel, and the module's own admin endpoint — the only
// one that carries an item breakdown — wrote none, so those parcels belonged to
// no order as far as every question asked through the link was concerned. The
// definition's owner now writes it inside CreateFulfillment, which both paths go
// through, and the tests for it live beside the write (ADR 0140).
func TestOpeningAShipmentReportsTheParcelItOpened(t *testing.T) {
	t.Parallel()

	flow := newFlow(t, &fakeOrders{}, &fakeFulfillments{id: "ful_1"}, newFakeLinks())

	result, err := flow.OpenForOrder(context.Background(), "order_1", "so_1", "key-1", oneItem)
	require.NoError(t, err)

	assert.Equal(t, "ful_1", result.FulfillmentID)
	assert.False(t, result.AlreadyOpen)
	assert.Equal(t, "order_1", result.OrderID)
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

	_, err := flow.OpenForOrder(context.Background(), "order_missing", "so_1", "key-1", oneItem)

	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "the refusal lost its kind: %v", err)
	assert.Zero(t, ful.calls, "a parcel was opened for an order that does not exist")
}

// TestASecondPressOpensNoSecondParcel is what the idempotency key buys, and the
// flow has to REPORT it rather than answer the same way either way.
func TestASecondPressOpensNoSecondParcel(t *testing.T) {
	t.Parallel()

	links := newFakeLinks()
	ful := &fakeFulfillments{id: "ful_1", links: links}
	flow := newFlow(t, &fakeOrders{}, ful, links)
	ctx := context.Background()

	first, err := flow.OpenForOrder(ctx, "order_1", "so_1", "key-1", oneItem)
	require.NoError(t, err)
	second, err := flow.OpenForOrder(ctx, "order_1", "so_1", "key-1", oneItem)
	require.NoError(t, err)

	assert.False(t, first.AlreadyOpen)
	assert.True(t, second.AlreadyOpen,
		"the second press was reported as a new parcel; an operator would believe two exist")
	assert.Equal(t, 2, ful.calls,
		"both presses reached the module, which is what makes its idempotency the "+
			"thing being relied on rather than a check this flow does")
}

// TestAMissingIdempotencyKeyIsRefused keeps the one input that cannot be
// defaulted from being defaulted.
func TestAMissingIdempotencyKeyIsRefused(t *testing.T) {
	t.Parallel()

	ful := &fakeFulfillments{id: "ful_1"}
	flow := newFlow(t, &fakeOrders{}, ful, newFakeLinks())

	_, err := flow.OpenForOrder(context.Background(), "order_1", "so_1", "  ", oneItem)

	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "%v", err)
	assert.Zero(t, ful.calls)
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
	// destination is what ShippingAddressJSON answers; nil answers JSON null,
	// an order with no shipping address.
	destination map[string]any
	// lines is what DispatchableLinesJSON answers; nil means an empty order.
	lines    []testLine
	linesErr error
	// corrected counts the corrections that reached the order module, and
	// correctedWith is the last body.
	corrected     int
	correctedWith json.RawMessage
	// correctedOrder and correctedFrom are the order and the row read the
	// last correction named (ADR 0388).
	correctedOrder string
	correctedFrom  string
	// parent and parentErr are ShippingParentOf's answer.
	parent    string
	parentErr error
	// sold is ShippingOptionOf's answer.
	sold string
	// facts is DeliveryFactsJSON's answer.
	facts map[string]any
	// changed counts the delivery changes that reached the order module, and
	// changedWith is the last request.
	changed     int
	changedWith json.RawMessage
	// returnDetail is what ReturnDetailJSON answers, written as the producer
	// writes it; returnErr is its error.
	returnDetail json.RawMessage
	returnErr    error
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

// ShippingAddressJSON reports whether the order exists, and where it goes.
func (f *fakeOrders) ShippingAddressJSON(context.Context, string) (json.RawMessage, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.destination == nil {
		return json.RawMessage("null"), nil
	}

	return json.Marshal(f.destination)
}

// ShippingOptionOf answers the scripted option.
func (f *fakeOrders) ShippingOptionOf(context.Context, string) (string, error) {
	return f.sold, nil
}

// ShippingParentOf answers the scripted parent, or the scripted refusal.
func (f *fakeOrders) ShippingParentOf(context.Context, string) (string, error) {
	if f.parentErr != nil {
		return "", f.parentErr
	}

	return f.parent, nil
}

// CorrectShippingAddressJSON records the correction and echoes the body.
func (f *fakeOrders) CorrectShippingAddressJSON(
	_ context.Context, orderID string, address json.RawMessage, readAddressID string,
) (json.RawMessage, error) {
	f.corrected++
	f.correctedWith = address
	f.correctedOrder, f.correctedFrom = orderID, readAddressID

	return address, nil
}

// DeliveryFactsJSON answers the scripted facts.
func (f *fakeOrders) DeliveryFactsJSON(context.Context, string) (json.RawMessage, error) {
	if f.err != nil {
		return nil, f.err
	}

	return json.Marshal(f.facts)
}

// ChangeDeliveryJSON records the change and echoes the request.
func (f *fakeOrders) ChangeDeliveryJSON(
	_ context.Context, _ string, request json.RawMessage,
) (json.RawMessage, error) {
	f.changed++
	f.changedWith = request

	return request, nil
}

// DispatchableLinesJSON answers what the order sold and what was written off.
func (f *fakeOrders) DispatchableLinesJSON(context.Context, string) (json.RawMessage, error) {
	if f.linesErr != nil {
		return nil, f.linesErr
	}

	return json.Marshal(f.lines)
}

// ReturnDetailJSON answers the scripted return.
func (f *fakeOrders) ReturnDetailJSON(context.Context, string) (json.RawMessage, error) {
	if f.returnErr != nil {
		return nil, f.returnErr
	}

	return f.returnDetail, nil
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
	// links is where this fake writes the binding, because the real module writes
	// one inside CreateFulfillment (ADR 0140).
	//
	// A fake that skipped it would be a fake that disagrees with its producer on
	// the one fact the flow READS BACK: "already open" is decided by whether the
	// parcel was bound BEFORE the call, so a fake that never binds reports every
	// repeat as a fresh parcel. That is exactly what happened when the write
	// moved, and the test caught it.
	links *fakeLinks
	// destination is the last destination the flow handed on.
	destination json.RawMessage
	// option is the last shipping option the flow opened a parcel on.
	option string
	// options is what ListOptionsJSON answers, and quoteErr its fault;
	// quotedWith is the last request.
	options    []map[string]any
	quoteErr   error
	quotedWith json.RawMessage

	// holding says the parcel was opened holding the order's goods and items
	// is the list it was given, nil for what the order owes (ADR 0409);
	// holdingErr is the module's refusal.
	holding    bool
	items      json.RawMessage
	holdingErr error
}

// ListOptionsJSON records the request and answers the scripted options.
func (f *fakeFulfillments) ListOptionsJSON(
	_ context.Context, request json.RawMessage,
) (json.RawMessage, error) {
	f.quotedWith = request
	if f.quoteErr != nil {
		return nil, f.quoteErr
	}

	return json.Marshal(map[string]any{"options": f.options})
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
func (f *fakeFulfillments) CreateFulfillment(
	ctx context.Context, reference, optionID, _ string, destination json.RawMessage,
) (string, error) {
	f.calls++
	f.destination = destination
	f.option = optionID

	if f.links != nil {
		if err := f.links.Create(ctx, "order_fulfillment", reference, f.id); err != nil {
			return "", err
		}
	}

	return f.id, nil
}

// CreateFulfillmentHolding records the items it was asked to hold, nil for
// "what the order owes", and opens the parcel the way CreateFulfillment does
// (ADR 0409). holdingErr, when set, is the module's refusal.
func (f *fakeFulfillments) CreateFulfillmentHolding(
	ctx context.Context, reference, optionID, key string, destination, items json.RawMessage,
) (string, error) {
	f.holding = true
	f.items = items
	if f.holdingErr != nil {
		return "", f.holdingErr
	}

	return f.CreateFulfillment(ctx, reference, optionID, key, destination)
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

	_, err := flow.OpenForOrder(context.Background(), "order_1", "sopt_1", "key-1", oneItem)

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

	result, err := flow.OpenForOrder(context.Background(), "order_1", "sopt_1", "key-1", oneItem)

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

	_, err := flow.OpenForOrder(context.Background(), "order_1", "sopt_1", "key-1", oneItem)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ful_1", "the opened shipment has to be named")
}

// TestTheParcelGoesWhereTheOrderWent hands the order's shipping address to the
// carrier as the order module sent it, and hands nothing on for an order that
// recorded none (ADR 0194).
func TestTheParcelGoesWhereTheOrderWent(t *testing.T) {
	t.Parallel()

	orders := &fakeOrders{destination: map[string]any{
		"first_name": "Ada", "address_1": "12 Main St", "country_code": "US",
	}}
	ful := &fakeFulfillments{id: "ful_1", links: newFakeLinks()}
	flow := newFlow(t, orders, ful, ful.links)

	_, err := flow.OpenForOrder(context.Background(), "order_1", "so_1", "key-1", oneItem)
	require.NoError(t, err)
	assert.JSONEq(t, `{"first_name":"Ada","address_1":"12 Main St","country_code":"US"}`,
		string(ful.destination))

	none := &fakeFulfillments{id: "ful_2", links: newFakeLinks()}
	_, err = newFlow(t, &fakeOrders{}, none, none.links).
		OpenForOrder(context.Background(), "order_2", "so_1", "key-2", oneItem)
	require.NoError(t, err)
	assert.JSONEq(t, `null`, string(none.destination), "an order with no address hands on none")
}

// TestAParcelGoesOnTheServiceTheOrderWasSold opens a parcel with no option
// named on the one the order was sold, keeps a named one, and refuses when
// there is nothing to default to (ADR 0198).
func TestAParcelGoesOnTheServiceTheOrderWasSold(t *testing.T) {
	t.Parallel()

	sold := &fakeFulfillments{id: "ful_1", links: newFakeLinks()}
	_, err := newFlow(t, &fakeOrders{sold: "so_express"}, sold, sold.links).
		OpenForOrder(context.Background(), "order_1", "", "key-1", oneItem)
	require.NoError(t, err)
	assert.Equal(t, "so_express", sold.option, "the parcel goes on the service the shopper paid for")

	named := &fakeFulfillments{id: "ful_2", links: newFakeLinks()}
	_, err = newFlow(t, &fakeOrders{sold: "so_express"}, named, named.links).
		OpenForOrder(context.Background(), "order_1", "so_standard", "key-2", oneItem)
	require.NoError(t, err)
	assert.Equal(t, "so_standard", named.option, "an option the operator names is kept")

	none := &fakeFulfillments{id: "ful_3", links: newFakeLinks()}
	_, err = newFlow(t, &fakeOrders{}, none, none.links).
		OpenForOrder(context.Background(), "order_1", " ", "key-3", oneItem)
	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "%v", err)
	assert.Zero(t, none.calls, "nothing to default to opens nothing")
}

// TestAWholeOrderParcelAsksForWhatIsOwed opens a parcel naming no items on an
// order sold one delivery, and asks the module for every unit still owed rather
// than for an empty parcel (ADR 0409, gap D264).
func TestAWholeOrderParcelAsksForWhatIsOwed(t *testing.T) {
	t.Parallel()

	ful := &fakeFulfillments{id: "ful_1", links: newFakeLinks()}
	_, err := newFlow(t, &fakeOrders{sold: "so_express"}, ful, ful.links).
		OpenForOrder(context.Background(), "order_1", "", "key-1", nil)
	require.NoError(t, err)
	assert.True(t, ful.holding, "the parcel holds the order's goods")
	assert.Nil(t, ful.items, "no list is sent: the module fills what the order owes")
	assert.Equal(t, "so_express", ful.option)

	named := &fakeFulfillments{id: "ful_2", links: newFakeLinks()}
	_, err = newFlow(t, &fakeOrders{sold: "so_express"}, named, named.links).
		OpenForOrder(context.Background(), "order_1", "so_standard", "key-2", []fulfilling.OpenItem{})
	require.NoError(t, err)
	assert.True(t, named.holding)
	assert.Nil(t, named.items, "an option named on an order sold one delivery still defaults its goods")
	assert.Equal(t, "so_standard", named.option)
}

// TestAnOrderSoldSeveralDeliveriesNamesItsItems refuses a parcel naming no items
// on an order not sold exactly one delivery: one parcel would take every
// delivery's goods (ADR 0409).
func TestAnOrderSoldSeveralDeliveriesNamesItsItems(t *testing.T) {
	t.Parallel()

	ful := &fakeFulfillments{id: "ful_1", links: newFakeLinks()}
	_, err := newFlow(t, &fakeOrders{}, ful, ful.links).
		OpenForOrder(context.Background(), "order_1", "so_standard", "key-1", nil)
	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "%v", err)
	assert.Equal(t, fulfilling.CodeItemsRequired, coreerrors.CodeOf(err))
	assert.Zero(t, ful.calls, "nothing is opened")
}

// TestNamedItemsArePassedAsNamed sends the list the operator named to the
// module as it is, and passes the module's refusal through with its own code
// (ADR 0409).
func TestNamedItemsArePassedAsNamed(t *testing.T) {
	t.Parallel()

	ful := &fakeFulfillments{id: "ful_1", links: newFakeLinks()}
	_, err := newFlow(t, &fakeOrders{}, ful, ful.links).OpenForOrder(context.Background(), "order_1",
		"so_standard", "key-1", []fulfilling.OpenItem{{LineItemID: "li_1", Quantity: 2}, {LineItemID: "li_2", Quantity: 1}})
	require.NoError(t, err)
	assert.True(t, ful.holding)
	assert.JSONEq(t, `[{"line_item_id":"li_1","quantity":2},{"line_item_id":"li_2","quantity":1}]`, string(ful.items))

	refused := &fakeFulfillments{id: "ful_2", links: newFakeLinks(),
		holdingErr: coreerrors.Conflict("fulfillment_nothing_owed", "order_1 owes no unit")}
	_, err = newFlow(t, &fakeOrders{sold: "so_express"}, refused, refused.links).
		OpenForOrder(context.Background(), "order_1", "", "key-2", nil)
	require.Error(t, err)
	assert.True(t, coreerrors.IsConflict(err), "%v", err)
	assert.Equal(t, "fulfillment_nothing_owed", coreerrors.CodeOf(err),
		"the module's refusal is the answer the operator reads")
}

// TestAReplacementParcelHoldsNoOrderLine opens an after-sale replacement's parcel
// through the call that holds no order line, on an order sold several
// deliveries too, since the goods are not the order's lines (ADR 0409).
func TestAReplacementParcelHoldsNoOrderLine(t *testing.T) {
	t.Parallel()

	ful := &fakeFulfillments{id: "ful_1", links: newFakeLinks()}
	result, err := newFlow(t, &fakeOrders{}, ful, ful.links).
		OpenForReplacement(context.Background(), "order_1", "so_standard", "replacement-1")
	require.NoError(t, err)
	assert.Equal(t, "ful_1", result.FulfillmentID)
	assert.False(t, ful.holding, "a replacement's parcel holds no order line")
	assert.Equal(t, 1, ful.calls)
}
