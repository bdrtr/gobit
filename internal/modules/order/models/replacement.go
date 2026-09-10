package models

import "time"

// ReplacementStatus is the state of a replacement record.
type ReplacementStatus string

// The states a replacement can be in.
//
// There are THREE of them and the shortness is still the decision: each one is
// written by a code path that exists. The third arrived with the flow that
// moves the goods (migration 000012); there is no 'held' and no 'dispatching',
// because nothing waits and nothing is half-sent — the dispatch sets stock
// aside, opens a parcel and confirms, and the status is its outcome.
const (
	// ReplacementRequested means the replacement was asked for.
	ReplacementRequested ReplacementStatus = "requested"
	// ReplacementCanceled means the request was withdrawn.
	ReplacementCanceled ReplacementStatus = "canceled"
	// ReplacementDispatched means the goods left the warehouse: the stock was
	// deducted and a parcel names them.
	ReplacementDispatched ReplacementStatus = "dispatched"
)

// Valid reports whether the status is one this module writes.
func (s ReplacementStatus) Valid() bool {
	switch s {
	case ReplacementRequested, ReplacementCanceled, ReplacementDispatched:
		return true
	default:
		return false
	}
}

// String returns the status as it is stored.
func (s ReplacementStatus) String() string { return string(s) }

// ReplacementSource names the kind of record a replacement settles.
type ReplacementSource string

// Replacement sources.
const (
	// SourceClaim is a damage or shortage claim to be met with goods.
	SourceClaim ReplacementSource = "claim"
	// SourceExchange is an exchange: goods going out against goods coming back.
	SourceExchange ReplacementSource = "exchange"
)

// Replacement is what a claim or an exchange promises to send.
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
	// ClaimID is the claim this replacement settles; empty when the source is an
	// exchange.
	ClaimID string
	// ExchangeID is the exchange this replacement settles; empty when the source
	// is a claim.
	//
	// Exactly ONE of the two is set and the database holds it
	// (order_replacements_one_source): a record with neither hangs off nothing
	// and could not be read back to an order, and a record with both would
	// answer "which one settled it" twice. Read the pair through [Replacement.Source].
	ExchangeID string
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
	// DispatchedAt is the moment the goods left; nil until they do. Its pairing
	// with Status is held the same way, by
	// order_replacements_dispatched_stamp.
	DispatchedAt *time.Time
	// FulfillmentID is the parcel the goods left in. It belongs to the
	// fulfillment module and IS NOT A FOREIGN KEY here (Principle 2.2); the
	// database requires it on a dispatched row, because a dispatch with no
	// parcel would be goods leaving with nothing to carry them.
	FulfillmentID string
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Source reports which kind of record this replacement settles.
//
// It reads the pair rather than a column of its own: a stored discriminator
// beside the two identifiers is a third thing that can disagree with them, and
// the pair already says it.
func (r Replacement) Source() ReplacementSource {
	if r.ExchangeID != "" {
		return SourceExchange
	}

	return SourceClaim
}

// SourceID is the identifier of the record this replacement settles.
func (r Replacement) SourceID() string {
	if r.ExchangeID != "" {
		return r.ExchangeID
	}

	return r.ClaimID
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
	// ReservationID is the promise the units are held under. It belongs to the
	// inventory module and is not a foreign key here.
	//
	// It is what makes a dispatch retryable: a second attempt reuses the
	// promise the first one made instead of setting the same units aside
	// twice, and the confirm behind it is idempotent.
	ReservationID string
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
}
