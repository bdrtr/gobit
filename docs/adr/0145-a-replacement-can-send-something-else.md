# ADR 0145 — A replacement can send something else

**Summary:** A replacement item may name a product VARIANT instead of an order
line, so an exchange can send a different size or colour. It costs a nullable
column and a rule that the bought-ceiling no longer covers every item, and it
gives the money half of an exchange something to answer.

- **Status:** Accepted
- **Date:** 2026-09-12

## Context

Every after-sales item in the order module points at an existing line with a NOT
NULL foreign key — a return item, a cancellation, a replacement item. That is
right for the records that talk about goods the customer already has.

It is wrong for a replacement. "Send me the same shirt a size larger" is the
ordinary exchange, and the only thing `order_replacement_items` could express was
units of the EXACT variant already on the order. An exchange has been able to take
a difference in money since ADR 0120 — its own collection, funded before dispatch
— and there was nothing for that money to answer: the goods it paid for could only
be goods the order already sold.

The feature list marks exchanges done on that basis. What the row does not say is
that the exchange can only send the same thing back.

Measurement: [measurements/0145](../measurements/0145-what-an-exchange-could-not-send.md)

## Decision

`order_replacement_items` gains a nullable `variant_id`, `order_line_item_id`
becomes nullable, and a CHECK holds that exactly one of them is set. An item that
names a variant is bounded by the exchange's money guard rather than by what the
order bought.

## Consequences

Nothing downstream changed, and that is the measurement's finding rather than
luck: the dispatch flow already worked from a variant id — it held stock from
`detail.Lines[i].VariantID` — and the only reason the row had a line at all was
that the document derived the variant by joining the order's lines. A variant item
carries its own answer, so the join is skipped for it and kept for the others.

The two bounds are now different and that is the cost. A line item cannot promise
more than was bought; a variant item has no such number, because the order never
sold it. What stands in its place is ADR 0120's guard — a dispatch is refused
until the difference is funded — and that difference is a figure the operator
TYPES. So the goods and the money are checked against each other only as far as a
human's arithmetic goes, which is written into `known-limits.md` rather than
implied.

The uniqueness became partial and gained a sibling. `NULL` is not equal to `NULL`
in a unique index, so leaving the old one alone would have let a replacement carry
the same variant twice while still refusing the same line twice.

The sum that feeds the ceiling excludes variant-shaped rows in SQL rather than in
Go, and the predicate is a statement rather than a filter: counting such a row
would attribute goods the order never sold to a line chosen by nothing.

Three mutations bit. The one that survived first is worth naming: restoring the
join in the detail document broke no test, because nothing exercised the document
with a variant item — and the document is what the dispatch flow reads, so a
variant replacement would have failed at dispatch with a message about a line it
does not have. Its test exists now.

## Rejected

- **A second table for variant-shaped items.** The two are one fact — this many
  of this thing goes out — and two lists would give the dispatch two things to
  merge and the ceiling two places to drift apart.
- **Write a new `order_line_items` row instead.** The order's total and its issued
  invoice are immutable (ADR 0024), so a line added after placement would put the
  invoice in silent disagreement with the order.
- **Price the variant item and check it against the difference.** It needs a
  pricing read at settlement time and a decision about which price applies to a
  replacement; the money guard already refuses an unfunded dispatch, and making
  the figure exact is a separate record.
