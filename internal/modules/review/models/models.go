// Package models holds the review module's domain types.
//
// # A review is INVISIBLE until a human approves it
//
// Every type here is shaped by that one sentence. A review is born
// [StatusSubmitted], the storefront read is filtered to [StatusApproved], and
// the only way between the two is an admin endpoint an operator calls.
//
// The reason is decision A15 in docs/gaps.md: the storefront's only principal
// is the publishable key, so content arriving there is written by a party this
// framework cannot identify. A15 carries a discriminator for that class — does
// a human stand between the write and its effect? — and approval is what makes
// the answer yes.
package models

import (
	"time"

	"github.com/bdrtr/gobit/internal/core/page"
)

// Status is where a review stands.
//
// # There is deliberately no "published" separate from "approved"
//
// Two states would mean an operator could approve a review and then decide
// separately whether it is shown, which is a second decision nobody would make
// differently from the first. Approval IS publication here, and that is what
// makes the storefront filter a single equality rather than a rule that has to
// be repeated in every read.
type Status string

const (
	// StatusSubmitted is where every review is born: written, stored, and
	// visible to nobody but an operator.
	StatusSubmitted Status = "submitted"
	// StatusApproved means an operator read it and it may be shown.
	StatusApproved Status = "approved"
	// StatusRejected means an operator read it and it may not.
	//
	// The row STAYS. Deleting it would lose the record of the decision, and the
	// same text arriving again would be moderated a second time by somebody who
	// could not see that it had already been refused once.
	StatusRejected Status = "rejected"
)

// Valid reports whether the status is one this module knows.
func (s Status) Valid() bool {
	switch s {
	case StatusSubmitted, StatusApproved, StatusRejected:
		return true
	default:
		return false
	}
}

// String returns the status as text.
func (s Status) String() string { return string(s) }

// Moderated reports whether a human has already decided about this status.
//
// It is the Go half of the schema's reviews_moderation_mirror constraint: the
// moment stamp is set exactly when this is true, and the two agreeing is what
// keeps the column from meaning something different from the status next to it.
func (s Status) Moderated() bool { return s == StatusApproved || s == StatusRejected }

// CanMoveTo reports whether a review may move from this status to the next.
//
// The table is written out rather than derived, because every edge is a
// decision:
//
//   - submitted may be approved or rejected. That is the moderation act, and it
//     is the only thing this module exists to make possible.
//   - approved may be REJECTED. This is the edge that is easy to leave out and
//     expensive to be missing: it is the only way to take a published review
//     back down. Without it an operator who approves something defamatory has
//     no exit that is not psql.
//   - rejected may be APPROVED. A rejection is a person's judgement, not a
//     legal record, and judgements get reversed. Refusing this edge would mean
//     the only repair is asking the author to write the review again — and the
//     author has no identity here, no way to find the row and no way to be
//     told, so in practice the repair would not happen at all. This is where a
//     review differs from an invoice, whose rejected status is terminal because
//     the document's number is spent either way.
//   - a status never moves to ITSELF. Approving an already approved review
//     would restamp the moment of a decision that was taken earlier, and the
//     record would then say a human looked at it at a time no human did.
func (s Status) CanMoveTo(next Status) bool {
	allowed := map[Status][]Status{
		StatusSubmitted: {StatusApproved, StatusRejected},
		StatusApproved:  {StatusRejected},
		StatusRejected:  {StatusApproved},
	}

	for _, candidate := range allowed[s] {
		if candidate == next {
			return true
		}
	}

	return false
}

// Review is one customer-written review of a product.
type Review struct {
	// ID is the record's identifier.
	ID string
	// ProductID is what the review is ABOUT.
	//
	// It is a product rather than a variant because the storefront addresses
	// products and cannot address a variant. It belongs to another module and
	// is not validated here (Principle 2.2), the same rule the order module's
	// line follows for the variant it sold.
	ProductID string
	// Rating is the star count, 1 to 5, bounded by the column's own CHECK as
	// well as by the service.
	Rating int16
	// Title is the headline; it may be empty.
	Title string
	// Body is the review text.
	Body string
	// AuthorName is the byline the author typed.
	//
	// It is the ONLY thing this module stores about the person who wrote the
	// review. There is no email, no phone number and no network address; see
	// the migration for the argument, which is the same one that keeps the
	// recipient address out of the notification module.
	AuthorName string
	// Status is where the review stands.
	Status Status
	// ModeratedAt is when a human decided; it is the zero time while the review
	// is still submitted.
	ModeratedAt time.Time
	// ModerationNote is why. It is required for a rejection.
	ModerationNote string
	// CreatedAt and UpdatedAt are the record's own timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Summary is the count-and-average a product page shows next to a rating.
//
// # Why it is computed and not stored
//
// The alternative is a denormalized row per product, kept in step inside every
// moderation transaction. It was refused on a measurement rather than a taste,
// and the numbers are in the migration: with the partial index the module
// declares, this aggregate costs 0.2 ms for a product with 19 approved reviews,
// 1.3-2.0 ms at 5,000 and 9.3 ms at 50,000, against 33-38 ms with no index at
// all. A stored counter would buy those milliseconds and owe a correctness
// obligation on every path that ever writes a review row — which is exactly the
// trade docs/gaps.md A16 records against denormalizing a price into the catalog,
// where the missing piece was an invalidation signal.
//
// The crossing point is stated rather than hidden: the cost is linear in the
// number of APPROVED reviews of the one product being read, so a shop whose
// single product carries hundreds of thousands of them is the case that wants a
// stored counter. Nothing else is.
type Summary struct {
	// ProductID is the product the two numbers belong to.
	ProductID string
	// Count is how many APPROVED reviews the product has.
	Count int64
	// AverageHundredths is the mean rating multiplied by 100 and rounded — 433
	// means 4.33 stars.
	//
	// It is an integer for the same reason money is one in this repository: a
	// float would make the value depend on where it was rounded, and the number
	// a shop prints next to a product should not differ between two clients
	// that both did the obvious thing. Zero when Count is zero, which a client
	// can tell apart because the count is right there.
	AverageHundredths int64
}

// Filter is the criterion set of the review listings.
//
// It lives in models rather than in either the service or the repository
// because both sides speak it; a copy on each side would be two types that have
// to be kept in step by hand.
type Filter struct {
	// Status, when given, returns only the reviews in that status. The
	// storefront listing sets it to "approved" and never takes it from the
	// request; the admin listing takes it from the query string.
	Status *string
	// ProductID, when given, returns only the reviews of that product.
	ProductID *string
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
