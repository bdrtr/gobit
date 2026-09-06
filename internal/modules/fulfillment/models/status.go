package models

// This file is the fulfillment module's STATE MACHINE.
//
// The transitions live here, as pure and database-free functions; the service
// only turns the result into a typed error. The separation is deliberate:
// whether a transition is legal is a business rule and must be readable as a
// table, rather than having to be extracted from ifs scattered across three
// separate service methods (the same separation as in the payment module).
//
// # A transition is a REPORTED FACT, not a command
//
// Every method here except cancellation answers the same question: "the carrier
// says this happened — what does the record become?" The carrier is the
// authority on the physical world and this module is a ledger of what it was
// told. Two consequences follow and both are written into the tables below.
//
// A carrier's events ARRIVE OUT OF ORDER AND REPEAT. "Delivered" reaching us
// before "collected" is not a contradiction — both events are true, one of them
// merely overtook the other on the way here. Refusing the second one does not
// make the parcel un-delivered; it makes the record wrong AND, because the
// reporter is a webhook, it makes the carrier retry the same event forever
// against an endpoint that will never accept it. That is why the table has a
// fourth outcome, [ActionRecord]: a fact that arrived behind the shipment's
// current position is ACCEPTED without moving the status backwards.
//
// The rejected alternative was a SECOND, looser table used only by carrier
// callbacks, leaving these three strict for the admin surface. It was refused
// on the argument this file opens with: the value of a table is that it can be
// read, and two tables that disagree about the same four statuses is the exact
// shape of "extracted from ifs scattered across three service methods" that the
// separation exists to prevent. It also mis-states the admin case. An operator
// reconciling against a carrier's portal by hand is not commanding anything
// either — they are reporting, late, what the portal told them, and the strict
// table forced them to click "ship" first and thereby write a dispatch moment
// that was NEVER MEASURED. The looser table does not remove a guard from the
// operator; it stops requiring them to fabricate a timestamp.

// FulfillmentStatus is the status of a fulfillment.
//
// The values are IDENTICAL to provider.FulfillmentStatus in
// core/provider, but that package is not reused here: the column value
// belongs to the module's own schema, and the values in the database must not
// change silently when the core contract changes. The translation is done at
// the repository/service boundary.
//
// [StatusReturned] has NO counterpart in the core contract, which is the reason
// the two types must stay separate rather than being converted directly: a
// status this module's schema accepts is not automatically a status a provider
// may report (see the service's providerStatus).
type FulfillmentStatus string

// Fulfillment statuses.
const (
	// StatusPending means the fulfillment was created and the carrier has not
	// picked it up yet.
	StatusPending FulfillmentStatus = "pending"
	// StatusShipped means the carrier picked the fulfillment up; it is on its
	// way.
	StatusShipped FulfillmentStatus = "shipped"
	// StatusDelivered means the fulfillment reached the recipient. IT IS
	// IRREVERSIBLE.
	StatusDelivered FulfillmentStatus = "delivered"
	// StatusCanceled means the fulfillment was canceled.
	StatusCanceled FulfillmentStatus = "canceled"
	// StatusReturned means the parcel CAME BACK to the sender without ever
	// being delivered — the Turkish carriers' "iade". It is terminal.
	//
	// # It is not the same thing as a return shipment, and the difference is
	// physical
	//
	// This module already had an answer for a parcel the customer sends BACK
	// after receiving it, and that answer stands: a new fulfillment is opened
	// with a shipping option marked IsReturn (see [FulfillmentStatus.CancelAction]).
	// That is a second shipment — a second waybill, bought and paid for, going
	// the other way.
	//
	// A parcel that was never delivered — the recipient could not be found,
	// refused it, or the address was wrong — comes back under THE ORIGINAL
	// WAYBILL, with the original external identifier, and nobody bought a
	// second label. Modeling it as a new fulfillment would invent a shipment
	// that does not exist and would leave the original one reading "shipped"
	// forever, which is the state the parcel is provably NOT in.
	//
	// The two other candidates were both rejected as untrue rather than as
	// inconvenient. 'canceled' asserts the shipment did not happen, but a label
	// was printed and the parcel traveled twice. 'delivered' asserts the
	// recipient has it, which is the whole point of what did not occur.
	StatusReturned FulfillmentStatus = "returned"
)

// String returns the textual form of the status.
func (s FulfillmentStatus) String() string { return string(s) }

// Valid reports whether the status is a defined value.
func (s FulfillmentStatus) Valid() bool {
	switch s {
	case StatusPending, StatusShipped, StatusDelivered, StatusCanceled, StatusReturned:
		return true
	default:
		return false
	}
}

// Action is the outcome of a fulfillment operation in the state machine.
//
// Its zero value is [ActionConflict]; an undefined status is never accidentally
// read as "go ahead".
type Action uint8

// The possible outcomes of fulfillment operations.
const (
	// ActionConflict means the transition is ILLEGAL; the service returns
	// errors.Conflict.
	ActionConflict Action = iota
	// ActionProceed means the transition is legal; the operation is performed
	// and the status is written.
	ActionProceed
	// ActionNoop means the fulfillment is ALREADY in the target status; the
	// provider is NOT contacted and no error is returned. This is the branch
	// that provides idempotency.
	ActionNoop
	// ActionRecord means the reported fact is BEHIND the shipment's current
	// status: it really happened, it is not a repeat of the current status, and
	// it must not drag the status backwards.
	//
	// It is what a carrier's out-of-order event lands on. "Collected" arriving
	// after "delivered" is the ordinary case — the two events race on the way
	// here and the earlier one loses. Neither [ActionNoop] nor [ActionConflict]
	// fits it: the shipment is not already in that status, and refusing it
	// would leave the webhook retrying an event the endpoint will never accept.
	//
	// What the service does with it is DELIBERATELY less than "write the
	// stamp". The moment a late event carries is the moment it ARRIVED, not the
	// moment it describes, and stamping shipped_at with the clock's "now" after
	// delivery has already been recorded would write a dispatch that happened
	// AFTER its own delivery — a row that reconciliation must read as
	// impossible. A missing stamp says "nobody ever told us when it set out",
	// which is true; a stamp out of order says something false. The service
	// therefore records what the event CARRIES (a tracking number that was not
	// known before) and leaves the unmeasured moment null.
	ActionRecord
)

// String returns the textual form of the outcome.
func (a Action) String() string {
	switch a {
	case ActionProceed:
		return "proceed"
	case ActionNoop:
		return "noop"
	case ActionRecord:
		return "record"
	case ActionConflict:
		return "conflict"
	default:
		return "conflict"
	}
}

// CancelAction returns the outcome of a cancel request in this status.
//
// Cancellation IS THE SAGA COMPENSATION and, per the core contract
// (core/provider), it MUST be IDEMPOTENT; the only conflict branches in
// the table are the ones the physical world has already closed.
//
// It is also the one method here that is a COMMAND rather than a reported fact:
// it asks the carrier to recall a parcel. That is why it does not take the
// [ActionRecord] branch anywhere — there is no such thing as a cancellation
// that arrived late and applies to an earlier moment.
//
// Transition table:
//
//	pending   -> proceed   (the label is canceled)
//	shipped   -> proceed   (the carrier CAN RECALL a fulfillment in transit;
//	                        the authority on whether it can is the provider,
//	                        and if it cannot, Cancel returns an error. Closing
//	                        this off here would force the operator to work
//	                        outside the system.)
//	delivered -> conflict  (delivery HAS HAPPENED; the parcel is in the
//	                        customer's hands and "cancel" would be a lie about
//	                        the physical world. The remedy is a RETURN: a new
//	                        fulfillment is opened with a shipping option marked
//	                        is_return. The rule is the same as a captured
//	                        session in payment not being cancelable but
//	                        refundable.)
//	returned  -> conflict  (the parcel already traveled out and back; there is
//	                        nothing left to recall. The same lie as the
//	                        delivered branch, told about a different journey.)
//	canceled  -> noop      (idempotency is provided here)
func (s FulfillmentStatus) CancelAction() Action {
	switch s {
	case StatusPending, StatusShipped:
		return ActionProceed
	case StatusCanceled:
		return ActionNoop
	case StatusDelivered, StatusReturned:
		return ActionConflict
	default:
		return ActionConflict
	}
}

// ShipAction returns the outcome of a "the carrier collected it" report.
//
// Transition table:
//
//	pending   -> proceed   (the carrier picked it up; shipped_at is written)
//	shipped   -> noop      (the same fulfillment does not set out TWICE)
//	delivered -> record    (the collection event arrived AFTER the delivery
//	                        event. Both are true; only their order on the wire
//	                        was wrong, and this is the single most common thing
//	                        a carrier's webhooks do. Until 2026-09-06 this was
//	                        a conflict, which meant a carrier that reported its
//	                        two events out of order could never be
//	                        acknowledged for the first one.)
//	returned  -> record    (same reason: a parcel cannot come back without
//	                        having set out, so a late collection event on a
//	                        returned shipment is a fact, not a contradiction)
//	canceled  -> conflict  (WE recalled this parcel. A carrier reporting that it
//	                        collected it afterwards contradicts our own record
//	                        rather than merely overtaking it, and an operator
//	                        has to see that.)
func (s FulfillmentStatus) ShipAction() Action {
	switch s {
	case StatusPending:
		return ActionProceed
	case StatusShipped:
		return ActionNoop
	case StatusDelivered, StatusReturned:
		return ActionRecord
	case StatusCanceled:
		return ActionConflict
	default:
		return ActionConflict
	}
}

// DeliverAction returns the outcome of a delivery notification in this status.
//
// Transition table:
//
//	pending   -> proceed   (delivered_at is written and shipped_at is LEFT
//	                        NULL. Until 2026-09-06 this was a conflict, on the
//	                        argument that "skipping the step would leave
//	                        shipped_at empty and reconciliation would have no
//	                        answer for when the fulfillment set out". The
//	                        argument was right about the hole and wrong about
//	                        the remedy: refusing the event does not produce the
//	                        dispatch moment, it discards the delivery too. The
//	                        empty stamp IS the answer — nobody has told us yet —
//	                        and the collection event that overtook it fills it
//	                        in when it lands on the record branch above.)
//	shipped   -> proceed   (delivered_at is written)
//	delivered -> noop      (idempotency is provided here)
//	returned  -> conflict  (the parcel came back to the sender; it cannot also
//	                        be in the recipient's hands. Unlike an out-of-order
//	                        pair, these two events cannot both be true.)
//	canceled  -> conflict  (a canceled fulfillment is not delivered)
func (s FulfillmentStatus) DeliverAction() Action {
	switch s {
	case StatusShipped, StatusPending:
		return ActionProceed
	case StatusDelivered:
		return ActionNoop
	case StatusCanceled, StatusReturned:
		return ActionConflict
	default:
		return ActionConflict
	}
}

// ReturnAction returns the outcome of an "it came back undelivered" report —
// the carriers' "iade".
//
// Transition table:
//
//	pending   -> conflict  (the parcel was never collected, so there is nothing
//	                        that could come back. This one is NOT an
//	                        out-of-order case that the record branch could
//	                        absorb: a return implies a collection, so the pair
//	                        cannot arrive in this order without one of them
//	                        being about a different parcel.)
//	shipped   -> proceed   (returned_at is written; this is the whole point of
//	                        the status)
//	delivered -> conflict  (the recipient HAS the parcel. Sending it back after
//	                        that is a customer return, and this module's answer
//	                        to that is a SECOND fulfillment on an is_return
//	                        option — see [StatusReturned] and
//	                        [FulfillmentStatus.CancelAction].)
//	returned  -> noop      (idempotency is provided here)
//	canceled  -> conflict  (we recalled the parcel ourselves; the carrier
//	                        reporting an undeliverable return contradicts that)
func (s FulfillmentStatus) ReturnAction() Action {
	switch s {
	case StatusShipped:
		return ActionProceed
	case StatusReturned:
		return ActionNoop
	case StatusPending, StatusDelivered, StatusCanceled:
		return ActionConflict
	default:
		return ActionConflict
	}
}
