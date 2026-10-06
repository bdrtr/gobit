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

// refuseOverReturn refuses a parcel that would bring back what its return does
// not name, and answers, per line, what the return names.
//
// # The bound
//
// Per line, what the return names less what the return's live parcels already
// hold, and only while the order module says the return awaits its goods. A line
// the return does not name is refused rather than read as unlimited. The order
// module answers "awaits" from its own status rule, so no status word is copied
// here (the D59 class).
//
// What the return names is read here, before the transaction, and holds no
// parcel; what its live parcels hold is counted inside it, under the order's
// lock ([Service.holdToReturn], ADR 0420): a target both sides of which are read
// the same way, so two parcels opened at once cannot each count the units the
// other is about to take. Whether the return still awaits its goods stays a
// read before the transaction, since asking the order module while holding this
// module's locks takes a second connection from the same pool (ADR 0135).
//
// # It fails CLOSED, like the outgoing bound
//
// A return that cannot be read is not a bound; an unknown return keeps the kind
// it was reported with, so it answers 404 the way an unknown order does.
func (s *Service) refuseOverReturn(
	ctx context.Context, reference, returnID string, items []FulfillmentItemInput,
) (map[string]int64, error) {
	if s.bound == nil {
		return nil, errors.Internal(CodeDispatchBoundUnknown,
			"the fulfillment module has no way to check what return %s still awaits, so a "+
				"parcel cannot be opened; the fulfilling flow is what answers it and it "+
				"was not bound", returnID)
	}

	awaited, lines, err := s.bound.ReturnLines(ctx, reference, returnID)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeDispatchBoundUnknown,
			"what return %s of order %s still awaits could not be read, so nothing was opened",
			returnID, reference)
	}
	if !awaited {
		return nil, errors.Conflict(CodeReturnNotAwaited,
			"return %s of order %s awaits no goods; it is another order's, received or canceled",
			returnID, reference)
	}

	for _, item := range items {
		named, inReturn := lines[item.LineItemID]
		if !inReturn {
			return nil, errors.Invalid(CodeLineNotDispatchable,
				"line %s is not in return %s; a parcel bringing a return back holds only "+
					"what the return names", item.LineItemID, returnID)
		}
		if item.Quantity > named {
			return nil, errors.Conflict(CodeLineNotDispatchable,
				"return %s names %d unit(s) of line %s and the parcel asks for %d",
				returnID, named, item.LineItemID, item.Quantity)
		}
	}
	if lines == nil {
		lines = map[string]int64{}
	}

	return lines, nil
}

// holdToReturn holds a parcel bringing a return back to what the return names
// less what that return's live parcels hold (ADR 0420, gap D265).
//
// It runs inside the transaction, after the order's dispatch lock and after the
// parcel's row is written but before its items are: the parcel being opened is
// not in the count, and a parcel committed by the lock's last holder is. Only
// this return's parcels count; another return of the same order names its own
// units.
func (s *Service) holdToReturn(
	ctx context.Context, reference, returnID string, items []FulfillmentItemInput, named map[string]int64,
) error {
	held, err := s.store.ReturningQuantities(ctx, returnID)
	if err != nil {
		return err
	}

	for _, item := range items {
		if left := named[item.LineItemID] - held[item.LineItemID]; left < item.Quantity {
			return errors.Conflict(CodeLineNotDispatchable,
				"return %s of order %s still awaits %d unit(s) of line %s and the parcel asks "+
					"for %d; what the return names minus what its live parcels hold is the bound",
				returnID, reference, max(left, 0), item.LineItemID, item.Quantity)
		}
	}

	return nil
}
