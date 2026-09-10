# ADR 0119 — An order does not keep a second copy of the money

**Summary:** Where an amount collected by the payment module decides something,
it is asked of that module at the moment it decides, not mirrored on an order
row. It costs the row-local guard and buys a record that cannot be silently
falsified.

- **Status:** Accepted
- **Date:** 2026-09-10

## Context

An exchange's difference has been designed three times and six candidates were
destroyed by six independent readings. They disagreed about nearly everything
and died of one thing: each put a money figure on the exchange's row and held a
rule against it with a row-local CHECK — the shape migration 000017 argued for
when it bounded the completion.

For money that another module owns, the argument does not carry. The payment
module publishes a route that refunds a capture, it emits no events at all, and
a constraint on an order table cannot see a write to a payment table. So an
order-side copy is a claim a published route can invalidate in silence, and the
strongest guard the schema offers is the one that cannot notice.

The tree already carries such a copy. The order's summary keeps the paid and
refunded totals, written only by two flows, and a refund made through the
payment module's own route reaches neither.

## Decision

An order-side record does not keep a figure that the payment module owns as its
own truth; where such an amount decides something, the deciding flow asks the
payment module at that moment. What an order row may record about it is the
MOMENT a question was answered, never the answer's arithmetic.

## Consequences

The bound on an exchange's completion cannot be a row-local CHECK the way
migration 000017's is, and that is given up deliberately: 000017's reason was
that its fact needed only the row, and this fact does not. What the schema still
holds is what is genuinely local — that a completion carries its moment, and
that a negative difference never reaches one.

A measurement and the write that follows it are not in one transaction, because
the two modules may not share one. A refund inside that window is not seen, so a
completion states the moment it was written rather than promising every moment
after — the exposure the returns flow already accepts when it refunds first.

The order summary's totals are named for what they are: a report, not the
source. Their surface explained its own merge semantics by a subscriber
listening to payment events, and there are no payment events; the sentence is
corrected. The staleness itself is not, and its trigger is the one ADR 0022
already named — the day the payment module publishes. Gap D55.

The record that finally builds the difference starts from what three rounds
settled rather than re-deriving it: the link `order_exchange_payment` bound one
to one, a positive-only predicate, an equality rather than a floor, a structural
ceiling, a down that rewrites, and a method added rather than a signature widened.

One question is left open on purpose: a withdrawal wants to refuse while the
exchange still holds the customer's money, only the payment module can answer
that, and the withdrawal's route is on the order module.

Measurement: [measurements/0119](../measurements/0119-where-the-truth-about-collected-money-lives.md)

## Rejected

**Mirroring the amount and accepting that it can go stale.** It is what all six
candidates did, and the record then states a settlement that the money contradicts.

**Making the payment module publish, so a subscriber can keep the copy true.**
It is the honest fix and it is a decision of its own; ADR 0022 named the trigger
and this record does not pull it forward.

**A reconciliation job that reads across and repairs the copy.** It makes the
copy eventually true and leaves every reader between two repairs reading a
number that is not.

**Deciding the withdrawal guard here.** Three shapes are visible, each loses
something real, and none was measured.
