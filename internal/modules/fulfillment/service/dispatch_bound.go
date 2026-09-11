package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
)

// CodeLineNotDispatchable is a parcel that would hold more than the order owes.
const CodeLineNotDispatchable = "fulfillment_line_not_dispatchable"

// CodeDispatchBoundUnknown is a parcel whose bound could not be read.
const CodeDispatchBoundUnknown = "fulfillment_dispatch_bound_unknown"

// DispatchBound answers, per order line, how many units a new parcel may hold.
//
// It is the narrow slice of the fulfilling flow this module calls, declared HERE
// with primitive types only: this module cannot import that package, so the
// signature has to be one it can repeat verbatim (ADR 0001/0006).
type DispatchBound interface {
	// DispatchableQuantities answers, per line of the order, how many units are
	// still owed. A line the order does not have is ABSENT from the map.
	DispatchableQuantities(
		ctx context.Context, orderID string, lineItemIDs []string,
	) (map[string]int64, error)
}

// refuseOverDispatch refuses a parcel that would hold more than the order owes.
//
// # What was wrong
//
// `POST /admin/v1/fulfillments` took a line identifier and a quantity and checked
// neither against the order. Not that the line belongs to it, not that the quantity
// is within what was sold, and not that the units had been written off — so a
// parcel could ship goods a customer had already been told were canceled, and the
// stock those units went back to (ADR 0134) went out of the door a second time.
// Nothing failed and nothing was logged. Gap D72.
//
// # Why this module asks instead of knowing
//
// It does not know the order module (Principle 2.1/2.4) and its own record says the
// reference it carries is free text it never validates. So it resolves a flow by
// name at request time — the shape the cart module uses for pricing, where the
// endpoint stays where integrators found it and the cross-module decision lives
// above it.
//
// # It runs BEFORE the transaction, and it fails CLOSED
//
// Before, because asking another module while holding this one's locks takes a
// second connection from the same pool, and enough concurrent parcels would each
// hold one and wait for another (measured for a different write in ADR 0130).
//
// Closed, because a bound that cannot be read is not a bound. An unresolvable flow
// refuses the parcel rather than opening one nobody checked, which is the answer
// the cart module gives when its pricing flow is missing and the answer ADR 0007
// gives for an unconfigured authenticator.
//
// # A retry is not a second parcel
//
// The idempotency key is looked up first. A key that already names a fulfillment is
// a retry: that parcel's units are ALREADY counted as committed, so re-checking the
// bound would refuse the very request that must be answered with the existing
// shipment. The item list of a retry is guarded by the mismatch check the
// transaction already makes.
func (s *Service) refuseOverDispatch(
	ctx context.Context, reference, idempotencyKey string, items []FulfillmentItemInput,
) error {
	if existing, err := s.store.FulfillmentByIdempotencyKey(ctx, idempotencyKey); err == nil &&
		existing.ID != "" {
		return nil
	}

	if s.bound == nil {
		return errors.Internal(CodeDispatchBoundUnknown,
			"the fulfillment module has no way to check what order %s still owes, so a "+
				"parcel cannot be opened; the fulfilling flow is what answers it and it "+
				"was not bound", reference)
	}

	lineIDs := make([]string, 0, len(items))
	for _, item := range items {
		lineIDs = append(lineIDs, item.LineItemID)
	}

	owed, err := s.bound.DispatchableQuantities(ctx, reference, lineIDs)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), CodeDispatchBoundUnknown,
			"what order %s still owes could not be read, so nothing was opened", reference)
	}

	for _, item := range items {
		remaining, onTheOrder := owed[item.LineItemID]
		if !onTheOrder {
			return errors.Invalid(CodeLineNotDispatchable,
				"line %s is not on order %s; a parcel cannot hold goods the order did "+
					"not sell", item.LineItemID, reference)
		}
		if item.Quantity > remaining {
			return errors.Conflict(CodeLineNotDispatchable,
				"line %s of order %s owes %d more unit(s) and the parcel asks for %d; "+
					"what was sold minus what was canceled minus what is already in a "+
					"live parcel is the bound",
				item.LineItemID, reference, remaining, item.Quantity)
		}
	}

	return nil
}
