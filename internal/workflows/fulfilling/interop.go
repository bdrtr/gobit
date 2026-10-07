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
	// Items are the units the parcel holds; left out, every unit the order
	// still owes, on an order sold one delivery (ADR 0409). A replacement's
	// parcel ignores them.
	Items []OpenItem `json:"items,omitempty"`
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

	out, err := i.w.OpenForOrder(ctx, orderID, body.ShippingOptionID, body.IdempotencyKey, body.Items)
	if err != nil {
		return "", false, err
	}

	return out.FulfillmentID, out.AlreadyOpen, nil
}

// OpenForReplacement opens the parcel an after-sale replacement's goods leave
// in, bound to the order and holding no order line; the rules are
// [Workflows.OpenForReplacement]'s and the request is [Interop.OpenForOrder]'s,
// its items ignored.
//
// It is apart from OpenForOrder so that the order's own route, which an
// operator's request reaches as it was sent, cannot open a parcel the order's
// bound counts nothing in (ADR 0409).
func (i *Interop) OpenForReplacement(
	ctx context.Context, orderID string, request json.RawMessage,
) (fulfillmentID string, alreadyOpen bool, err error) {
	var body interopOpenRequest
	if err := json.Unmarshal(request, &body); err != nil {
		return "", false, errors.Invalid(CodeInvalidInput,
			"the shipment request could not be read: %v", err)
	}

	out, err := i.w.OpenForReplacement(ctx, orderID, body.ShippingOptionID, body.IdempotencyKey)
	if err != nil {
		return "", false, err
	}

	return out.FulfillmentID, out.AlreadyOpen, nil
}

// ShipInParcel lets an order's goods travel in its parent's parcel; the rules
// are [Workflows.ShipInParcel]'s.
func (i *Interop) ShipInParcel(ctx context.Context, orderID, fulfillmentID string) error {
	return i.w.ShipInParcel(ctx, orderID, fulfillmentID)
}

// CorrectShippingAddress corrects where an order ships; the rules are
// [Workflows.CorrectShippingAddress]'s. The address travels as the order
// module's JSON both ways, and readAddressID, the row the caller read or "",
// comes after it so it cannot be swapped with the order's id.
func (i *Interop) CorrectShippingAddress(
	ctx context.Context, orderID string, address json.RawMessage, readAddressID string,
) (json.RawMessage, error) {
	return i.w.CorrectShippingAddress(ctx, orderID, address, readAddressID)
}

// ChangeDelivery puts one of an order's deliveries on another option; the
// rules are [Workflows.ChangeDelivery]'s. quotedAmount is the price the caller
// showed, or nil.
func (i *Interop) ChangeDelivery(
	ctx context.Context, orderID, shippingMethodID, shippingOptionID, collectionID string, quotedAmount *int64,
) (json.RawMessage, error) {
	return i.w.ChangeDelivery(ctx, orderID, shippingMethodID, shippingOptionID, collectionID, quotedAmount)
}

// DeliveryQuoteJSON lists the options the order's deliveries can be put on,
// each as {"id","name","amount"} in the order's currency (ADR 0388); the
// rules are [Workflows.QuoteDelivery]'s.
func (i *Interop) DeliveryQuoteJSON(ctx context.Context, orderID string) (json.RawMessage, error) {
	quotes, err := i.w.QuoteDelivery(ctx, orderID)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(quotes)
	if err != nil {
		return nil, errors.Internal(CodeQuoteFailed, "the quote of order %s could not be encoded", orderID)
	}

	return encoded, nil
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
// still hold, as the fulfillment module counts them; the rules are
// [Workflows.DispatchableQuantities]'s.
//
// # Who asks
//
// The order module's panel surface, to draw its open form (ADR 0409). The
// fulfillment module's create endpoint asks [Interop.DispatchCeilings] instead
// and counts the parcels itself under the order's lock: it has the line
// identifiers and quantities an operator sent and can check neither against the
// order, which it does not know (Principle 2.1/2.4), so it resolves this flow by
// name at request time (ADR 0135).
//
// A line missing from the map is a line the order does not have, and the caller has
// to read that as a refusal rather than as an unlimited quantity.
func (i *Interop) DispatchableQuantities(
	ctx context.Context, orderID string, lineItemIDs []string,
) (map[string]int64, error) {
	return i.w.DispatchableQuantities(ctx, orderID, lineItemIDs)
}

// DispatchCeilings answers, per order line, how many units the order may ship
// at all, whatever any parcel holds; the rules are [Workflows.DispatchCeilings]'s.
// The fulfillment module holds every outgoing parcel to it under the order's
// lock (ADR 0409). spoken is, for the same lines, how many units a return or a
// replacement speaks for (ADR 0423).
func (i *Interop) DispatchCeilings(
	ctx context.Context, orderID string, lineItemIDs []string,
) (ceilings, spoken map[string]int64, err error) {
	return i.w.DispatchCeilings(ctx, orderID, lineItemIDs)
}

// ReturnLines answers whether an order return still awaits its goods and, per
// line, how many units it brings back. The fulfillment module's create endpoint
// asks it before it opens a parcel on a return option (ADR 0384).
func (i *Interop) ReturnLines(
	ctx context.Context, orderID, returnID string,
) (awaited bool, lines map[string]int64, err error) {
	return i.w.ReturnLines(ctx, orderID, returnID)
}
