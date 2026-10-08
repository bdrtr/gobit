package fulfilling

import (
	"context"
	"encoding/json"
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

// codeModuleNotPending is the fulfillment module's refusal of a parcel that is
// no longer pending; on a parcel this flow already read as gone it answers
// with its own [CodeParcelNotWaiting]. It is a literal for [statusCanceled]'s
// reason.
const codeModuleNotPending = "fulfillment_invalid_transition"

// codeModuleItemsRequired is the fulfillment module's refusal of a join that
// names no item where no default is allowed; this flow answers it with its own
// [CodeItemsRequired], the open's code (ADR 0409). It is a literal for
// [statusCanceled]'s reason.
const codeModuleItemsRequired = "fulfillment_items_required"

// ShipInParcel lets an order's goods travel in a parcel of the order it adds
// to (ADR 0197).
//
// The order module answers which order that is, and refuses an addition going
// to another address. This flow holds the parcel's half: the parcel has to be
// bound to that parent. Then the fulfillment module puts the addition's units
// into the parcel as items the addition owns (ADR 0428), and the addition is
// bound to the parcel, beside the order it was opened for.
//
// # The addition's units are the parcel's items
//
// Until ADR 0428 the parcel's items stayed the parent's lines and the
// addition rode itemless, so no count held its units: the order route and the
// panel offered them to a second parcel, and a write-off of its line put boxed
// units back on the shelf (gap D264). Every count of what an order's parcels
// hold sums the items the order owns, so the addition's units now count for
// the addition wherever they travel.
//
// The parcel takes the units an open would: the items named, each within what
// its line still owes, or, when none are named on an addition sold exactly one
// delivery on a shipping option, every unit still owed. Any other addition
// names its items, as an open does, or is refused with [CodeItemsRequired]; a
// unit not named stays owed. The module's conflicts pass through as they are,
// fulfillment_nothing_owed and fulfillment_line_not_dispatchable among them,
// and nothing is bound.
//
// Nothing is sent to the carrier. The parcel's destination is the parent's
// address and the addition's is the same, so the label already printed is the
// label for both; what changes is which orders the parcel answers for.
//
// # The binding comes after the items, and a repeat writes it in any state
//
// The module commits the items first, and the binding follows, as an open's
// does (ADR 0140): a binding with no items behind it would say the addition
// travels in a parcel no count holds its goods in. Only a pending parcel takes
// goods, but a parcel that already holds the addition's items is answered as
// joined whatever its state, so a binding that failed is written by asking
// again even after the parcel left, as an open's is, while both orders are
// pending. The binding re-runs no recount: a cancel or a come-back of the
// parcel recounted before it missed the addition's lines, and the line's next
// act restores its shelf. A second join cannot add units to a parcel that
// already carries the addition's: one naming other units is refused with the
// module's fulfillment_join_items_differ. A canceled parcel
// leaves both orders free to ship in another.
func (w *Workflows) ShipInParcel(ctx context.Context, orderID, fulfillmentID string, items []OpenItem) error {
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

	// No items asks for what the addition owes, which only an addition sold
	// exactly one delivery may, the test an open's default makes (ADR 0409).
	var listed json.RawMessage
	itemsOwed := false
	if len(items) == 0 {
		sold, err := w.orders.ShippingOptionOf(ctx, orderID)
		if err != nil {
			return errors.Wrap(err, errors.KindOf(err), CodeOrderUnreadable,
				"the delivery order %s was sold could not be read", orderID)
		}
		itemsOwed = sold != ""
	} else if listed, err = json.Marshal(items); err != nil {
		return errors.Internal(CodeCreateFailed, "the items joining the parcel could not be encoded: %v", err)
	}

	err = w.fulfillments.JoinParcel(ctx, fulfillmentID, parentID, orderID, listed, itemsOwed)
	switch {
	case err == nil:
	case errors.CodeOf(err) == codeModuleItemsRequired:
		return errors.Invalid(CodeItemsRequired,
			"order %s was not sold exactly one delivery on a shipping option, so it cannot "+
				"default to every unit it owes; name the items it puts in parcel %s", orderID, fulfillmentID)
	case errors.CodeOf(err) == codeModuleNotPending && status != statusPending:
		return errors.Conflict(CodeParcelNotWaiting,
			"parcel %s is %s; only a pending parcel takes more goods", fulfillmentID, status)
	case errors.IsConflict(err):
		return err
	default:
		return errors.Wrap(err, errors.KindOf(err), CodeCreateFailed,
			"the goods of order %s could not be put into parcel %s", orderID, fulfillmentID)
	}

	if err := w.links.Create(ctx, LinkOrderFulfillment, orderID, fulfillmentID); err != nil {
		return errors.Wrap(err, errors.KindOf(err), CodeLinkFailed,
			"order %s's goods are in parcel %s and the binding between them could not be "+
				"written; asking again while both orders are pending binds them, whatever the "+
				"parcel's state, but a cancel or a come-back of the parcel recounted before then "+
				"is not recounted, and the line's next act restores its shelf", orderID, fulfillmentID)
	}

	return nil
}
