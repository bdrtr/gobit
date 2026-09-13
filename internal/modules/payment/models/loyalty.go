package models

import "time"

// This file carries the loyalty point ledger (ADR 0164): the record of what a
// customer earned from the money that actually moved, and of what a refund took
// back again.

// LoyaltyKind says why a customer's point total changed.
//
// The vocabulary is closed and the schema holds it, for [StoreCreditKind]'s
// reason: a misspelled kind would still SUM correctly, so the balance would be
// right and the history would be unreadable, which is the half a ledger exists
// for.
type LoyaltyKind string

// The two things that can happen to a customer's points.
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
)

// Valid reports whether the kind is one of the two.
func (k LoyaltyKind) Valid() bool {
	switch k {
	case LoyaltyEarn, LoyaltyReverse:
		return true
	default:
		return false
	}
}

// String returns the kind as text.
func (k LoyaltyKind) String() string { return string(k) }

// LoyaltyEntry is ONE change to a customer's point total.
//
// There is no "balance" record anywhere: the balance is the sum of these rows,
// and every row is the DIFFERENCE between what its collection should have earned
// and what it had already been written.
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
	// Reference is the payment collection the row was earned against. Every row
	// has one: the target is recomputed per collection, and this is what makes
	// that sum possible.
	Reference string
	// CreatedAt is when it happened (UTC).
	CreatedAt time.Time
}
