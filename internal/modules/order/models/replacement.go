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
	// deducted and a parcel names them. A canceled parcel sends the record back
	// to requested with its units on the shelf (ADR 0239).
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
	// Recalls counts the parcels canceled under this replacement (ADR 0239).
	// Each sent the record back to 'requested' with its units on the shelf, and
	// the next parcel is opened under a key that names the count, since a key
	// that resolves to a canceled parcel is refused (ADR 0088).
	Recalls int
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
// A claim's item carries no amount: a claim settles a fault with goods and
// sells nothing. An item of an exchange that names its return carries the
// price it is sold at ([ReplacementItem.Price], ADR 0432), because what it
// sends less what the return takes back is the exchange's difference. An item
// naming a line carries no variant, since the line holds it and is immutable.
type ReplacementItem struct {
	// ID is the identifier with the "oreplitem_" prefix.
	ID string
	// ReplacementID is the replacement the line belongs to.
	ReplacementID string
	// OrderLineItemID is the order line being replaced; EMPTY on an item that
	// names a variant instead.
	OrderLineItemID string
	// VariantID is the product being sent when it is NOT one the order sold.
	//
	// "Send me the same shirt a size larger" is the ordinary exchange, and until
	// ADR 0145 the only thing a replacement could carry was units of a variant
	// already on the order. Exactly one of this and [ReplacementItem.OrderLineItemID]
	// is set, which the schema holds as a CHECK rather than as a convention.
	//
	// It belongs to the product module and is NOT a foreign key here, the same
	// rule `order_line_items.variant_id` already follows.
	VariantID string
	// Quantity is how many units of that line are being sent.
	Quantity int64
	// ReservationID is the promise the units are held under. It belongs to the
	// inventory module and is not a foreign key here.
	//
	// It is what makes a dispatch retryable: a second attempt reuses the
	// promise the first one made instead of setting the same units aside
	// twice, and the confirm behind it is idempotent.
	//
	// It stays empty on an item with [ReplacementItem.Parts]: those units are
	// held part by part, under each part's own promise.
	ReservationID string
	// Parts are what one unit of the item holds when it replaces a line that
	// sold a bundle (ADR 0238), copied from the line when the item was written;
	// nil for any other item. The goods that leave are these parts, as the sale
	// took them, whatever the bundle is made of by then.
	Parts []ReplacementItemPart
	// Price is what the item's units are sold at, written once with the item
	// and never re-read from a price list or a tax table (ADR 0432); nil on an
	// item of a claim and of an exchange written without a return.
	Price *ReplacementPrice
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ReplacementPricedBy says where a replacement item's price came from.
type ReplacementPricedBy string

// The sources of a replacement item's price (ADR 0432).
const (
	// PricedByLine is the order line's own sold figures, for an item naming a
	// line: the units it sends are the units the line sold, at what the line
	// charged for them, so an even swap moves nothing.
	PricedByLine ReplacementPricedBy = "line"
	// PricedByQuote is a quote in the order's region, sales channel and
	// customer, taxed as a cart's line is, for an item naming a variant.
	PricedByQuote ReplacementPricedBy = "quote"
	// PricedByOperator is a unit price the operator named for an item naming a
	// variant; its tax is still the quote's computation at that price.
	PricedByOperator ReplacementPricedBy = "operator"
)

// Valid reports whether the source is one this module writes.
func (p ReplacementPricedBy) Valid() bool {
	switch p {
	case PricedByLine, PricedByQuote, PricedByOperator:
		return true
	default:
		return false
	}
}

// ReplacementPrice is what a replacement item's units are sold at.
//
// The figures are for the item's whole quantity, as an order line's are, and
// Total is what the buyer pays for them with the tax in it: Total less TaxTotal
// is their net value, whichever convention the order's prices follow.
type ReplacementPrice struct {
	// UnitPrice is the unit price the item was priced at, in the order's
	// convention: tax included when the order's prices include it (ADR 0246).
	UnitPrice int64
	// Total is what the buyer pays for the item's units, tax included.
	Total int64
	// TaxTotal is the tax in Total, at TaxRateBps.
	TaxTotal int64
	// TaxRateBps is the rate the tax was computed at, in basis points; the
	// stack's base when TaxComponents is filled, as on an order line.
	TaxRateBps int32
	// TaxComponents is the per-rate breakdown when a stack taxed the item;
	// empty when one rate says it all. Σ TaxAmount equals TaxTotal.
	TaxComponents []ReplacementItemTax
	// PricedBy says where the price came from.
	PricedBy ReplacementPricedBy
}

// ReplacementItemTax is one rate applied inside a replacement item's tax
// stack; the shape is an order line's component's ([OrderLineTax]).
type ReplacementItemTax struct {
	RateID        string
	RateBps       int32
	Compound      bool
	TaxableAmount int64
	TaxAmount     int64
}

// ReplacementItemPart is one variant a unit of a replacement item holds, how
// many of it, and the promise its units are held under (ADR 0238).
//
// VariantID belongs to the product module and ReservationID to the inventory
// module; neither is a foreign key here, as on the item.
type ReplacementItemPart struct {
	VariantID     string
	Quantity      int64
	ReservationID string
}

// Held reports whether every promise the item's units need is written: each
// part's for an item with parts, its own for any other.
func (i ReplacementItem) Held() bool {
	if len(i.Parts) == 0 {
		return i.ReservationID != ""
	}
	for _, p := range i.Parts {
		if p.ReservationID == "" {
			return false
		}
	}

	return true
}

// Names answers what the item is sending, for a message.
//
// One of the two identifiers is always empty, and a caller writing "%s" with the
// wrong one would report a blank where the reader expects a name.
func (i ReplacementItem) Names() string {
	if i.OrderLineItemID != "" {
		return i.OrderLineItemID
	}

	return i.VariantID
}

// SendsAVariant reports whether the item names goods the order did not sell.
//
// The two shapes are one fact — this many of this thing goes out — and only the
// naming differs, so the readers that care are few: the bought-ceiling, which
// applies to lines alone, and the detail document, which has a variant to publish
// either way.
func (i ReplacementItem) SendsAVariant() bool { return i.VariantID != "" }
