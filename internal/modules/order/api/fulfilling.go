package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// Fulfilling is the part of the fulfilling flow this module's endpoints and
// its panel surface call.
//
// It is declared HERE, on the consumer's side, and carries only primitives and
// JSON: the flow lives in internal/workflows and this module cannot import it
// (ADR 0006 holds in both directions).
//
// # Why the endpoints are on the ORDER and not on the shipment
//
// "Ship this order" is a question asked about an order, and the operator asking
// it is holding an order id. The fulfillment module's own create endpoint takes
// a free-text reference it never validates and knows nothing about orders;
// putting an order id into it would give that module a fact about another
// module for the sake of a URL — and it would still leave nothing able to
// answer which order a parcel belongs to.
type Fulfilling interface {
	// OpenForOrder opens a shipment for the order and binds the two.
	//
	// alreadyOpen being true means the idempotency key had already opened this
	// shipment and nothing new was created. It is reported rather than
	// inferred, because an operator who pressed the button twice has to be told
	// the second press did nothing — the alternative is two labels for one
	// parcel, discovered at the carrier.
	OpenForOrder(ctx context.Context, orderID string, request json.RawMessage) (
		fulfillmentID string, alreadyOpen bool, err error,
	)

	// ShipmentsOfOrderJSON lists the shipments bound to the order.
	ShipmentsOfOrderJSON(ctx context.Context, orderID string) (json.RawMessage, error)

	// CorrectShippingAddress corrects where the order ships and returns the
	// address that is current afterwards (ADR 0195). It lives on the flow
	// because the one question this module cannot answer — is a parcel already
	// on its way — is the flow's. readAddressID is the shipping address row
	// the caller drew the correction from, refused when the order holds
	// another; the API names none (ADR 0388).
	CorrectShippingAddress(
		ctx context.Context, orderID string, address json.RawMessage, readAddressID string,
	) (json.RawMessage, error)

	// ShipInParcel lets the order's goods travel in a parcel of the order it
	// adds to (ADR 0197). request is {"items":[...]}, the units it puts in,
	// or empty for every unit it owes on an addition sold one delivery
	// (ADR 0428).
	ShipInParcel(ctx context.Context, orderID, fulfillmentID string, request json.RawMessage) error

	// ChangeDelivery puts one of the order's deliveries on another shipping
	// option at the price the fulfillment module quotes for the order
	// (ADR 0199), a dearer one paid by the named collection (ADR 0200). It
	// lives on the flow because the quote, the parcels and the collection are
	// the flow's to read. quotedAmount is the price the caller showed, refused
	// when the quote moved; the API names none (ADR 0388).
	ChangeDelivery(
		ctx context.Context, orderID, shippingMethodID, shippingOptionID, collectionID string, quotedAmount *int64,
	) (json.RawMessage, error)

	// DeliveryQuoteJSON lists the options the order's deliveries can be put
	// on at the price a change would write, as [{"id","name","amount"}]
	// (ADR 0388).
	DeliveryQuoteJSON(ctx context.Context, orderID string) (json.RawMessage, error)

	// DispatchableQuantities answers, per line of the order, how many units a
	// new parcel may still hold; nil lineItemIDs asks for every line. The
	// panel's open form is drawn with it (ADR 0409).
	DispatchableQuantities(ctx context.Context, orderID string, lineItemIDs []string) (map[string]int64, error)
}

// changeDeliveryRequest is the body of the delivery change endpoint.
type changeDeliveryRequest struct {
	// ShippingOptionID is the option the delivery goes on. Its price is the
	// fulfillment module's quote for the order, not the caller's.
	ShippingOptionID string `json:"shipping_option_id"`
	// PaymentCollectionID is the collection that took the difference of a
	// dearer option: opened for the order, for exactly the difference, and
	// captured (ADR 0200). A change that costs no more names none.
	PaymentCollectionID string `json:"payment_collection_id,omitempty"`
}

// openShipmentRequest is the body of the open endpoint.
//
// It exists so the OpenAPI document can describe the body. The body itself is
// passed to the flow as raw JSON: this module does not interpret it.
type openShipmentRequest struct {
	// ShippingOptionID is the option the parcel ships on. Left empty, it is the
	// option of the one delivery the order was sold, as it stands after its
	// changes (ADR 0198, 0199).
	ShippingOptionID string `json:"shipping_option_id"`
	// IdempotencyKey is required. Without one a retried request opens a SECOND
	// parcel for the same order.
	IdempotencyKey string `json:"idempotency_key"`
	// Items are the units the parcel holds. Left out on an order sold one
	// delivery, the parcel holds every unit still owed to a parcel (ADR 0409).
	Items []openShipmentItem `json:"items,omitempty"`
}

// openShipmentItem is one line of the units a parcel holds.
type openShipmentItem struct {
	// LineItemID is the order line.
	LineItemID string `json:"line_item_id"`
	// Quantity is how many of its units the parcel holds.
	Quantity int64 `json:"quantity"`
}

// shipmentOpenedDTO is what the open endpoint answers.
type shipmentOpenedDTO struct {
	// FulfillmentID is the shipment's identifier.
	FulfillmentID string `json:"fulfillment_id"`
	// AlreadyOpen reports that nothing new was created.
	AlreadyOpen bool `json:"already_open"`
}

// adminOpenShipment POST /admin/v1/orders/{id}/fulfillments
func (h *Handler) adminOpenShipment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
			"the request body could not be read"))

		return
	}
	if len(body) == 0 {
		// The flow needs a shipping option and an idempotency key; an empty
		// body cannot carry them, and refusing here says so rather than letting
		// the flow report a missing option the caller never sent.
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
			"the request body cannot be empty; it carries the shipping option and the "+
				"idempotency key"))

		return
	}

	flow, err := h.fulfillingFlow()
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	fulfillmentID, already, err := flow.OpenForOrder(ctx, orderID(r), body)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	response := singleEnvelope{Data: shipmentOpenedDTO{
		FulfillmentID: fulfillmentID,
		AlreadyOpen:   already,
	}}

	// The two codes are written at two call sites rather than through a
	// variable: the repository's error-path audit resolves a status only when it
	// is a constant at the call, and a status it cannot resolve is one nobody
	// can prove bypasses the core's error writer.
	if already {
		corehttp.WriteJSON(ctx, w, http.StatusOK, response)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusCreated, response)
}

// adminCorrectShippingAddress PUT /admin/v1/orders/{id}/shipping-address
//
// The body is the whole corrected address, in the order's address schema, and
// it is passed through to the flow as raw JSON; the order module decodes it
// and refuses a field it does not know. The answer is the admin order record,
// so the operator reads the address the order now holds (ADR 0195).
func (h *Handler) adminCorrectShippingAddress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
			"the request body could not be read"))

		return
	}
	if len(bytes.TrimSpace(body)) == 0 {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
			"the request body cannot be empty; it carries the corrected address"))

		return
	}

	flow, err := h.fulfillingFlow()
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	if _, err := flow.CorrectShippingAddress(ctx, orderID(r), body, ""); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	h.writeCurrentOrder(w, r)
}

// joinParcelRequest is the body of a join: the units the addition puts into
// the parcel, in the open's shape (ADR 0428).
type joinParcelRequest struct {
	// Items are the units the parcel takes. Left out on an addition sold one
	// delivery, it takes every unit still owed to a parcel.
	Items []openShipmentItem `json:"items,omitempty"`
}

// adminShipInParcel PUT /admin/v1/orders/{id}/fulfillments/{fulfillmentId}
//
// It binds the order, an addition, to a pending parcel of the order it adds to,
// and answers with the order's shipments — the parcel among them. The path
// names both records and the body, which may be left out, names the units the
// parcel takes (ADR 0428); the call can be repeated (ADR 0197).
func (h *Handler) adminShipInParcel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body joinParcelRequest
	if err := decodeOptionalBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	request, err := json.Marshal(body)
	if err != nil {
		corehttp.WriteError(ctx, w, coreerrors.Internal(codeInvalidRequest,
			"the join request could not be encoded"))

		return
	}

	flow, err := h.fulfillingFlow()
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	if err := flow.ShipInParcel(ctx, orderID(r), chi.URLParam(r, paramFulfillmentID), request); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	h.adminListShipments(w, r)
}

// adminChangeDelivery PUT /admin/v1/orders/{id}/shipping-methods/{shippingMethodId}
//
// It puts the delivery on the option in the body and answers with the admin
// order record, whose shipping method now lists the change. Putting it on the
// option it is already on writes nothing (ADR 0199).
func (h *Handler) adminChangeDelivery(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body changeDeliveryRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	flow, err := h.fulfillingFlow()
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	if _, err := flow.ChangeDelivery(ctx, orderID(r), chi.URLParam(r, paramShippingMethodID),
		body.ShippingOptionID, body.PaymentCollectionID, nil); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	h.writeCurrentOrder(w, r)
}

// adminListShipments GET /admin/v1/orders/{id}/fulfillments
//
// This is the read the support desk asks for first — "where is the parcel" —
// and until the binding existed it had no answer at all.
func (h *Handler) adminListShipments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	flow, err := h.fulfillingFlow()
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	shipments, err := flow.ShipmentsOfOrderJSON(ctx, orderID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	// The single envelope, not the paged one: an order's shipments are bounded
	// by the order and there is no page to ask for. Filling a paging envelope
	// with zeros would announce a count, an offset and a limit that nothing
	// means.
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: shipments})
}
