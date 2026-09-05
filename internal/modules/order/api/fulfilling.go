package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// Fulfilling is the part of the fulfilling flow this module's endpoints call.
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
}

// openShipmentRequest is the body of the open endpoint.
//
// It exists so the OpenAPI document can describe the body. The body itself is
// passed to the flow as raw JSON: this module does not interpret it.
type openShipmentRequest struct {
	// ShippingOptionID is the option the parcel ships on.
	ShippingOptionID string `json:"shipping_option_id"`
	// IdempotencyKey is required. Without one a retried request opens a SECOND
	// parcel for the same order.
	IdempotencyKey string `json:"idempotency_key"`
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
