package service

import (
	"context"
	"maps"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
)

// CodeLineNotDispatchable is a parcel that would hold more than the order owes.
const CodeLineNotDispatchable = "fulfillment_line_not_dispatchable"

// CodeDispatchBoundUnknown is a parcel whose bound could not be read.
const CodeDispatchBoundUnknown = "fulfillment_dispatch_bound_unknown"

// DispatchBound answers, per order line, how many units the order may ship.
//
// It is the narrow slice of the fulfilling flow this module calls, declared HERE
// with primitive types only: this module cannot import that package, so the
// signature has to be one it can repeat verbatim (ADR 0001/0006).
type DispatchBound interface {
	// DispatchCeilings answers, per line of the order, how many units it may
	// ship at all: what it sold less what was written off, whatever any parcel
	// holds (ADR 0409). nil lineItemIDs asks for every line, and a line the
	// order does not have is ABSENT from the map.
	//
	// spoken answers, for the same lines, how many units a return or a
	// replacement speaks for, which a parcel that came back undelivered holds
	// its units to (ADR 0423); a line neither names is absent from it.
	DispatchCeilings(
		ctx context.Context, orderID string, lineItemIDs []string,
	) (ceilings, spoken map[string]int64, err error)
	// ReturnLines answers whether order orderID's return returnID still awaits its
	// goods and, per order line it names, how many units it brings back. A return of
	// another order awaits nothing on this one.
	ReturnLines(ctx context.Context, orderID, returnID string) (awaited bool, lines map[string]int64, err error)
}

// refuseOverDispatch refuses a parcel that would hold more than the order may
// ship, and answers the line ceilings it read for an outgoing one.
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
// What it reads is the line's ceiling, what the order may ship at all. What
// its parcels already hold is counted by this module, inside the transaction
// and under the order's lock ([Service.holdToCeiling], ADR 0409): the units a
// parcel may still take are the ceiling less that count, a target both sides
// of which are read the same way, rather than a difference against a bound
// that counts parcels through a link written after their transaction (D265).
//
// # A retry is not a second parcel
//
// It is not asked for a retry at all ([Service.isRetry]).
//
// # A parcel coming back is bounded by its return
//
// A parcel naming a return carries goods the customer sends back, not goods the
// order still owes, so the order's bound would refuse it for every unit already
// shipped (D234). It is bounded by what that return names instead
// ([Service.refuseOverReturn], ADR 0384), and held under the order's lock to
// what the return's live parcels leave of it ([Service.holdToReturn],
// ADR 0420).
func (s *Service) refuseOverDispatch(
	ctx context.Context, reference, returnID string, items []FulfillmentItemInput,
) (ceilings, spoken map[string]int64, err error) {
	if returnID != "" {
		named, err := s.refuseOverReturn(ctx, reference, returnID, items)

		return named, nil, err
	}

	if s.bound == nil {
		return nil, nil, errors.Internal(CodeDispatchBoundUnknown,
			"the fulfillment module has no way to check what order %s may ship, so a "+
				"parcel cannot be opened; the fulfilling flow is what answers it and it "+
				"was not bound", reference)
	}

	// No items asks for every line: a parcel of what the order owes fills its
	// list from them under the lock.
	var lineIDs []string
	for _, item := range items {
		lineIDs = append(lineIDs, item.LineItemID)
	}
	ceilings, spoken, err = s.bound.DispatchCeilings(ctx, reference, lineIDs)
	if err != nil {
		return nil, nil, errors.Wrap(err, errors.KindOf(err), CodeDispatchBoundUnknown,
			"what order %s may ship could not be read, so no parcel took any of it", reference)
	}

	for _, item := range items {
		ceiling, onTheOrder := ceilings[item.LineItemID]
		if !onTheOrder {
			return nil, nil, errors.Invalid(CodeLineNotDispatchable,
				"line %s is not on order %s; a parcel cannot hold goods the order did "+
					"not sell", item.LineItemID, reference)
		}
		if item.Quantity > ceiling {
			return nil, nil, errors.Conflict(CodeLineNotDispatchable,
				"line %s of order %s may ship %d unit(s) at all and the parcel asks for %d; "+
					"what was sold minus what was written off is the ceiling",
				item.LineItemID, reference, ceiling, item.Quantity)
		}
	}
	if ceilings == nil {
		ceilings = map[string]int64{}
	}

	return ceilings, spoken, nil
}

// holdToCeiling holds an outgoing parcel, or the units an addition puts into
// its parent's parcel ([Service.JoinParcel]), to what its order may still
// ship, and fills the list of one asked to hold what is owed (ADR 0409, ADR
// 0428, gaps D264, D265).
//
// It runs inside the transaction, after the order's dispatch lock: the units
// the order's outgoing parcels hold are read there, by the reference each item
// stores, so a parcel committed by the lock's last holder is counted whether
// or not its link to the order is written yet, and so are the units the order
// put into another order's parcel when it joined it. A line may still take
// its ceiling less what they hold; a parcel asking for more is refused, and a
// parcel asked to hold what is owed takes exactly that on every line where it
// is above zero, or is refused when it is zero everywhere. The ceiling is read
// before the transaction, so a write-off landing between that read and this
// one is not seen here (D265).
//
// A parcel that came back undelivered holds its units only as far as a return
// or a replacement speaks for them, spoken (ADR 0423): the rest the order owes
// again, so a parcel may take them. spoken is read with the ceiling, so a
// return or a replacement written or withdrawn after that read is not seen
// here either.
func (s *Service) holdToCeiling(
	ctx context.Context, reference string, items []FulfillmentItemInput, ceilings, spoken map[string]int64,
	owedDefault bool,
) ([]FulfillmentItemInput, error) {
	units, err := s.store.HeldQuantitiesForReference(ctx, reference)
	if err != nil {
		return nil, err
	}
	held := heldOf(units, spoken)

	if owedDefault {
		items = nil
		for _, line := range slices.Sorted(maps.Keys(ceilings)) {
			if left := ceilings[line] - held[line]; left > 0 {
				items = append(items, FulfillmentItemInput{LineItemID: line, Quantity: left})
			}
		}
		if len(items) == 0 {
			return nil, errors.Conflict(CodeNothingOwed,
				"order %s owes no unit to a parcel: what it sold is written off, in a live "+
					"parcel, or came back undelivered and is spoken for by a return or a "+
					"replacement, so no parcel took any of it", reference)
		}

		return normalizeItems(items)
	}

	for _, item := range items {
		if left := ceilings[item.LineItemID] - held[item.LineItemID]; left < item.Quantity {
			return nil, errors.Conflict(CodeLineNotDispatchable,
				"line %s of order %s owes %d more unit(s) and the parcel asks for %d; "+
					"what was sold minus what was written off minus what the order's parcels "+
					"hold is the bound, and a parcel that came back undelivered holds only "+
					"what a return or a replacement speaks for",
				item.LineItemID, reference, max(left, 0), item.Quantity)
		}
	}

	return items, nil
}

// isRetry reports whether the idempotency key already names a fulfillment.
//
// # A retry is not a second parcel
//
// A key that already names a fulfillment is a retry: that parcel's units are
// ALREADY counted as committed, so re-checking the bound would refuse the very
// request that must be answered with the existing shipment. The item list, the
// option and the return of a retry are guarded by the mismatch check the
// transaction already makes.
//
// # The direction is not asked again either
//
// An option's is_return can be changed after a parcel was opened on it, so the
// option's direction today is not the parcel's. A retry is answered from the row
// the key names, whose return_id is the direction it was opened with (ADR 0384).
func (s *Service) isRetry(ctx context.Context, idempotencyKey string) bool {
	existing, err := s.store.FulfillmentByIdempotencyKey(ctx, idempotencyKey)
	return err == nil && existing.ID != ""
}
