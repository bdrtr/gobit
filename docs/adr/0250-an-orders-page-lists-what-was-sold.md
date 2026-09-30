# ADR 0250 — An order's page lists what was sold

**Summary:** The panel's order page reads the order's lines through the order
module's line entity and prints them in the order they were written, each add-on
under its line; the entity lists one order's lines in that order.

- **Status:** Accepted
- **Date:** 2026-09-30

## Context

The panel's order page printed the order's status, its addresses and five
amounts, and nothing about what was bought. D96 found that the reason its
comment gave was false: the order module offers its lines to the read layer as
an entity of their own, and the sales report in the same package reads it.

That entity, filtered to one order, broke the tie between the order's lines on
the line id, so its random tail listed them in an order nobody wrote (D174).
ADR 0233 gave the order's own read of its lines `created_at, seq`, and the audit
that followed it (D161) looked for ties on `created_at`; this listing sorts on
the order's `placed_at` first and was not among them.

## Decision

The order page reads the order's lines through the line entity and prints each
with its quantity, unit price, subtotal, discount, tax and total, in the order
they were written and with each add-on under the line it belongs to. The line
entity lists one order's lines by the `created_at, seq` the order's own read
uses, after the newest sale first.

## Consequences

- The page shows what was sold and not what became of it: the payments, the
  deliveries and the quantities returned or canceled per line are records of
  their own, and the known limit now names those.
- It reads at most a hundred lines, the entity's ceiling and the most distinct
  lines a cart may carry. A full read is followed by a one-row read past it, so
  an order grown past a hundred lines by exchanges says only the first hundred
  are shown, and says so when that read fails too.
- A line read that fails leaves the order on screen and says its lines could
  not be read, as the additions already do.
- The page reads under the order screen's privilege; the sales report already
  reads the same entity under it.
- An add-on whose line was not among those read stays where it was written, and
  no line is dropped by the nesting.
- The analytics listing still pages newest sale first; `seq` is an identity, so
  a page boundary drawn through one order's lines does not move between calls.
- Each view of the page costs one more read, and a second one when the order
  has a hundred lines or more.

## Rejected

- Embedding the lines in the order entity: a record carries one id, and an
  order's lines are an unbounded set (the order provider's own reasons).
- The panel holding the order module's service: the panel knows no module
  (ADR 0011).
- Sorting the lines in the panel: it cannot know the written order, and `seq`
  is a column of the table, not a field of the entity.
- Paging the lines on the page: only an order grown by exchanges reaches a
  hundred, and a pager for it would be the page's most complicated part.
