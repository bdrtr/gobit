package models

// This file is the fulfillment module's STATE MACHINE.
//
// The transitions live here, as pure and database-free functions; the service
// only turns the result into a typed error. The separation is deliberate:
// whether a transition is legal is a business rule and must be readable as a
// table, rather than having to be extracted from ifs scattered across three
// separate service methods (the same separation as in the payment module).

// FulfillmentStatus is the status of a fulfillment.
//
// The values are IDENTICAL to provider.FulfillmentStatus in
// internal/core/provider, but that package is not reused here: the column value
// belongs to the module's own schema, and the values in the database must not
// change silently when the core contract changes. The translation is done at
// the repository/service boundary.
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
)

// String returns the textual form of the status.
func (s FulfillmentStatus) String() string { return string(s) }

// Valid reports whether the status is a defined value.
func (s FulfillmentStatus) Valid() bool {
	switch s {
	case StatusPending, StatusShipped, StatusDelivered, StatusCanceled:
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
)

// String returns the textual form of the outcome.
func (a Action) String() string {
	switch a {
	case ActionProceed:
		return "proceed"
	case ActionNoop:
		return "noop"
	case ActionConflict:
		return "conflict"
	default:
		return "conflict"
	}
}

// CancelAction returns the outcome of a cancel request in this status.
//
// Cancellation IS THE SAGA COMPENSATION and, per the core contract
// (internal/core/provider), it MUST be IDEMPOTENT; the only conflict branch in
// the table is the irreversible one.
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
//	canceled  -> noop      (idempotency is provided here)
func (s FulfillmentStatus) CancelAction() Action {
	switch s {
	case StatusPending, StatusShipped:
		return ActionProceed
	case StatusCanceled:
		return ActionNoop
	case StatusDelivered:
		return ActionConflict
	default:
		return ActionConflict
	}
}

// ShipAction returns the outcome of a ship request in this status.
//
// Transition table:
//
//	pending   -> proceed   (the carrier picked it up; shipped_at is written)
//	shipped   -> noop      (the same fulfillment does not set out TWICE)
//	delivered -> conflict  (a delivered fulfillment does not go back to "in
//	                        transit")
//	canceled  -> conflict  (a canceled fulfillment does not set out)
func (s FulfillmentStatus) ShipAction() Action {
	switch s {
	case StatusPending:
		return ActionProceed
	case StatusShipped:
		return ActionNoop
	case StatusDelivered, StatusCanceled:
		return ActionConflict
	default:
		return ActionConflict
	}
}

// DeliverAction returns the outcome of a delivery notification in this status.
//
// Transition table:
//
//	pending   -> conflict  (a fulfillment that was never picked up CANNOT be
//	                        delivered; skipping the step would leave shipped_at
//	                        empty and reconciliation would have no answer for
//	                        when the fulfillment set out)
//	shipped   -> proceed   (delivered_at is written)
//	delivered -> noop      (idempotency is provided here)
//	canceled  -> conflict  (a canceled fulfillment is not delivered)
func (s FulfillmentStatus) DeliverAction() Action {
	switch s {
	case StatusShipped:
		return ActionProceed
	case StatusDelivered:
		return ActionNoop
	case StatusPending, StatusCanceled:
		return ActionConflict
	default:
		return ActionConflict
	}
}
