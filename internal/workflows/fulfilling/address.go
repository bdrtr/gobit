package fulfilling

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
)

// CodeParcelUnderway refuses an address correction or a delivery change while
// a parcel is on its way.
const CodeParcelUnderway = "fulfilling_parcel_underway"

// statusReturned is the fulfillment module's word for a parcel that came back.
// It is a literal for [statusCanceled]'s reason.
const statusReturned = "returned"

// CorrectShippingAddress corrects where an order ships, and returns the address
// that is current afterwards (ADR 0195).
//
// A carrier was handed the destination when a parcel was opened (ADR 0194), and
// a correction written afterwards would not reach the label. So the correction
// is refused while any parcel of the order is pending, shipped or delivered. A
// canceled parcel never left, and a returned one came back — the address that
// sent it back is the one being corrected — so neither stands in the way.
//
// # The window this does NOT close
//
// The parcels are read, and then the order is written. A parcel opened between
// the two reads the old address. There is no transaction spanning the
// fulfillment and order modules (ADR 0001), and closing it would take a lock
// both flows share. It needs two operators acting on one order at the same
// instant, and the parcel it produces carries the address the order held when
// it was opened, which the timeline dates.
func (w *Workflows) CorrectShippingAddress(
	ctx context.Context, orderID string, address json.RawMessage,
) (json.RawMessage, error) {
	if strings.TrimSpace(orderID) == "" {
		return nil, errors.Invalid(CodeInvalidInput, "the order id is required")
	}

	if err := w.refuseWhileUnderway(ctx, orderID, "the address it was opened with", "correcting the address"); err != nil {
		return nil, err
	}

	return w.orders.CorrectShippingAddressJSON(ctx, orderID, address)
}

// refuseWhileUnderway refuses while any parcel of the order is pending,
// shipped or delivered: its carrier holds what the parcel was opened with.
// held names what that is, and act what is being refused.
func (w *Workflows) refuseWhileUnderway(ctx context.Context, orderID, held, act string) error {
	bound, err := w.boundFulfillments(ctx, orderID)
	if err != nil {
		return err
	}
	for fulfillmentID := range bound {
		status, err := w.fulfillments.FulfillmentStatus(ctx, fulfillmentID)
		if err != nil {
			return errors.Wrap(err, errors.KindOf(err), CodeLinkUnreadable,
				"the status of shipment %s could not be read, so order %s was not changed",
				fulfillmentID, orderID)
		}
		if status != statusCanceled && status != statusReturned {
			return errors.Conflict(CodeParcelUnderway,
				"shipment %s of order %s is %s and its carrier has %s; "+
					"cancel it or wait for it to come back before %s",
				fulfillmentID, orderID, status, held, act)
		}
	}

	return nil
}
