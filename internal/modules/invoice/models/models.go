// Package models holds the invoice module's domain types.
//
// # An invoice is a SNAPSHOT, not a view
//
// Every field the document prints is COPIED at the moment it is issued: the
// seller's and buyer's details, each line's title, quantity, unit price,
// discount, tax rate and amounts. Nothing here points at a catalog record, a
// customer record or an order line whose contents could change afterwards.
//
// That is what an invoice is for. It answers "who sold what to whom, at what
// price, on what date" permanently, and an answer that moved when the catalog
// was edited would not be an answer.
package models

import (
	"time"

	"github.com/bdrtr/gobit/internal/core/page"
)

// Kind is what the document is FOR.
type Kind string

const (
	// KindSale is the ordinary sales invoice.
	KindSale Kind = "sale"
	// KindRefund is the document that reverses a sale, in whole or in part.
	//
	// It is a document of its own with a number of its own rather than an edit
	// to the sale: an issued invoice cannot be changed, and the reversal has to
	// be as traceable as the sale was.
	KindRefund Kind = "refund"
)

// Valid reports whether the kind is one this module knows.
func (k Kind) Valid() bool { return k == KindSale || k == KindRefund }

// String returns the kind as text.
func (k Kind) String() string { return string(k) }

// Status is where the document is in its life.
//
// # There is deliberately no DRAFT
//
// A draft would need a number, because a document nobody can refer to is not a
// draft of anything — and a number handed to a draft that is then abandoned is
// exactly the gap the series may not have (see [Series]). So a document is
// either issued or it does not exist; what a shop would call a draft is
// prepared outside this module and only reaches it when it is real.
type Status string

const (
	// StatusIssued means the document exists and carries its number. This is
	// the state every invoice is born in.
	StatusIssued Status = "issued"
	// StatusSent means a provider accepted it for transmission.
	StatusSent Status = "sent"
	// StatusAccepted means the receiving side accepted it.
	StatusAccepted Status = "accepted"
	// StatusRejected means the receiving side refused it.
	//
	// The document REMAINS: its number is spent and the reason is part of the
	// record. What follows a rejection is a cancellation and a new invoice, not
	// an edit of this one.
	StatusRejected Status = "rejected"
	// StatusCanceled means the document was withdrawn.
	//
	// The row stays and the number stays spent. Deleting it would put a hole in
	// the series, which is the one thing the series may not have.
	StatusCanceled Status = "canceled"
)

// Valid reports whether the status is one this module knows.
func (s Status) Valid() bool {
	switch s {
	case StatusIssued, StatusSent, StatusAccepted, StatusRejected, StatusCanceled:
		return true
	default:
		return false
	}
}

// String returns the status as text.
func (s Status) String() string { return string(s) }

// CanMoveTo reports whether the document may move from this status to the next.
//
// The table is written out rather than derived, because every one of these
// edges is a decision:
//
//   - issued may be sent, or canceled before anyone saw it.
//   - sent may be accepted or rejected by the receiving side, and may still be
//     canceled — a transmission that never comes back is a real situation and
//     the operator has to be able to close it.
//   - accepted and rejected are both FINAL for transmission, but accepted can
//     still be canceled: a genuine sale can be withdrawn afterwards, which is
//     an ordinary event in a shop.
//   - rejected does NOT move to canceled: it never took effect, so there is
//     nothing to withdraw, and letting it would make the two states mean the
//     same thing in the record.
//   - canceled is final in every direction.
func (s Status) CanMoveTo(next Status) bool {
	allowed := map[Status][]Status{
		StatusIssued:   {StatusSent, StatusCanceled},
		StatusSent:     {StatusAccepted, StatusRejected, StatusCanceled},
		StatusAccepted: {StatusCanceled},
		StatusRejected: nil,
		StatusCanceled: nil,
	}

	for _, candidate := range allowed[s] {
		if candidate == next {
			return true
		}
	}

	return false
}

// Party is one side of the document, copied at the moment it is issued.
//
// # Why the tax identifiers are free text
//
// A Turkish invoice carries a VKN (10 digits, legal entity) or a TCKN (11
// digits, natural person), and other jurisdictions carry other things. The
// module stores what it was given and does not validate the shape: a framework
// that refused an identifier format it had not heard of would be wrong in every
// country it did not anticipate, and the validation belongs where the regime is
// known — the provider.
type Party struct {
	// Name is the legal name printed on the document.
	Name string
	// TaxNumber is the VKN/TCKN or its equivalent; it may be empty.
	TaxNumber string
	// TaxOffice is the tax office the number belongs to (Turkey); may be empty.
	TaxOffice string
	// Email is where the document is sent; it may be empty.
	Email string
	// Address is the printed address, already formatted into lines.
	Address string
	// CountryCode is the ISO 3166-1 alpha-2 country code.
	CountryCode string
}

// Line is one row of the document.
//
// The amounts are minor-unit integers and the rate is in basis points, the same
// as everywhere else in the repository. Nothing here is a percentage float.
type Line struct {
	// ID is the line's own identifier.
	ID string
	// InvoiceID is the document the line belongs to.
	InvoiceID string
	// Position is the line's place on the printed document, starting at 1.
	//
	// It is stored rather than derived from the insertion order, because the
	// printed order is part of the document and a listing sorted by id would
	// reorder it the day the id format changes.
	Position int32
	// Description is what is printed; it was copied from the catalog when the
	// order was placed and copied again here.
	Description string
	// Quantity is the count sold.
	Quantity int64
	// UnitPrice is the unit price (minor unit).
	UnitPrice int64
	// Subtotal is UnitPrice x Quantity.
	Subtotal int64
	// DiscountTotal is the discount falling on the line, carried POSITIVE.
	DiscountTotal int64
	// TaxRateBps is the rate the line was taxed at, in BASIS POINTS.
	//
	// It is copied rather than recomputed: the tax is rounded down per line, so
	// the amount alone maps back to a range of rates, and the document has to
	// print the rate the customer was CHARGED under.
	TaxRateBps int32
	// TaxTotal is the tax falling on the line.
	TaxTotal int64
	// Total is Subtotal - DiscountTotal + TaxTotal.
	Total int64
}

// Invoice is the document.
type Invoice struct {
	// ID is the record's identifier.
	ID string
	// Number is the legal serial, e.g. "GBT2026000000001".
	//
	// It is allocated from a series under a row lock and is UNIQUE. See
	// [Series] for why it is not a database sequence.
	Number string
	// SeriesID is the series the number was taken from.
	SeriesID string
	// Kind is what the document is for.
	Kind Kind
	// Status is where it is in its life.
	Status Status
	// CurrencyCode is the currency of every amount on it (ISO 4217).
	CurrencyCode string
	// Seller and Buyer are the two sides, copied at issue.
	Seller Party
	Buyer  Party
	// Subtotal, DiscountTotal, TaxTotal and Total are the document totals.
	//
	// They are stored rather than summed from the lines on read: the document
	// is what was issued, and a total recomputed later from lines could differ
	// from the one that was printed if the summing rule ever changed.
	Subtotal      int64
	DiscountTotal int64
	TaxTotal      int64
	Total         int64
	// IssuedAt is the moment the document came into being.
	IssuedAt time.Time
	// ProviderID is the transmission provider that handled it; empty until one
	// does.
	ProviderID string
	// ExternalID is the identifier the provider gave it; empty until then.
	ExternalID string
	// StatusReason carries WHY a rejection or a cancellation happened.
	StatusReason string
	// Lines are the rows of the document, in printed order.
	Lines []Line
	// Metadata is free structured context.
	Metadata map[string]any
	// CreatedAt and UpdatedAt are the record's own timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TotalsConsistent reports whether the document's totals satisfy its identity.
//
// Total = Subtotal - DiscountTotal + TaxTotal. Shipping is not a separate term
// here: it reaches the document as a LINE, because that is how it is printed.
func (i Invoice) TotalsConsistent() bool {
	return i.Total == i.Subtotal-i.DiscountTotal+i.TaxTotal
}

// Series is the source of invoice numbers.
//
// # Why a locked row and not a database sequence
//
// The order module numbers its orders with an IDENTITY sequence, and its
// migration argues the case: a sequence advances atomically, two concurrent
// inserts cannot take the same value, and there is no common row to lock
// because both of them open a NEW row.
//
// The invoice needs the OPPOSITE answer, and the reason is legal rather than
// technical. A sequence is not GAP-FREE: it advances outside the transaction,
// so a transaction that rolls back burns its number and leaves a hole. For an
// order number a hole is harmless. For an invoice serial it is not — the series
// has to run 1, 2, 3 with nothing missing, because a missing number reads as a
// document that was issued and then hidden.
//
// The invoice does have a common row to lock: the series itself. Every invoice
// in a series locks that one row, reads the last number, writes the next, and
// commits both together. Concurrency is serialized on the series — which is
// exactly the cost the guarantee is worth, and which is bounded because the
// contention is per series and per year.
type Series struct {
	// ID is the record's identifier.
	ID string
	// Prefix is the letters at the front of the number (Turkey: exactly 3).
	Prefix string
	// Year is the calendar year the series belongs to.
	//
	// A series is per YEAR because the numbering restarts each year; carrying
	// the year in the row rather than parsing it out of the last number keeps
	// the reset explicit.
	Year int32
	// LastNumber is the last sequence value handed out; the next invoice gets
	// LastNumber + 1.
	LastNumber int64
	// CreatedAt and UpdatedAt are the record's own timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Filter is the criterion set of the invoice listing.
//
// It lives in models rather than in either the service or the repository
// because both sides speak it; a copy on each side would be two types that have
// to be kept in step by hand.
type Filter struct {
	// Status, when given, returns only the documents in that status.
	Status *string
	// Kind, when given, returns only the documents of that kind.
	Kind *string
	// Limit is the maximum number of rows to return.
	Limit int64
	// Offset is the number of rows to skip.
	Offset int64
	// After is the keyset position the page starts below; the zero value is the
	// first page.
	//
	// It is applied TOGETHER with Offset so the query keeps one shape; the API
	// refuses the two at once, because they name two different positions.
	After page.Cursor
}
