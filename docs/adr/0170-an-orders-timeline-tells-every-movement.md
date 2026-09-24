# ADR 0170 — An order's timeline tells every movement

**Summary:** The order timeline has one entry per dated row the order reaches:
each capture and each refund with the amount it moved, and every line
cancellation, credit, replacement moment, exchange funding and erasure. It is
the first slice of reading an order as it stood at a past moment.

- **Status:** Accepted
- **Date:** 2026-09-25

Measurement: [measurements/0170](../measurements/0170-what-the-timeline-left-out.md)

## Context

The feature list's C8 row asks for an order's state at a past moment. Three
read-only surveys found no event log to rebuild it from. They also found that
the timeline, which claims "everything that happened to an order", did not
show what the order's own rows already record. Money came in as two entries,
the first capture and the last refund, each carrying the lifetime total, so a
refund of 1,000 on Monday and 500 on Friday read as one refund of 1,500 on
Friday. Line cancellations, credits, replacements, an exchange's funding and
the erasure were dated rows with no entry at all.

## Decision

The timeline composes one entry for every dated row the order reaches: each
capture and each refund with the amount it moved, through a `movements` field
the payment collection now offers, and every line cancellation, credit,
replacement moment, exchange funding and erasure. The storefront's allowlist
gains the goods among them (line cancellations and replacements), and the
money and the shop's own acts stay on the support desk's side.

## Consequences

An amount on an entry is what moved at that moment, and the sum of the refund
entries is the collection's refunded total. A money entry's `ref_id` now names
the payment or the refund it came from, not the collection. That is a change
to the admin API. A line cancellation carries its `quantity` on both views.

A movement the timeline cannot read fails the read instead of dropping out. A
shorter timeline is the fault this one had.

The exchange's funding is the difference it collected on its own collection,
so it is not also counted among the order's captures.

This is a history, not yet a reading at a moment. The status in some entries'
`detail` is today's. A removed claim evidence leaves no row. The money the order
itself records is overwritten. Those decide what the next slice of C8 can
answer.

## Rejected

- **A timeline table.** A second copy of rows that exist, as the timeline says.
- **Keeping the first/last entries beside them.** Two answers to one question.
- **Showing credits and funding to the customer.** They are money (ADR 0100).
- **Skipping an unreadable movement.** The history would be short in silence.
