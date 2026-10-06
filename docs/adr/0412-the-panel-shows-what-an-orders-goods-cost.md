# ADR 0412 — The panel shows what an order's goods cost

**Summary:** The variant page writes a variant's unit cost one currency at a time, and the order list and order page print the placed margin and each line's cost.
It costs two reads on the order module's panel surface and a cost write that compares what it was drawn with.

- **Status:** Accepted
- **Date:** 2026-10-06

## Context

ADR 0401 put a unit cost per currency on a variant, copied it onto each order
line at the checkout, and published the line's cost and the order's placed
margin on the admin API. The panel, where an operator works, printed neither,
and its variant page offered no way to write a cost. The panel reads orders
through the read layer, whose records are maps the cost gate cannot read, and
its forms send the value they were drawn with (ADR 0280).

## Decision

The variant page reads a variant's unit costs and writes one currency's cost at
a time from the cost the form was drawn with, through the product module's panel
surface. The order list and the order page print each order's placed margin, and
the order page each line's unit cost, read from the order module's panel surface
in one call per page.

## Consequences

- The read layer's order and line entities carry no cost; the margin and the
  costs reach the panel as JSON of declared types the cost gate reads.
- The panel computes no margin: it prints the module's `placed_margin`, and a
  line's margin is not shown.
- A cost write takes the variant's row and refuses with
  `product_variant_cost_moved` when the currency's cost is not the one the
  form showed; another currency's cost is never written by the form, and an
  empty amount clears the one it names.
- An order whose lines are all gift cards, or one with a line that kept no
  cost, prints no margin and says why; a margin that could not be read leaves
  the page standing and says so.
- `order:read` reads the margin and the line costs, `product:read` the
  variant's costs, and `product:write` writes one, as on the admin API. The
  variant page reads the currency scales for the costs only where it has a
  cost to print or a form to offer.
- The sales report and the CSV export and import carry no cost.

## Rejected

- Cost fields on the read layer's order and line entities: a map the cost gate cannot read.
- A line margin computed by the panel: a second definition of the margin.
- Writing the whole cost list from the page: a currency another operator changed would be written back.
- A margin column on the sales report: it sums lines, and the margin is the order's.
- The cost on the CSV export and import: a surface with its own column rules, left to its own record.
