# ADR 0341 — The order page writes off a line's units

**Summary:** A pending order's line writes off some of its units under
`order:write` through the order module's surface, from how many of the
line's units were asked back or written off when the page was drawn; the
module refuses under the order's lock when that count moved, so a form
sent twice writes off once.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The API writes off units of one line of a live order, a line out of stock
or dropped at the customer's request, and the units no parcel holds come
back to the shelf (ADR 0134). The panel could not, and the API's write-off
carries no key: the module refuses only a write-off past what the line
holds, so a button pressed twice would write off twice while units remain.

## Decision

A line cancellation may name how many of the line's units were asked back
or written off when the caller read the line, the count the order's
ceiling reads under its lock (ADR 0252), and the module refuses with
`order_line_moved` when it has changed. The order's page offers each line
of a pending order with units left the write-off form carrying that count.

## Consequences

- A line out of stock is written off where the order is read, with a
  reason and a note, and its units come back to the shelf.
- The same form sent twice writes off once; the second is told what the
  line has now and to draw the page again.
- A line fully spoken for, an add-on, which goes with its line, and a gift
  card line, closed in the payment module, are offered no write-off.
- The admin API's line cancellation names no count and is judged by the
  ceiling alone, as before.
- Writing off moves no money: a unit paid for and not coming is settled
  through a refund or a credit, as the module has it.

## Rejected

- An idempotency key on the write-off: a key says the form was sent once,
  and the count says the line is as the operator saw it, which is what a
  write-off decided from the page depends on.
- Offering the write-off on a completed order: its lines are past changing.
