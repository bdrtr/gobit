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
