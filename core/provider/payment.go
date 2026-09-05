package provider

import (
	"context"
	"encoding/json"
)

// SessionStatus is a payment session's status on the provider side.
type SessionStatus string

// The payment session statuses.
const (
	// SessionPending means the session was opened but not authorized yet.
	SessionPending SessionStatus = "pending"
	// SessionAuthorized means the amount is HELD against the customer; it has
	// not been taken yet.
	SessionAuthorized SessionStatus = "authorized"
	// SessionCaptured means the amount was collected.
	SessionCaptured SessionStatus = "captured"
	// SessionCanceled means the session was canceled; any hold was released.
	SessionCanceled SessionStatus = "canceled"
	// SessionFailed means the provider rejected the operation.
	SessionFailed SessionStatus = "failed"
)

// CreateSessionInput is the input of a new payment session.
type CreateSessionInput struct {
	// Amount is the amount to collect, an INTEGER in minor units (plan
	// Section 8).
	Amount int64
	// CurrencyCode is the ISO 4217 currency code.
	CurrencyCode string
	// Reference is the identity the caller gave its own record (e.g. the
	// payment collection's id). The provider stores it on its side; it is the
	// field that matches the two systems during reconciliation.
	Reference string
	// IdempotencyKey stops the same session from being opened twice.
	//
	// A saga may retry a step (plan Section 2.6); without the key a retry
	// would mean a SECOND attempt to charge the customer.
	IdempotencyKey string
	// Data is provider-specific free-form data (a card token, a return URL and
	// so on).
	Data map[string]any
}

// Session is a payment session opened at the provider.
type Session struct {
	// ID is the session's identity on the provider side.
	ID string
	// Status is the session's current status.
	Status SessionStatus
	// Amount and CurrencyCode are the session's amount.
	Amount       int64
	CurrencyCode string
	// Data is the raw data returned by the provider (e.g. the client_secret
	// the client will use). It is stored as it is; the core does not interpret
	// it.
	Data json.RawMessage
}

// AuthResult is the outcome of an authorization attempt.
type AuthResult struct {
	// Status is the session status after authorization.
	Status SessionStatus
	// AuthorizedAmount is the amount held; on a partial authorization it may
	// be smaller than the one requested.
	AuthorizedAmount int64
	// Data is the raw data returned by the provider.
	Data json.RawMessage
	// DeclineReason is the provider-side reason for the refusal when Status is
	// SessionFailed. It is for diagnosis, NOT to be shown to the customer.
	DeclineReason string
}

// PaymentProvider is the contract a payment provider offers the core (plan
// Section 5.6).
//
// # Idempotency and the saga
//
// This interface's methods are called from saga steps and a saga MAY RETRY a
// step. Therefore:
//   - CreateSession, called a second time with the same IdempotencyKey, does
//     NOT open a new session; it returns the existing one.
//   - Authorize, Capture and Refund must be callable again on the same
//     session; a second call must return the current state, NOT an error.
//
// The compensation path is Cancel and, as the saga requires, it must be
// IDEMPOTENT: a session canceled twice must not fail on the second call.
type PaymentProvider interface {
	Provider

	// CreateSession opens a payment session at the provider.
	CreateSession(ctx context.Context, in CreateSessionInput) (Session, error)

	// Authorize HOLDS the amount against the customer; it collects nothing.
	//
	// The distinction is deliberate: the saga moves to collection after
	// creating the order, and if a step in between fails, releasing a hold is
	// both faster and less irreversible than refunding an amount already
	// taken.
	Authorize(ctx context.Context, sessionID string) (AuthResult, error)

	// Capture collects the held amount. amount CANNOT be larger than the
	// authorized amount; given zero, the whole amount is taken.
	Capture(ctx context.Context, sessionID string, amount int64) error

	// Refund returns a collected amount. With amount zero the whole amount is
	// refunded.
	Refund(ctx context.Context, sessionID string, amount int64) error

	// Cancel cancels a session that was authorized but NOT captured and
	// releases the hold. This is the saga's compensation; it must be
	// IDEMPOTENT.
	Cancel(ctx context.Context, sessionID string) error
}

// SessionInspection is what a provider says about a session in ITS OWN ledger.
//
// It is deliberately a SNAPSHOT of amounts rather than a history: what
// reconciliation compares is "how much does each side think was taken", and a
// history would invite a second, richer definition of the same question living
// in a background job.
type SessionInspection struct {
	// Status is the provider's own view of the session.
	Status SessionStatus
	// AuthorizedAmount, CapturedAmount and RefundedAmount are minor-unit
	// integers as the PROVIDER holds them.
	AuthorizedAmount int64
	CapturedAmount   int64
	RefundedAmount   int64
}

// SessionInspector is an OPTIONAL capability: a provider that can be asked what
// its own ledger says about a session.
//
// # Why this exists
//
// The payment module makes the provider call inside its own database
// transaction. If that transaction blows up AFTER the provider took the money,
// the rollback leaves the money gone and NO trace of it locally — the saga then
// reads the local collection, sees no capture and rolls the order back. The
// checkout workflow's documentation names this as the one risk it narrows but
// cannot close, and names the only correct closure: ask the provider.
//
// # Why OPTIONAL rather than a method on PaymentProvider
//
// Adding a method to [PaymentProvider] would make every provider change in
// order for one of them to gain a capability, which is the inversion this
// package's own documentation exists to prevent. It would also force a provider
// that genuinely CANNOT answer to implement something that lies.
//
// A provider that does not implement this is not broken; it is simply
// unreconcilable, and whatever asks must SAY SO rather than treat silence as
// agreement. That distinction is the whole value of making it optional: "the
// two ledgers agree" and "nobody asked" must never look the same.
//
// # It is a READ
//
// An inspector must not move money, must not change the provider's state and
// must be safe to call repeatedly. What is done with a divergence it reveals is
// a decision for a human, not for the caller.
type SessionInspector interface {
	PaymentProvider

	// InspectSession returns the provider's own view of a session, addressed by
	// the identifier the provider gave it ([Session.ID], stored locally as the
	// session's external id).
	//
	// A session the provider has never heard of returns a NotFound error rather
	// than a zero inspection: "the provider has no such session" and "the
	// provider says nothing was taken" are different facts, and treating the
	// first as the second would hide a session opened against the wrong
	// account.
	InspectSession(ctx context.Context, sessionID string) (SessionInspection, error)
}
