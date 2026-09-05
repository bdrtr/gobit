package models

// This file is the STATE MACHINE of the payment module.
//
// The transitions stand here as pure, database-free functions; the service
// only turns the result into a typed error. The split is deliberate: whether a
// transition is valid or not is a business rule and must be readable as a
// table, rather than having to be dug out of ifs scattered across three
// separate service methods.

// SessionStatus is the status of a payment session.
//
// The values are ONE FOR ONE the same as
// [github.com/bdrtr/gobit/core/provider.SessionStatus], but that
// package is not reused here: the column value belongs to the module's own
// schema, and the values in the database must not change silently when the
// core contract changes. The translation is done at the repository/service
// boundary.
type SessionStatus string

// Payment session statuses.
const (
	// SessionPending means the session was opened and not authorized yet.
	SessionPending SessionStatus = "pending"
	// SessionAuthorized means the amount was put ON HOLD on the customer; it
	// was not drawn.
	SessionAuthorized SessionStatus = "authorized"
	// SessionCaptured means the amount was captured; a Payment was born out of
	// the session.
	SessionCaptured SessionStatus = "captured"
	// SessionCanceled means the session was closed; the hold, if there was
	// one, was released.
	SessionCanceled SessionStatus = "canceled"
	// SessionFailed means the provider declined the authorization; the reason
	// is in decline_reason.
	SessionFailed SessionStatus = "failed"
)

// String returns the textual form of the status.
func (s SessionStatus) String() string { return string(s) }

// Terminal reports that the session has closed IRREVERSIBLY: a canceled or
// declined session cannot be authorized again and no capture comes out of it
// (see [SessionStatus.AuthorizeAction]).
//
// [SessionCaptured] DOES NOT COUNT as terminal: a captured session is the
// successfully completed form of the flow, and a call repeating the same
// request can read the existing capture out of it. The distinction determines
// in which status a repeat made with the same idempotency key may move ahead.
func (s SessionStatus) Terminal() bool {
	return s == SessionCanceled || s == SessionFailed
}

// Valid reports whether the status is a defined value.
func (s SessionStatus) Valid() bool {
	switch s {
	case SessionPending, SessionAuthorized, SessionCaptured, SessionCanceled, SessionFailed:
		return true
	default:
		return false
	}
}

// SessionAction is the outcome of a session operation in the state machine.
//
// Its zero value is [ActionConflict]; an undefined status is not accidentally
// read as "proceed".
type SessionAction uint8

// The possible outcomes of session operations.
const (
	// ActionConflict means the transition IS INVALID; the service returns
	// errors.Conflict.
	ActionConflict SessionAction = iota
	// ActionProceed means the transition is valid; the provider is called and
	// the status is written.
	ActionProceed
	// ActionNoop means the session is ALREADY in the target status; the
	// provider IS NOT called and no error is returned. This is the branch that
	// provides idempotency.
	ActionNoop
)

// String returns the textual form of the outcome.
func (a SessionAction) String() string {
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

// AuthorizeAction returns the outcome of an authorization request in this
// status.
//
// Transition table (see the note at the top of the file):
//
//	pending    -> proceed   (the provider is called; becomes authorized or failed)
//	authorized -> noop      (already on hold; IT IS NOT put on hold A SECOND TIME)
//	captured   -> conflict  (a captured amount cannot be authorized again)
//	canceled   -> conflict  (a closed session is not reopened)
//	failed     -> conflict  (the decline is FINAL on the provider side; a new session is opened)
func (s SessionStatus) AuthorizeAction() SessionAction {
	switch s {
	case SessionPending:
		return ActionProceed
	case SessionAuthorized:
		return ActionNoop
	case SessionCaptured, SessionCanceled, SessionFailed:
		return ActionConflict
	default:
		return ActionConflict
	}
}

// CaptureAction returns the outcome of a capture request in this status.
//
// Transition table:
//
//	pending    -> conflict  (an authorization is required first)
//	authorized -> proceed   (the hold is drawn; a Payment record is born)
//	captured   -> noop      (a SECOND capture does not come out of the same session)
//	canceled   -> conflict
//	failed     -> conflict
func (s SessionStatus) CaptureAction() SessionAction {
	switch s {
	case SessionAuthorized:
		return ActionProceed
	case SessionCaptured:
		return ActionNoop
	case SessionPending, SessionCanceled, SessionFailed:
		return ActionConflict
	default:
		return ActionConflict
	}
}

// CancelAction returns the outcome of a cancel request in this status.
//
// Cancel IS THE SAGA COMPENSATION and it MUST be idempotent; the only conflict
// branch in the table is the one that cannot be undone.
//
// Transition table:
//
//	pending    -> proceed   (an open session is closed)
//	authorized -> proceed   (the hold is released)
//	captured   -> conflict  (the money has been drawn; the way back is A REFUND)
//	canceled   -> noop      (idempotency is provided right here)
//	failed     -> proceed   (the session is closed; the decline reason IS KEPT in decline_reason)
func (s SessionStatus) CancelAction() SessionAction {
	switch s {
	case SessionPending, SessionAuthorized, SessionFailed:
		return ActionProceed
	case SessionCanceled:
		return ActionNoop
	case SessionCaptured:
		return ActionConflict
	default:
		return ActionConflict
	}
}

// CollectionStatus is the status of a payment collection.
type CollectionStatus string

// Payment collection statuses.
const (
	// CollectionNotPaid means there is not an open session nor a capture yet.
	CollectionNotPaid CollectionStatus = "not_paid"
	// CollectionAwaiting means at least one session is open; the payment is
	// being awaited.
	CollectionAwaiting CollectionStatus = "awaiting"
	// CollectionAuthorized means the collection has been put on hold IN FULL.
	CollectionAuthorized CollectionStatus = "authorized"
	// CollectionPartiallyCaptured means a capture has been made but IT DOES
	// NOT COVER the collection's amount; the payment is short.
	CollectionPartiallyCaptured CollectionStatus = "partially_captured"
	// CollectionCaptured means the collection has been captured IN FULL.
	CollectionCaptured CollectionStatus = "captured"
	// CollectionPartiallyRefunded means part of the capture has been refunded.
	CollectionPartiallyRefunded CollectionStatus = "partially_refunded"
	// CollectionRefunded means the WHOLE of the captured amount has been
	// refunded.
	CollectionRefunded CollectionStatus = "refunded"
	// CollectionCanceled means the sessions have been canceled and there is no
	// capture.
	CollectionCanceled CollectionStatus = "canceled"
)

// String returns the textual form of the status.
func (c CollectionStatus) String() string { return string(c) }

// Valid reports whether the status is a defined value.
func (c CollectionStatus) Valid() bool {
	switch c {
	case CollectionNotPaid, CollectionAwaiting, CollectionAuthorized,
		CollectionPartiallyCaptured, CollectionCaptured,
		CollectionPartiallyRefunded, CollectionRefunded, CollectionCanceled:
		return true
	default:
		return false
	}
}

// SessionCounts is the count of a collection's sessions broken down by status.
type SessionCounts struct {
	// Live are the open sessions (pending or authorized).
	Live int64
	// Canceled are the canceled sessions.
	Canceled int64
	// Failed are the sessions the provider declined.
	Failed int64
	// Total are ALL the sessions that have not been deleted.
	Total int64
}

// CollectionStatusFor DERIVES the collection's status from its amounts and
// from the session counts.
//
// The status is kept in a column, but the source of truth is this function: it
// is recomputed and written after every mutation. The alternative — assigning
// the status by hand in every flow — meant the same rule spread over five
// places and one branch being forgotten; if a collection's status and its
// amounts drift apart, reconciliation becomes impossible.
//
// The ORDER IS MEANINGFUL and runs from the money towards the sessions; the
// money always beats the counts:
//
//  1. If there is a capture, the refund status is looked at first: if all of
//     it was refunded, refunded; if part of it was refunded,
//     partially_refunded.
//  2. If there is no refund, whether the capture COVERS the collection or not
//     is looked at: if all of it was captured, captured; if it is short,
//     partially_captured. Calling a short capture "captured" meant counting an
//     order as paid when 1 unit is drawn out of a collection of 50,000; the
//     very same rule holds in the authorization branch below.
//  3. If the amount put on hold covers the WHOLE of the collection,
//     authorized. A partial authorization is not enough; a collection that is
//     put on hold short is still awaiting payment.
//  4. If there is an open session, awaiting.
//  5. If there is a canceled session, canceled. This is the trace of the saga
//     compensation.
//  6. Otherwise not_paid. A collection that has only DECLINED sessions falls
//     here too: a decline is not final, the customer can try again with a new
//     session, and saying "canceled" would close that door in the wrong way.
func CollectionStatusFor(c PaymentCollection, counts SessionCounts) CollectionStatus {
	if c.CapturedAmount > 0 {
		switch {
		case c.RefundedAmount >= c.CapturedAmount:
			return CollectionRefunded
		case c.RefundedAmount > 0:
			return CollectionPartiallyRefunded
		case c.CapturedAmount >= c.Amount:
			return CollectionCaptured
		default:
			return CollectionPartiallyCaptured
		}
	}
	if c.AuthorizedAmount > 0 && c.AuthorizedAmount >= c.Amount {
		return CollectionAuthorized
	}
	if counts.Live > 0 {
		return CollectionAwaiting
	}
	if counts.Canceled > 0 {
		return CollectionCanceled
	}
	return CollectionNotPaid
}
