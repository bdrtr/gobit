package fulfilling

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
)

// CodeParcelUnderway refuses an address correction while a parcel is on its way.
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

	bound, err := w.boundFulfillments(ctx, orderID)
	if err != nil {
		return nil, err
	}
	for fulfillmentID := range bound {
		status, err := w.fulfillments.FulfillmentStatus(ctx, fulfillmentID)
		if err != nil {
			return nil, errors.Wrap(err, errors.KindOf(err), CodeLinkUnreadable,
				"the status of shipment %s could not be read, so order %s's address was not corrected",
				fulfillmentID, orderID)
		}
		if status != statusCanceled && status != statusReturned {
			return nil, errors.Conflict(CodeParcelUnderway,
				"shipment %s of order %s is %s and its carrier has the address it was opened with; "+
					"cancel it or wait for it to come back before correcting the address",
				fulfillmentID, orderID, status)
		}
	}

	return w.orders.CorrectShippingAddressJSON(ctx, orderID, address)
}
