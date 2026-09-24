package models

import "time"

// This file carries the store credit LEDGER (ADR 0152): the shop's money owed
// to a customer. The provider's own session, which is how that money is spent,
// is [TenderSession], the record every balance tender of this module keeps.

// StoreCreditKind says what happened to a customer's credit.
//
// The vocabulary is closed and the schema holds it, because a misspelled kind
// would still SUM correctly: the balance would be right and the history would be
// unreadable, which is the half a ledger exists for.
type StoreCreditKind string

// The four things that can happen to store credit.
const (
	// StoreCreditIssue is an operator giving the customer money; the amount is
	// positive.
	StoreCreditIssue StoreCreditKind = "issue"
	// StoreCreditHold is a payment session putting some of it aside; the amount
	// is NEGATIVE.
	//
	// The hold is what makes the balance safe to read: from the moment it is
	// written the money is no longer spendable, so a second checkout cannot spend
	// it while the first one is still deciding.
	StoreCreditHold StoreCreditKind = "hold"
	// StoreCreditRelease is a canceled session's hold coming back; positive.
	StoreCreditRelease StoreCreditKind = "release"
	// StoreCreditRefund is a captured payment repaid into the credit; positive.
	StoreCreditRefund StoreCreditKind = "refund"
)

// Valid reports whether the kind is one of the four.
func (k StoreCreditKind) Valid() bool {
	switch k {
	case StoreCreditIssue, StoreCreditHold, StoreCreditRelease, StoreCreditRefund:
		return true
	default:
		return false
	}
}

// String returns the kind as text.
func (k StoreCreditKind) String() string { return string(k) }

// StoreCreditEntry is ONE event in a customer's credit ledger.
//
// There is no "balance" record anywhere: the balance is the sum of these rows,
// and a correction is a new row rather than an edited one.
type StoreCreditEntry struct {
	// ID is the "scredit_" prefixed identifier.
	ID string
	// CustomerID is whose money it is. It is not a foreign key (Principle 2.2).
	CustomerID string
	// CurrencyCode is the ISO 4217 code. Credit in one currency is not credit in
	// another, and the balance is read per currency for that reason.
	CurrencyCode string
	// Amount is SIGNED minor units; the balance is the sum of them. Its sign is
	// decided by the kind and the schema holds the pairing.
	Amount int64
	// Kind is what happened.
	Kind StoreCreditKind
	// Reference is the payment session the row belongs to, for the three kinds
	// that have one; an issue carries the operator's own reference or nothing.
	Reference string
	// Reason is why an operator issued the credit — the half a balance column
	// cannot keep.
	Reason string
	// CreatedAt is when it happened (UTC).
	CreatedAt time.Time
}
