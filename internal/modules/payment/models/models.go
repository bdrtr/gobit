// Package models holds the domain models of the payment module.
//
// The types here are independent of the database driver: pgtype and the types
// sqlc generates DO NOT LEAK in here. The translation is done in the
// repository layer; the service, the API and the tests see only these types.
//
// Money is everywhere an INTEGER minor unit (cents) and the currency stands in
// a separate field (plan Section 8); floating point is used in no field. Times
// are UTC.
//
// # What it does not know
//
// This module does not know WHICH cart or order a payment belongs to.
// [PaymentCollection.Reference] is free text, IT IS NOT a foreign key
// (Principle 2.2) and its existence is not validated here; the link is
// established through Module Links.
package models

import (
	"encoding/json"
	"time"
)

// Amount limits.
//
// The limits are not arbitrary: the collection's amount and the authorized,
// captured and refunded amounts are all subject to the same ceiling, and their
// sum MUST FIT into an int64. Because 4 × 10^12 < 9.22 × 10^18, an overflow is
// structurally impossible. The same ceiling is deliberately identical to the
// one in the cart and pricing modules; since the modules do not import one
// another, the value is repeated here (the accepted price of ADR 0001).
const (
	// MinAmount is the smallest permitted amount.
	//
	// IT IS NOT ZERO: no payment is collected for an order whose amount is
	// zero, and a collection opened like that could never become "captured" —
	// it would be a dead record waiting for payment forever.
	MinAmount int64 = 1
	// MaxAmount is the largest permitted amount (minor unit).
	MaxAmount int64 = 1_000_000_000_000
)

// PaymentMoments are WHEN a collection's money moved.
//
// The amounts live on the collection row; these two do not, because they belong
// to other tables — the capture is a payments row and the refund is a refunds
// row. They are read separately and only when asked for, so the common question
// (how much) does not pay for the rare one (when).
//
// Both are nil when the thing never happened, and nil is the honest answer: a
// zero time on a timeline reads as 1 January year one.
type PaymentMoments struct {
	// CollectionID is the collection the two moments belong to.
	CollectionID string
	// FirstCapturedAt is when money FIRST moved. With partial captures there
	// are several; the first one is what "when was it paid" means to the person
	// asking.
	FirstCapturedAt *time.Time
	// LastRefundedAt is when the LAST refund went out. With partial refunds
	// there are several; the most recent one is what "when was it refunded"
	// means.
	//
	// It comes from refunds.created_at: the refunds table has no refunded_at
	// column, and the row is written when the refund is made.
	LastRefundedAt *time.Time
}

// PaymentCollection is the container of the payments collected for a cart or
// an order.
//
// # Amounts
//
// AuthorizedAmount, CapturedAmount and RefundedAmount are the sums of the
// child records and are updated under the collection's row lock. Status is
// DERIVED from those amounts and from the session counts (see
// [CollectionStatusFor]); the column exists only for queryability.
//
// The REMAINING amount a new session can cover cannot be computed from this
// row on its own: open sessions that have not been authorized yet also reserve
// an amount, and none of it enters AuthorizedAmount. The computation is done
// in the service layer, which also sees the sessions, and under the collection
// lock.
type PaymentCollection struct {
	// ID is the "paycol_" prefixed, time-sortable identifier.
	ID string
	// Reference is the identifier of the caller's own record (a cart or an
	// order). IT IS NOT A FOREIGN KEY (Principle 2.2) and is not validated in
	// this module.
	Reference string
	// Amount is the total amount that must be collected (minor unit).
	Amount int64
	// CurrencyCode is the ISO 4217 code and is always stored in UPPER case.
	CurrencyCode string
	// Status is the derived status; see [CollectionStatusFor].
	Status CollectionStatus
	// AuthorizedAmount is the total amount that is STILL on hold (minor unit).
	//
	// It is not cumulative: the hold of a canceled or captured session is
	// subtracted from it. Otherwise the same money would count as both held
	// and captured, and the collection would show an amount that is not on the
	// customer.
	AuthorizedAmount int64
	// CapturedAmount is the total captured amount (minor unit).
	CapturedAmount int64
	// RefundedAmount is the total refunded amount (minor unit).
	RefundedAmount int64
	// Metadata is the caller's free-form extra data.
	Metadata map[string]any
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt is the moment of the soft delete; if nil, the collection is
	// alive.
	DeletedAt *time.Time
}

// RefundableAmount returns the amount that can be paid back out of the
// collection.
func (c PaymentCollection) RefundableAmount() int64 {
	if c.RefundedAmount >= c.CapturedAmount {
		return 0
	}
	return c.CapturedAmount - c.RefundedAmount
}

// PaymentSession is a payment session opened AT A PROVIDER.
type PaymentSession struct {
	// ID is the "payses_" prefixed module identifier.
	ID string
	// PaymentCollectionID is the collection the session is attached to (an
	// in-module FK).
	PaymentCollectionID string
	// ProviderID is the identifier of the provider that opened the session
	// (e.g. "manual").
	ProviderID string
	// ExternalID is the session identifier on the provider side; this is the
	// field that matches up the two systems during reconciliation.
	ExternalID string
	// Status is the current status of the session.
	Status SessionStatus
	// Amount is the amount of the session (minor unit).
	Amount int64
	// AuthorizedAmount is the amount put on hold; under a partial
	// authorization it can be smaller than Amount.
	AuthorizedAmount int64
	// CurrencyCode is the ISO 4217 code and is always stored in UPPER case.
	CurrencyCode string
	// Data is the provider's raw data; it is stored as is and not interpreted.
	Data json.RawMessage
	// IdempotencyKey prevents the same session from being opened twice.
	IdempotencyKey string
	// DeclineReason is filled only while Status is [SessionFailed]. It is for
	// diagnosis, IT IS NOT meant to be shown to the customer.
	DeclineReason string
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt is the moment of the soft delete; if nil, the session is alive.
	DeletedAt *time.Time
}

// Payment is a capture that has actually happened.
//
// AT MOST ONE capture is born from a session; a partial capture closes the
// session. The idempotency of Capture rests on that.
type Payment struct {
	// ID is the "pay_" prefixed identifier.
	ID string
	// PaymentSessionID is the session the capture came out of.
	PaymentSessionID string
	// PaymentCollectionID is the collection the capture belongs to.
	PaymentCollectionID string
	// Amount is the captured amount (minor unit).
	Amount int64
	// CurrencyCode is the ISO 4217 code.
	CurrencyCode string
	// RefundedAmount is the total amount refunded out of this capture.
	RefundedAmount int64
	// CapturedAt is the moment the capture happened (UTC).
	CapturedAt time.Time
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt is the moment of the soft delete; if nil, the capture is alive.
	DeletedAt *time.Time
}

// RefundableAmount returns the remaining amount that can be paid back out of
// the capture.
func (p Payment) RefundableAmount() int64 {
	if p.RefundedAmount >= p.Amount {
		return 0
	}
	return p.Amount - p.RefundedAmount
}

// Refund is the paying back of a capture. A partial refund produces more than
// one record.
type Refund struct {
	// ID is the "refund_" prefixed identifier.
	ID string
	// PaymentID is the capture the refund was made against.
	PaymentID string
	// Amount is the refunded amount (minor unit); it is always positive.
	Amount int64
	// Reason is the free-text reason of the refund; it is optional.
	Reason string
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt is the moment of the soft delete; if nil, the refund is alive.
	DeletedAt *time.Time
}

// ManualSession is the session in the manual provider's OWN ledger.
//
// This record is not the module's domain data; it is the state of the
// simulated external system. The payment service never touches it, only the
// manual provider reads and writes it
// (see internal/modules/payment/manual).
type ManualSession struct {
	// ID is the "manses_" prefixed PROVIDER identifier; it sits on the
	// module's session record as ExternalID.
	ID string
	// IdempotencyKey prevents the same session from being opened twice; it is
	// UNIQUE in the provider's ledger.
	IdempotencyKey string
	// Reference is the identifier of the caller's own record (the collection
	// identifier).
	Reference string
	// Amount is the amount of the session (minor unit).
	Amount int64
	// CurrencyCode is the ISO 4217 code.
	CurrencyCode string
	// Status is the status of the session on the provider side.
	Status SessionStatus
	// AuthorizedAmount, CapturedAmount and RefundedAmount are the amounts in
	// the provider's ledger (minor unit).
	AuthorizedAmount int64
	CapturedAmount   int64
	RefundedAmount   int64
	// Data is the free-form data given while the session was being opened. The
	// keys that steer the manual provider's behavior are in there (see the
	// manual package).
	Data json.RawMessage
	// DeclineReason is filled only while Status is [SessionFailed].
	DeclineReason string
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// RefundableAmount returns the remaining amount that can be paid back in the
// provider's ledger.
func (m ManualSession) RefundableAmount() int64 {
	if m.RefundedAmount >= m.CapturedAmount {
		return 0
	}
	return m.CapturedAmount - m.RefundedAmount
}
