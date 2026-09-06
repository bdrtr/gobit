package models

import "time"

// This file is the rule that decides whether an order can be FORGOTTEN yet.
//
// It stands here, as a pure database-free function, for the reason
// aftersales_status.go states for the after-sales transitions: the rule is
// worth reading AS A TABLE rather than being dug out of the branches of a
// service method. The service turns the answer into a sentence a controller
// can repeat to a data subject; what the answer IS gets decided here.
//
// Why the module needs the rule at all is ADR 0032's argument read backwards.
// The invoice refuses erasure in the schema because a document that was ISSUED
// is a document the law makes somebody keep. None of the three reasons it gives
// reaches the order: the order's address columns are nullable TEXT with no
// CHECK where the invoice's buyer_name is NOT NULL, ADR 0024's immutability
// clause binds the invoice NUMBER and not this record, and "an order is a
// commercial record" is a retention judgement — which ADR 0029 lists among the
// things gobit explicitly does NOT owe. So the order anonymizes.
//
// What it does NOT do is anonymize an order that is still being PERFORMED. That
// is not a retention judgement smuggled back in: an unsettled order is work in
// progress, the contact and the address are what the remaining work is carried
// out with, and erasing them would not protect the person, it would strand a
// parcel or a refund that is owed to them. The carve-out is therefore bounded
// by a fact the module can measure rather than by a period somebody chose.

// UnsettledFact names the one fact that keeps an order from being forgotten.
//
// It is a fact and not a boolean because "retained" with no reason is useless
// to whoever has to answer the data subject: the person is owed a sentence
// saying WHAT is still open and, by implication, when to ask again.
type UnsettledFact int

// The settlement facts.
const (
	// Settled means nothing is outstanding on the order and it can be
	// anonymized.
	Settled UnsettledFact = iota
	// UnsettledPending means the order has not reached a terminal status: it
	// was taken and it has been neither completed nor canceled, so the sale is
	// still being performed.
	UnsettledPending
	// UnsettledOutstanding means money is still owed in one direction or the
	// other.
	UnsettledOutstanding
	// UnsettledReturnRequested means a return was asked for and the goods have
	// neither arrived nor been written off.
	UnsettledReturnRequested
	// UnsettledExchangeRequested means an exchange request is still open.
	UnsettledExchangeRequested
	// UnsettledClaimRequested means a damage or shortage claim is still open.
	UnsettledClaimRequested
)

// String returns the fact in the words somebody answering a data-subject
// request would use.
//
// The sentences deliberately name the RECORD and not the person: the string
// ends up in an erasure report that is handed to a controller, and a report
// about forgetting somebody is the last place to restate their address.
func (f UnsettledFact) String() string {
	switch f {
	case Settled:
		return "settled"
	case UnsettledPending:
		return "the order is still pending"
	case UnsettledOutstanding:
		return "money is still outstanding on the order"
	case UnsettledReturnRequested:
		return "a return has been requested and not received"
	case UnsettledExchangeRequested:
		return "an exchange request is still open"
	case UnsettledClaimRequested:
		return "a claim is still open"
	default:
		return "unknown"
	}
}

// OrderErasureCandidate is one order of the person an erasure request is about,
// together with everything needed to decide whether it can be forgotten.
//
// It is a READ shape and not a slice of [Order]: the four facts that decide the
// question live in four different tables, and an [Order] carries none of them.
// Loading the order, its summary and its three kinds of after-sales record
// separately would be four extra round trips per order for a person who may
// have hundreds of them, and the decision would still have to be reassembled
// afterwards.
type OrderErasureCandidate struct {
	// OrderID is the order the facts belong to.
	OrderID string
	// DisplayID is the human-readable number.
	//
	// It is carried because it is what the report NAMES. A controller telling a
	// person "order 1042 is still pending" is saying something the person can
	// check; an internal identifier is not.
	DisplayID int64
	// Status is where the order stands in its lifecycle.
	Status OrderStatus
	// CurrencyCode is the currency the outstanding amount is in; without it the
	// number in the report means nothing.
	CurrencyCode string
	// Deleted reports that the order has been soft deleted.
	//
	// A soft-deleted order is still ERASED — the row holds the person's e-mail
	// whether or not the read paths show it — but it is never RETAINED: nothing
	// is being performed on a record no surface can reach, so holding a
	// person's data against work that cannot happen would be a refusal with no
	// fact behind it.
	Deleted bool
	// ErasedAt is when this order's personal columns were rewritten; nil while
	// they never were.
	//
	// It is what makes a second sweep able to tell "already anonymized" from
	// "never had an e-mail", which the columns themselves cannot say: every one
	// of them is nullable for reasons that predate erasure (migration 000009).
	ErasedAt *time.Time
	// Total is the order's own total and PaidTotal and RefundedTotal are the
	// summary's LIFETIME amounts, all in minor units.
	//
	// The three are carried raw rather than pre-subtracted into one figure
	// because the two summary columns only ever GROW: queries/order_summaries.sql
	// merges them with GREATEST so that payment events, which are delivered at
	// least once and in no order, converge. A refund therefore does not shrink
	// PaidTotal, and the difference [OrderSummary.Outstanding] shows a shop —
	// total - (paid - refunded) — reads a FULLY REFUNDED order as owing its
	// whole total for ever. That figure answers "how much of this sale is
	// unpaid", which is the right question on a payment screen and the wrong one
	// here; [OrderErasureCandidate.Owed] answers the question this file asks,
	// which is whether the money has stopped moving.
	Total         int64
	PaidTotal     int64
	RefundedTotal int64
	// ReturnRequested, ExchangeRequested and ClaimRequested report whether an
	// after-sales record of that kind is still in its requested state.
	ReturnRequested   bool
	ExchangeRequested bool
	ClaimRequested    bool
}

// Owed reports what is still owed on the order in minor units: a positive
// number is owed BY the buyer, a negative one is owed BACK to them, and zero
// means the money has stopped moving.
//
// # Why it is not total - (paid - refunded)
//
// That expression — the one [OrderSummary.Outstanding] computes — cannot answer
// this question, because paid_total NEVER SHRINKS. A refund is recorded by
// GROWING refunded_total beside it (queries/order_summaries.sql explains why the
// two columns are merged with GREATEST rather than overwritten), so an order
// that was paid in full and then refunded in full still reads as owing its
// whole total under that expression. Believing it, this module would answer
// RETAINED for that order on every sweep for ever: a person who was fully
// refunded could never be forgotten, and the report would keep saying the sale
// is still moving after it had finished.
//
// # The two debts, and why one signed number can carry both
//
// Three lifetime numbers describe two different debts, and only one of them can
// hold at a time:
//
//   - total - paid_total, while it is positive, is the part of the sale that
//     was never collected — money the buyer owes;
//   - (paid_total - refunded_total) - total, while it is positive, is money the
//     shop is holding OVER the value of the sale — an overcollection owed back.
//
// Both would need a negative refunded_total to hold at once, so they are
// exclusive, and one signed number says which debt it is as well as how much.
//
// A full refund lands on zero from both sides: nothing was left uncollected
// that anybody will ever collect, and the shop holds nothing. That is the case
// the old expression got wrong.
func (c OrderErasureCandidate) Owed() int64 {
	if uncollected := c.Total - c.PaidTotal; uncollected > 0 {
		return uncollected
	}
	if overheld := c.PaidTotal - c.RefundedTotal - c.Total; overheld > 0 {
		return -overheld
	}

	return 0
}

// Unsettled reports the fact that keeps the order from being forgotten, or
// [Settled].
//
// # The order of the checks
//
// Several facts can hold at once — a pending order usually also has its total
// outstanding — and exactly one of them is reported, so the order of the checks
// is the answer's wording and not an implementation detail. It runs:
//
//  1. the STATUS, because it is the order's own state and the one a reader
//     recognizes without any arithmetic;
//  2. the MONEY, as [OrderErasureCandidate.Owed] measures it, because it is the
//     consequential one: a person told "there is still an amount outstanding"
//     learns something they can act on, and the figure would otherwise never be
//     mentioned;
//  3. the three after-sales requests, in return, exchange, claim order, which
//     is the order they are declared in and the order the schema created them
//     in.
//
// The sequence is fixed rather than "whichever is found first" so that two
// runs of the same sweep produce the same sentence. A report whose wording
// moves between runs is a report a controller cannot compare with the previous
// one.
//
// # Why a canceled order is not held by its outstanding amount
//
// A canceled order that was never paid has its whole total uncollected, and it
// always will: nothing will ever be collected against it. Reading
// that as unsettled would make cancellation a PERMANENT refusal to forget
// somebody, which is the opposite of what a cancellation means. The module
// already settled this question once, in queries/spending.sql: a cancellation
// means "this purchase did not happen", so the amount is not owed by anybody
// and does not enter the sum. The same reading is applied here.
//
// An archived order, by contrast, is a COMPLETED order that left the daily
// lists, so money outstanding on it is money genuinely owed and is reported.
func (c OrderErasureCandidate) Unsettled() UnsettledFact {
	// A record no surface can reach is not being performed; see [Deleted].
	if c.Deleted {
		return Settled
	}
	if c.Status == OrderPending {
		return UnsettledPending
	}
	if c.Owed() != 0 && c.Status != OrderCanceled {
		return UnsettledOutstanding
	}
	if c.ReturnRequested {
		return UnsettledReturnRequested
	}
	if c.ExchangeRequested {
		return UnsettledExchangeRequested
	}
	if c.ClaimRequested {
		return UnsettledClaimRequested
	}

	return Settled
}
