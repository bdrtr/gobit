package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// This file is the parcel that brings an order return back (ADR 0384): which
// requests may open one, and what bounds it.

// CodeOptionDirectionMismatch is a parcel whose option and return_id disagree
// about which way it travels.
const CodeOptionDirectionMismatch = "fulfillment_option_direction_mismatch"

// CodeReturnNotAwaited is a parcel naming a return that awaits no goods on the
// order it names.
const CodeReturnNotAwaited = "fulfillment_return_not_awaited"

// refuseWrongDirection refuses a parcel whose option and return_id disagree.
//
// It runs before the bound is asked, because the direction decides which bound
// applies: a return option without a return would be bounded by what the order
// owes, which is the refusal D234 recorded, and a return on an outgoing option
// would be bounded by the return and still leave for the customer's address.
func refuseWrongDirection(option models.ShippingOption, returnID string, items []FulfillmentItemInput) error {
	switch {
	case option.IsReturn && returnID == "":
		return errors.Invalid(CodeOptionDirectionMismatch,
			"option %s is a return option; a parcel on it names the order return it brings "+
				"back (return_id)", option.ID)
	case !option.IsReturn && returnID != "":
		return errors.Invalid(CodeOptionDirectionMismatch,
			"option %s ships to the customer; a parcel bringing return %s back goes on a "+
				"return option", option.ID, returnID)
	case returnID != "" && len(items) == 0:
		return errors.Invalid(CodeInvalidInput,
			"a parcel bringing a return back names the units it carries")
	case returnID != "":
		return checkTextLen("the return id", returnID)
	}

	return nil
}

// refuseOverReturn refuses a parcel that would bring back more than its return
// still awaits.
//
// # The bound
//
// Per line, what the return names less what the return's live parcels already
// hold, and only while the order module says the return awaits its goods. A line
// the return does not name is refused rather than read as unlimited. The order
// module answers "awaits" from its own status rule, so no status word is copied
// here (the D59 class).
//
// # It fails CLOSED, like the outgoing bound
//
// A return that cannot be read is not a bound; an unknown return keeps the kind
// it was reported with, so it answers 404 the way an unknown order does.
func (s *Service) refuseOverReturn(
	ctx context.Context, reference, returnID string, items []FulfillmentItemInput,
) error {
	if s.bound == nil {
		return errors.Internal(CodeDispatchBoundUnknown,
			"the fulfillment module has no way to check what return %s still awaits, so a "+
				"parcel cannot be opened; the fulfilling flow is what answers it and it "+
				"was not bound", returnID)
	}

	awaited, lines, err := s.bound.ReturnLines(ctx, reference, returnID)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), CodeDispatchBoundUnknown,
			"what return %s of order %s still awaits could not be read, so nothing was opened",
			returnID, reference)
	}
	if !awaited {
		return errors.Conflict(CodeReturnNotAwaited,
			"return %s of order %s awaits no goods; it is another order's, received or canceled",
			returnID, reference)
	}

	inbound, err := s.store.ReturningQuantities(ctx, returnID)
	if err != nil {
		return err
	}

	for _, item := range items {
		named, inReturn := lines[item.LineItemID]
		if !inReturn {
			return errors.Invalid(CodeLineNotDispatchable,
				"line %s is not in return %s; a parcel bringing a return back holds only "+
					"what the return names", item.LineItemID, returnID)
		}
		remaining := max(named-inbound[item.LineItemID], 0)
		if item.Quantity > remaining {
			return errors.Conflict(CodeLineNotDispatchable,
				"return %s still awaits %d unit(s) of line %s and the parcel asks for %d; what "+
					"the return names minus what its live parcels hold is the bound",
				returnID, remaining, item.LineItemID, item.Quantity)
		}
	}

	return nil
}
