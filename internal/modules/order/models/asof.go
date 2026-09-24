package models

import "time"

// OrderAsOf is an order as it stood at a past moment (ADR 0171).
//
// Every field is DERIVED from rows that carry their own moment: the order's
// stamps, its lines and line cancellations, its credits, its after-sales
// records' stamps, the payment collection's movements and the parcels'
// transitions. Nothing here is stored, so nothing here can disagree with the
// records it is read from; a field whose past the records do not keep is not
// guessed but said to be unknown.
type OrderAsOf struct {
	// OrderID is the order read.
	OrderID string
	// At is the moment it was read at.
	At time.Time
	// Status is the order's status at At. It is nil for the one moment the
	// records cannot place: an order archived before its archiving was dated
	// (migration 000007), read after its completion.
	Status *OrderStatus
	// Money is what the order was owed and had moved at At.
	Money MoneyAsOf
	// Lines are the order's lines with what had been canceled of them by At.
	Lines []LineAsOf
	// Returns, Claims, Exchanges and Replacements are the after-sales records
	// that existed at At, each with its status then.
	Returns      []RecordAsOf
	Claims       []RecordAsOf
	Exchanges    []RecordAsOf
	Replacements []RecordAsOf
	// Shipments are the parcels that existed at At, each with its status then.
	Shipments []RecordAsOf
	// Contact says whether the contact and addresses the order holds today are
	// the ones it held at At.
	Contact ContactAsOf
}

// MoneyAsOf is an order's money at a moment.
//
// Captured and Refunded are summed from the payment collection's movements up
// to the moment, not read from the order's recorded summary: the summary keeps
// only its latest value. Outstanding is [OrderSummary.Outstanding] over those
// sums, so it is the same formula the live order uses.
type MoneyAsOf struct {
	Currency    string
	Total       int64
	Credited    int64
	Captured    int64
	Refunded    int64
	Outstanding int64
}

// LineAsOf is one order line at a moment. Quantity is what was bought, which
// never changes; Canceled is what had been canceled of it by the moment.
type LineAsOf struct {
	LineItemID string
	VariantID  string
	Title      string
	Quantity   int64
	UnitPrice  int64
	Canceled   int64
}

// RecordAsOf is a record's status at a moment and when it entered it.
type RecordAsOf struct {
	// ID is the record.
	ID string
	// Status is its status at the moment, in the record's own vocabulary.
	Status string
	// Since is when it entered that status.
	Since time.Time
}

// ContactAsOf says what the order's contact was at a moment.
//
// The e-mail and the addresses are written once when the order is placed and
// changed only by erasure, which empties them; so before an erasure the contact
// held now IS the contact held then, and after one there is none.
type ContactAsOf string

// Contact states.
const (
	// ContactHeld means the contact the order holds now is the one it held at
	// the moment.
	ContactHeld ContactAsOf = "held"
	// ContactErased means it had been erased by the moment.
	ContactErased ContactAsOf = "erased"
	// ContactErasedSince means the order held a contact at the moment and it
	// has been erased since; what it was is no longer known to anyone.
	ContactErasedSince ContactAsOf = "erased_since"
)
