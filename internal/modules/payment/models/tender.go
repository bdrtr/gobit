package models

import "time"

// This file carries what the module's own tenders share (ADR 0165): a session
// record for a provider that spends a balance this module keeps, and the name
// of the tender that spends points.

// LoyaltyTenderID is the identity of the provider that spends loyalty points.
//
// It lives beside the ledger rather than in the provider's package because two
// parties need the name: the provider answers to it, and the earn path excludes
// money captured through it — a capture paid with points earns nothing, or a
// point would earn itself back at the ceiling rate (ADR 0165).
const LoyaltyTenderID = "loyalty_points"

// TenderSession is a balance tender's OWN view of a payment session.
//
// Store credit and loyalty points are one state machine over two ledgers
// (ADR 0165), so they keep one session record in two tables. It mirrors
// [ManualSession] because the state machine belongs to the core contract rather
// than to any provider, and it is a separate table from the module's
// payment_sessions for the same reason the manual provider's is: the module
// reaches a provider only through the contract.
type TenderSession struct {
	// ID is the PROVIDER identifier ("scrses_" or "lpses_" prefixed); it sits on
	// the module's session record as ExternalID.
	ID string
	// IdempotencyKey prevents the same session from being opened twice.
	IdempotencyKey string
	// Reference is the payment collection's identifier.
	Reference string
	// CustomerID is whose balance this session spends.
	CustomerID string
	// Amount is the session's amount and CurrencyCode its currency. The amount
	// is in the ledger's unit, which for both tenders is the currency's minor
	// unit: a point is worth one of them (ADR 0165).
	Amount       int64
	CurrencyCode string
	// Status is the session's status on the provider side.
	Status SessionStatus
	// AuthorizedAmount, CapturedAmount and RefundedAmount are the provider's own
	// amounts.
	AuthorizedAmount int64
	CapturedAmount   int64
	RefundedAmount   int64
	// DeclineReason is why the provider refused; it is for diagnosis and is not
	// shown to a customer.
	DeclineReason string
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
}
