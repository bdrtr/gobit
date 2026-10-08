# ADR 0433 — A return gives back at most what its units were sold for

**Summary:** A refund the returns flow makes names its record and a ceiling, and the payment module refuses it under the collection's lock when the refunds naming that record would pass the ceiling.
It costs a ceiling on the payment surface, and shipping goes back as a credit and a payment refund; a return's refunds stop at what its units were sold for.

- **Status:** Accepted
- **Date:** 2026-10-09
- **Amends:** [0187](0187-a-refund-names-its-cause.md), whose payment module now holds a cause's refunds to its caller's ceiling; [0271](0271-the-panel-acts-on-an-orders-after-sales.md), whose empty refund means what the return has left; [0272](0272-the-panel-opens-an-orders-after-sales.md), whose return form names a line

Measurement: [measurements/0433](../measurements/0433-what-a-return-gives-back.md)

## Context

A return's refund checked that its goods were received and that no exchange
took them back, then refunded the amount asked from the order's collection,
where zero meant everything the collection held. Nothing compared a refund
with the return: a second refund paid again, and zero on a one-unit return
paid the whole order (D269). A consumer building a marketplace on gobit met
it, and the panel's refund form, offered on a received return after every
refund, sends zero for an empty box. The payment module's godoc said the flow
refunds a return once. A return's planned figure is zero on every return a
shopper opens and nothing updates it; what its units were sold for is the
figure ADR 0432 values returned units at and ADR 0406 documents a return's
refund on. Every refund row names its cause (ADR 0187).

## Decision

Each refund the returns flow makes names its record and a ceiling, what a
return's units were sold for, the amount a claim's settle asks or an
exchange's difference, and the payment module refuses it, under the
collection's lock, when the refunds naming that record would add up past the
ceiling. Zero asks a return for what is left under its ceiling and a claim
for its own figure.

## Consequences

- A refund above what a return has left, and any refund once nothing is left,
  answers 409 `returns_workflow_refund_exceeds_return` and moves nothing; zero
  refunds what is left as far as the collection holds.
- The refunds naming a record are summed after the collection's lock and
  before the provider is called, so two refunds of one record at once give
  back at most its ceiling; a return's and a claim's refunds all come out of
  the order's one collection.
- A claim keeps an amount typed above its figure, and a refund already naming
  it refuses the next: a settled claim is refused by its status, and one still
  requested, whose stamp failed or which met another settle, moves nothing
  and is recorded settled. An exchange's refund names its difference, which
  its collection already held to.
- The return record publishes its `lines` and `sold_for`; what its refunds
  gave back stays the payment module's to say.
- Shipping given back with a return, and the money of a return that names no
  line, are a credit (ADR 0388) and the payment module's refund route. A
  return without lines answers 409 `returns_workflow_return_names_no_line` on
  its refund, and the order page refuses a return form that names none.
- A refund made before ADR 0187 names no return and is not counted. A return
  refunded past its units before this record refunds nothing more and keeps
  what it paid.
- A credit or a claim's refund given for the same goods is not taken off, as
  in ADR 0432, and a partial amount sent twice pays twice up to the ceiling.
  A line returned in parts gives back up to one minor unit per unit less than
  it sold for, since each return's share is rounded down.
- `RefundCollection` on the payment surface takes the ceiling and requires the
  cause, and a `payment.interop` registered without it fails the returns flow
  at startup.

## Rejected

- The return's planned refund as the ceiling: zero on a shopper's return, updated by nothing, held only to the order's total.
- Checking what is left in the flow before refunding: a refund in flight is not yet a refund, so two could pass.
- Holding the return's row lock across the refund: a second connection under a held one (ADR 0130, 0135).
- A running refunded total on the return: a copy of the payment module's figure (ADR 0119).
- Shipping inside a return's ceiling: one return could give back carriage another already gave.
- Holding a claim to its figure: a refund claim may be opened at zero, and its document falls on every row.
- Refusing a return without lines in the API: ADR 0272 keeps it as the service's shape.
- A token on the panel's refund form (ADR 0388): the ceiling bounds the repeat.
