package fulfilling

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
)

// InteropName is the name of the fulfilling flow in the container (ADR 0006).
//
// The order module's API resolves it BY NAME at request time: the flow is born
// after every module has registered, while the handler is built during
// registration, and deferring the resolution is how that circle is broken.
const InteropName = "workflows.fulfilling.interop"

// Interop is the flow's cross-module surface.
//
// It carries only PRIMITIVE and stdlib types, so a consumer can declare the
// interface on its own side without importing this package (ADR 0001/0006).
type Interop struct {
	w *Workflows
}

// NewInterop builds the surface over the given flow.
func NewInterop(w *Workflows) *Interop { return &Interop{w: w} }

// interopOpenRequest is the body [Interop.OpenForOrder] accepts.
//
// The fields travel as JSON rather than as positional strings for the reason
// the invoicing surface gives: two identifiers of the same type next to each
// other in a signature are two a caller swaps silently.
type interopOpenRequest struct {
	// ShippingOptionID is the option the parcel ships on.
	ShippingOptionID string `json:"shipping_option_id"`
	// IdempotencyKey is required. Without one a retry opens a SECOND parcel.
	IdempotencyKey string `json:"idempotency_key"`
}

// OpenForOrder opens a shipment for an order and binds the two.
//
// alreadyOpen being true means the idempotency key had already opened this
// shipment and nothing new was created. It crosses as its own value rather than
// being inferred, because the two outcomes are identical to a caller that only
// reads the id.
func (i *Interop) OpenForOrder(
	ctx context.Context, orderID string, request json.RawMessage,
) (fulfillmentID string, alreadyOpen bool, err error) {
	var body interopOpenRequest
	if err := json.Unmarshal(request, &body); err != nil {
		return "", false, errors.Invalid(CodeInvalidInput,
			"the shipment request could not be read: %v", err)
	}

	out, err := i.w.OpenForOrder(ctx, orderID, body.ShippingOptionID, body.IdempotencyKey)
	if err != nil {
		return "", false, err
	}

	return out.FulfillmentID, out.AlreadyOpen, nil
}

// ShipmentsOfOrderJSON lists the shipments bound to an order.
//
// It answers with identities and statuses rather than with the shipments: a
// client that wants a parcel's detail reads it from the fulfillment module's
// own endpoint, where its shape already lives.
func (i *Interop) ShipmentsOfOrderJSON(
	ctx context.Context, orderID string,
) (json.RawMessage, error) {
	shipments, err := i.w.ShipmentsOfOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}

	out := make([]interopShipment, 0, len(shipments))
	for _, shipment := range shipments {
		out = append(out, interopShipment(shipment))
	}

	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, errors.Internal(CodeInvalidInput,
			"the shipments of order %s could not be encoded", orderID)
	}

	return encoded, nil
}

// interopShipment is one shipment as it crosses the surface.
type interopShipment struct {
	FulfillmentID string `json:"fulfillment_id"`
	// Status is empty when the fulfillment module could not be asked; the
	// binding is still a fact and is still reported.
	Status string `json:"status"`
}

// DispatchableQuantities answers, per order line, how many units a NEW parcel may
// still hold.
//
// # Who asks, and why it is not the caller's own arithmetic
//
// The fulfillment module's create endpoint asks, before it opens anything. It has
// the line identifiers and the quantities an operator sent and can check neither
// against the order — it does not know this one (Principle 2.1/2.4). So it resolves
// this flow by name at request time, the way the cart module resolves its pricing
// flow: the endpoint stays where integrators found it and the cross-module decision
// lives above it (ADR 0135).
//
// A line missing from the map is a line the order does not have, and the caller has
// to read that as a refusal rather than as an unlimited quantity.
func (i *Interop) DispatchableQuantities(
	ctx context.Context, orderID string, lineItemIDs []string,
) (map[string]int64, error) {
	return i.w.DispatchableQuantities(ctx, orderID, lineItemIDs)
}
