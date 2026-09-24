package models

import "time"

// This file carries the loyalty point ledger (ADR 0164): the record of what a
// customer earned from the money that actually moved, of what a refund took
// back again, and — since ADR 0165 — of what they spent.

// LoyaltyKind says why a customer's point total changed.
//
// The vocabulary is closed and the schema holds it, for [StoreCreditKind]'s
// reason: a misspelled kind would still SUM correctly, so the balance would be
// right and the history would be unreadable, which is the half a ledger exists
// for.
type LoyaltyKind string

// The five things that can happen to a customer's points. The first two are
// EARNING and are written by the function that moves a collection's totals; the
// other three are SPENDING and are written by the loyalty-points provider, which
// runs the store-credit state machine on this ledger (ADR 0165).
const (
	// LoyaltyEarn is a collection's earned target rising because money was
	// taken; the points are positive.
	LoyaltyEarn LoyaltyKind = "earn"
	// LoyaltyReverse is that target falling because money was sent back; the
	// points are NEGATIVE.
	//
	// A reversal is a new row rather than a deleted one: what the customer
	// earned and what they gave back are two facts, and a ledger that erased the
	// first could not explain the second.
	LoyaltyReverse LoyaltyKind = "reverse"
	// LoyaltyHold is a payment session putting points aside; NEGATIVE. From the
	// moment it is written the points are not spendable by anything else, which
	// is what makes the balance safe to read.
	LoyaltyHold LoyaltyKind = "hold"
	// LoyaltyRelease is a canceled session's hold coming back; positive.
	LoyaltyRelease LoyaltyKind = "release"
	// LoyaltyRefund is a captured payment repaid into the points; positive.
	LoyaltyRefund LoyaltyKind = "refund"
)

// Valid reports whether the kind is one of the five.
func (k LoyaltyKind) Valid() bool {
	switch k {
	case LoyaltyEarn, LoyaltyReverse, LoyaltyHold, LoyaltyRelease, LoyaltyRefund:
		return true
	default:
		return false
	}
}

// String returns the kind as text.
func (k LoyaltyKind) String() string { return string(k) }

// LoyaltyEntry is ONE change to a customer's point total.
//
// There is no "balance" record anywhere: the balance is the sum of these rows.
// An earn row is the DIFFERENCE between what its collection should have earned
// and what it had already been written; a spend row is one step of a session's
// state machine.
type LoyaltyEntry struct {
	// ID is the "lpoint_" prefixed identifier.
	ID string
	// CustomerID is whose points these are. It is not a foreign key
	// (Principle 2.2).
	CustomerID string
	// CurrencyCode is the ISO 4217 code of the money the points were earned
	// from. Points are unitless but the money is not, and a customer who paid in
	// two currencies holds two balances rather than one meaningless sum.
	CurrencyCode string
	// Points is SIGNED; the balance is the sum of them. Its sign is decided by
	// the kind and the schema holds the pairing.
	Points int64
	// Kind is what happened.
	Kind LoyaltyKind
	// Reference is the payment collection an earn row was earned against, and
	// the provider's OWN session for a spend row — never a collection, so the
	// earn target, which is recomputed per collection, cannot mistake a hold for
	// points already written (ADR 0165).
	Reference string
	// CreatedAt is when it happened (UTC).
	CreatedAt time.Time
}
