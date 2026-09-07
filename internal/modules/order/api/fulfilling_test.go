package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/api"
)

// shipmentsPath is where both shipment endpoints live: POST opens a parcel for
// the order, GET lists the parcels already opened for it. It is named once
// because six tests below ask for it, and a path retyped per test is a path
// that can drift from the router without a single test noticing.
const shipmentsPath = "/admin/v1/orders/order_1/fulfillments"

// fakeFulfilling is the fulfilling flow's stand-in.
//
// It records the body VERBATIM rather than decoding it, because the claim the
// tests below make about the open endpoint is precisely that this module does
// not interpret the body: a fake that decoded it into a struct would throw away
// the evidence.
type fakeFulfilling struct {
	fulfillmentID string
	alreadyOpen   bool
	shipments     json.RawMessage
	err           error

	gotOrderID string
	gotBody    json.RawMessage
	openCalls  int
	listCalls  int
}

// That the fake satisfies the surface the handler expects is verified at
// compile time.
var _ api.Fulfilling = (*fakeFulfilling)(nil)

// OpenForOrder records the call and returns the scripted outcome.
func (f *fakeFulfilling) OpenForOrder(
	_ context.Context, orderID string, request json.RawMessage,
) (fulfillmentID string, alreadyOpen bool, err error) {
	f.openCalls++
	f.gotOrderID = orderID
	f.gotBody = request

	if f.err != nil {
		return "", false, f.err
	}

	return f.fulfillmentID, f.alreadyOpen, nil
}

// ShipmentsOfOrderJSON records the call and returns the scripted list.
func (f *fakeFulfilling) ShipmentsOfOrderJSON(
	_ context.Context, orderID string,
) (json.RawMessage, error) {
	f.listCalls++
	f.gotOrderID = orderID

	if f.err != nil {
		return nil, f.err
	}

	return f.shipments, nil
}

// newRouterWithFulfilling wires a router with the given fulfilling flow.
//
// The flow may be nil; the shipment endpoints failing CLOSED without it can
// only be exercised that way.
func newRouterWithFulfilling(svc api.Orders, fulfilling api.Fulfilling) chi.Router {
	r := chi.NewRouter()
	api.New(svc, &fakeReceiving{}, nil, fulfilling).Routes(r)

	return r
}

// TestOpeningAShipmentHandsTheBodyToTheFlowUNTOUCHED is the reason the request
// is carried as raw JSON and not as a struct.
//
// The order module does not own the vocabulary of a shipment. What a parcel
// needs — the option, the items, whatever the fulfillment module adds next — is
// the OTHER module's contract, and [api.openShipmentRequest] exists only so the
// OpenAPI document has something to describe. If this handler decoded the body
// and re-encoded it, every field the fulfillment module gained would be
// silently dropped here until somebody edited a struct in a module that has no
// business knowing about it — and the failure would look like the flow ignoring
// a field the client demonstrably sent.
func TestOpeningAShipmentHandsTheBodyToTheFlowUNTOUCHED(t *testing.T) {
	flow := &fakeFulfilling{fulfillmentID: "ful_1"}
	r := newRouterWithFulfilling(&fakeOrders{}, flow)

	// "metadata" is deliberately a field openShipmentRequest does not name.
	body := `{"shipping_option_id":"so_1","idempotency_key":"idem_1","metadata":{"note":"fragile"}}`

	rec := doRequest(t, r, http.MethodPost, shipmentsPath, body)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, 1, flow.openCalls)
	assert.Equal(t, "order_1", flow.gotOrderID,
		"the flow has to be told which order the parcel belongs to; that binding is the "+
			"whole reason the endpoint lives on the order")
	assert.JSONEq(t, body, string(flow.gotBody),
		"the flow has to receive the body the client sent, field for field; a body rebuilt "+
			"here drops whatever the fulfillment module understands and this module does not")
}

// TestASecondOpenIsAnnouncedAsHavingCreatedNothing is the difference between
// one parcel and two.
//
// The idempotency key makes the second press of the button harmless in the
// flow, but harmless is not the same as INVISIBLE. An operator who gets 201 a
// second time reads it as a second shipment and prints a second label; the two
// labels then meet at the carrier, on one parcel, and the shop pays for a
// dispatch it never made. So the answer has to differ in both places a client
// looks: the status line says nothing was created, and the body says so in a
// field a UI can render.
func TestASecondOpenIsAnnouncedAsHavingCreatedNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		alreadyOpen bool
		status      int
	}{
		"first press":  {alreadyOpen: false, status: http.StatusCreated},
		"second press": {alreadyOpen: true, status: http.StatusOK},
	} {
		t.Run(name, func(t *testing.T) {
			flow := &fakeFulfilling{fulfillmentID: "ful_1", alreadyOpen: tc.alreadyOpen}
			r := newRouterWithFulfilling(&fakeOrders{}, flow)

			rec := doRequest(t, r, http.MethodPost, shipmentsPath,
				`{"shipping_option_id":"so_1","idempotency_key":"idem_1"}`)

			require.Equal(t, tc.status, rec.Code, rec.Body.String())

			data, ok := decodeResponse(t, rec)["data"].(map[string]any)
			require.True(t, ok, rec.Body.String())
			assert.Equal(t, "ful_1", data["fulfillment_id"],
				"both answers name the shipment; the second press points at the FIRST one")
			assert.Equal(t, tc.alreadyOpen, data["already_open"],
				"whether anything was created has to be readable from the body as well as "+
					"from the status line")
		})
	}
}

// TestAShipmentIsNotOpenedFromAnEmptyBody refuses here rather than downstream.
//
// The body carries the shipping option and the idempotency key, and an empty
// one carries neither. Letting it through would put the refusal in the flow,
// which would report a missing shipping option — an error that reads as though
// the client sent a wrong option instead of no body at all, and which points
// the operator at the wrong thing to fix. Worse, a body-less retry is exactly
// the shape of the mistake the idempotency key exists to catch, and without a
// key nothing catches it.
func TestAShipmentIsNotOpenedFromAnEmptyBody(t *testing.T) {
	flow := &fakeFulfilling{fulfillmentID: "ful_1"}
	r := newRouterWithFulfilling(&fakeOrders{}, flow)

	rec := doRequest(t, r, http.MethodPost, shipmentsPath, "")

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Zero(t, flow.openCalls,
		"a request that cannot carry an idempotency key must not reach the flow at all")

	failure, ok := decodeResponse(t, rec)["error"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.Equal(t, "order_invalid_request", failure["code"],
		"the code has to say the REQUEST was wrong, not that a shipping option was missing")
}

// TestTheShipmentEndpointsFailClosedWhenTheFlowIsNotBound covers the adapter
// that stands between the handlers and the flow.
//
// A deployment can leave the fulfilling flow unbound — it is an optional
// argument of [api.New] — and the two endpoints then have to refuse rather than
// improvise. For the open endpoint the improvisation would be a real parcel
// bound to nothing: the label exists, the carrier has it, and no query can
// afterwards say which order it belonged to. For the list endpoint the
// improvisation is quieter and just as wrong — an empty list is an ANSWER, and
// the support desk would read "this order has no parcels" off a server that
// simply cannot look.
func TestTheShipmentEndpointsFailClosedWhenTheFlowIsNotBound(t *testing.T) {
	for name, tc := range map[string]struct {
		method string
		body   string
	}{
		"open": {method: http.MethodPost, body: `{"shipping_option_id":"so_1","idempotency_key":"k"}`},
		"list": {method: http.MethodGet, body: ""},
	} {
		t.Run(name, func(t *testing.T) {
			svc := &fakeOrders{}
			r := newRouterWithFulfilling(svc, nil)

			rec := doRequest(t, r, tc.method, shipmentsPath, tc.body)

			require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
			assert.Empty(t, svc.calls, "nothing may be written or read when the flow is absent")

			failure, ok := decodeResponse(t, rec)["error"].(map[string]any)
			require.True(t, ok, rec.Body.String())
			assert.Equal(t, "order_workflow_unavailable", failure["code"],
				"the code has to name a MISSING FLOW; a generic internal error would send "+
					"whoever is on call looking for a database fault")
		})
	}
}

// TestAShipmentThatCouldNotBeOpenedIsNotAnnouncedAsOne keeps the failure of the
// flow from being dressed up as a parcel.
//
// The flow refuses for reasons this module cannot judge — the order is
// canceled, the option belongs to another region, the key was reused with a
// different body. Whatever the reason, the client must not get an envelope with
// a fulfillment id in it, because the id would be empty and the operator would
// go looking for a shipment nobody opened.
func TestAShipmentThatCouldNotBeOpenedIsNotAnnouncedAsOne(t *testing.T) {
	flow := &fakeFulfilling{err: errors.Conflict(
		"fulfillment_order_not_shippable", "the order is canceled")}
	r := newRouterWithFulfilling(&fakeOrders{}, flow)

	rec := doRequest(t, r, http.MethodPost, shipmentsPath,
		`{"shipping_option_id":"so_1","idempotency_key":"idem_1"}`)

	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())

	payload := decodeResponse(t, rec)
	assert.NotContains(t, payload, "data",
		"a refusal must not carry a data envelope; a client reading data.fulfillment_id "+
			"off it would take an empty string for a shipment")

	failure, ok := payload["error"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.Equal(t, "fulfillment_order_not_shippable", failure["code"],
		"the flow's own code has to survive the hop; a code rewritten here would hide "+
			"which module refused")
}

// TestTheOrdersShipmentsArriveAsJSONAndNotAsAStringOfJSON is the one thing the
// list endpoint can get wrong.
//
// The flow answers with a document this module does not model, and the handler
// embeds it. Embedded as a raw message it becomes part of the response object
// and a client reads data[0].tracking_number. Embedded as text — which is what
// converting it to a string would do, and it compiles — the same response
// carries one long escaped string, every client has to parse twice, and the
// support desk's parcel list arrives as an opaque blob.
//
// The envelope is the SINGLE one for a reason of its own: an order's shipments
// are bounded by the order and there is no page to ask for, so a paging
// envelope would announce a count, an offset and a limit that mean nothing.
func TestTheOrdersShipmentsArriveAsJSONAndNotAsAStringOfJSON(t *testing.T) {
	flow := &fakeFulfilling{shipments: json.RawMessage(
		`[{"id":"ful_1","status":"shipped","tracking_number":"TR1"}]`)}
	r := newRouterWithFulfilling(&fakeOrders{}, flow)

	rec := doRequest(t, r, http.MethodGet, shipmentsPath, "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 1, flow.listCalls)
	assert.Equal(t, "order_1", flow.gotOrderID)

	payload := decodeResponse(t, rec)
	data, ok := payload["data"].([]any)
	require.True(t, ok,
		"the shipments have to be an array in the response, not a string holding one: %s",
		rec.Body.String())
	require.Len(t, data, 1)

	shipment, ok := data[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "TR1", shipment["tracking_number"],
		"the field the support desk actually asks for has to be reachable without a "+
			"second parse")

	for _, paging := range []string{"count", "offset", "limit"} {
		assert.NotContains(t, payload, paging,
			"an order's shipments are bounded by the order; announcing a page would "+
				"describe a page that does not exist")
	}
}

// TestOpeningAShipmentIsWriteScopedWhileListingIsNot draws the line where the
// damage is.
//
// Listing parcels is what the support desk does all day and it changes nothing.
// Opening one dispatches goods: a label is printed, a carrier collects, and
// there is no endpoint that un-ships a parcel. An identity granted for
// reporting must not be able to do the second, and the read scope is used here
// deliberately rather than an empty one — an empty scope list fails on EVERY
// admin endpoint and would not show that the refusal comes from the read/write
// DISTINCTION.
func TestOpeningAShipmentIsWriteScopedWhileListingIsNot(t *testing.T) {
	flow := &fakeFulfilling{
		fulfillmentID: "ful_1",
		shipments:     json.RawMessage(`[]`),
	}
	r := newRouterWithFulfilling(&fakeOrders{}, flow)

	rec := doRequestAs(t, r, http.MethodPost, shipmentsPath,
		`{"shipping_option_id":"so_1","idempotency_key":"idem_1"}`, readOnlyPrincipal())

	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	assert.Zero(t, flow.openCalls,
		"when the scope is insufficient the flow must not be reached at all; a parcel "+
			"refused after it was opened is still a parcel")

	rec = doRequestAs(t, r, http.MethodGet, shipmentsPath, "", readOnlyPrincipal())

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 1, flow.listCalls,
		"the same identity has to pass on the read, otherwise the 403 above proves only "+
			"that the scope map is too narrow")
}
