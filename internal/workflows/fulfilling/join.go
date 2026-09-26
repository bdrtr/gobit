package fulfilling

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
)

// The refusals of an addition joining its parent's parcel (ADR 0197).
const (
	// CodeParcelNotParents refuses a parcel that is not one of the parent's.
	CodeParcelNotParents = "fulfilling_parcel_not_parents"
	// CodeParcelNotWaiting refuses a parcel that is no longer pending.
	CodeParcelNotWaiting = "fulfilling_parcel_not_waiting"
)

// statusPending is the fulfillment module's word for a parcel that has not left.
// It is a literal for [statusCanceled]'s reason.
const statusPending = "pending"

// ShipInParcel lets an order's goods travel in a parcel of the order it adds
// to (ADR 0197).
//
// The order module answers which order that is, and refuses an addition going
// to another address. This flow holds the parcel's half: the parcel has to be
// bound to that parent, and pending — a parcel on its way has closed, and a
// canceled or returned one carries nothing. Then the addition is bound to the
// parcel, beside the order it was opened for.
//
// Nothing is sent to the carrier. The parcel's destination is the parent's
// address and the addition's is the same, so the label already printed is the
// label for both; what changes is which orders the parcel answers for. Binding
// the same pair twice is a no-op, so the call can be repeated.
//
// The parcel's items stay the parent's lines, and the addition's goods ride
// with it as an itemless share. A canceled parcel leaves both orders free to
// ship in another.
func (w *Workflows) ShipInParcel(ctx context.Context, orderID, fulfillmentID string) error {
	switch {
	case strings.TrimSpace(orderID) == "":
		return errors.Invalid(CodeInvalidInput, "the order id is required")
	case strings.TrimSpace(fulfillmentID) == "":
		return errors.Invalid(CodeInvalidInput, "the parcel id is required")
	}

	parentID, err := w.orders.ShippingParentOf(ctx, orderID)
	if err != nil {
		return err
	}

	bound, err := w.boundFulfillments(ctx, parentID)
	if err != nil {
		return err
	}
	if !bound[fulfillmentID] {
		return errors.Conflict(CodeParcelNotParents,
			"parcel %s is not a parcel of order %s, which order %s adds to",
			fulfillmentID, parentID, orderID)
	}

	status, err := w.fulfillments.FulfillmentStatus(ctx, fulfillmentID)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), CodeLinkUnreadable,
			"the status of parcel %s could not be read, so order %s was not added to it",
			fulfillmentID, orderID)
	}
	if status != statusPending {
		return errors.Conflict(CodeParcelNotWaiting,
			"parcel %s is %s; only a pending parcel takes more goods", fulfillmentID, status)
	}

	if err := w.links.Create(ctx, LinkOrderFulfillment, orderID, fulfillmentID); err != nil {
		return errors.Wrap(err, errors.KindOf(err), CodeLinkFailed,
			"order %s could not be bound to parcel %s", orderID, fulfillmentID)
	}

	return nil
}
