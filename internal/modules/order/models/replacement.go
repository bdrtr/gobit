package models

import "time"

// ReplacementStatus is the state of a replacement record.
type ReplacementStatus string

// The states a replacement can be in.
//
// There are TWO of them, and the shortness is the decision. A replacement that
// is held, dispatching or dispatched needs stock to have moved or a parcel to
// exist, and neither is possible yet; a status no code path can produce is the
// promise migration 000008 was written to withdraw. The vocabulary grows with
// the flow that fills it.
const (
	// ReplacementRequested means the replacement was asked for.
	ReplacementRequested ReplacementStatus = "requested"
	// ReplacementCanceled means the request was withdrawn.
	ReplacementCanceled ReplacementStatus = "canceled"
)

// Valid reports whether the status is one this module writes.
func (s ReplacementStatus) Valid() bool {
	switch s {
	case ReplacementRequested, ReplacementCanceled:
		return true
	default:
		return false
	}
}

// String returns the status as it is stored.
func (s ReplacementStatus) String() string { return string(s) }

// Replacement is what a claim promises to send.
//
// # Why it is not part of the claim
//
// A claim says HOW it will be settled and, when that is money, how much. What
// goes out instead is a different fact with its own lifecycle: it can be asked
// for, withdrawn, and one day shipped, while the claim it belongs to stays
// where it is. Putting the items on the claim would also have made a 'refund'
// claim carry columns it can never fill.
//
// # Why it is not part of the order
//
// The order is immutable — see [Order] — so a replacement is a record BESIDE
// it, the way a return and a claim are.
type Replacement struct {
	// ID is the identifier with the "orepl_" prefix.
	ID string
	// ClaimID is the claim this replacement settles.
	ClaimID string
	// Status is the state of the record.
	Status ReplacementStatus
	// ShippingOptionID is HOW it will be sent. It is answered when the
	// replacement is asked for rather than when it ships, so a retry reads it
	// from the record instead of trusting a repeated body.
	ShippingOptionID string
	// LocationID is the stock location it will be sent FROM.
	LocationID string
	// Note is a free-form note.
	Note string
	// CanceledAt is the moment the request was withdrawn; nil while it is open.
	//
	// The pairing with Status is held by the database in BOTH directions
	// (order_replacements_canceled_stamp), so a canceled replacement without a
	// moment cannot be written.
	CanceledAt *time.Time
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ReplacementItem is one line of a replacement: which line, and how many.
//
// It carries no variant and no amount, and both absences have the same reason
// as their counterparts on [ReturnItem]: the order line already holds the
// variant and is immutable, and a replacement is settled with goods rather than
// money.
type ReplacementItem struct {
	// ID is the identifier with the "oreplitem_" prefix.
	ID string
	// ReplacementID is the replacement the line belongs to.
	ReplacementID string
	// OrderLineItemID is the order line being replaced.
	OrderLineItemID string
	// Quantity is how many units of that line are being sent.
	Quantity int64
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
}
