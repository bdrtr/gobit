# ADR 0096 — A line remembers every rate that taxed it

**Summary:** A line's per-rate tax breakdown travels from tax through the cart
and the checkout into `order_line_taxes`. It costs a second table and one
schema change on every hop, all in one commit, because two of the boundaries
drop unknown fields in silence.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

ADR 0095 let a rate stand on another, so a line can be taxed by several rates at
once. The line carries ONE of them — the stack's base — and that was written
down as a known limit: the figure is a rate really applied on an amount really
recorded, but a line taxed at 5% + 8% reports "5%", and the customer's own
arithmetic disagrees with it. An invoice states every rate it charged, and in
Turkey the KDV rate is a required field on an e-fatura rather than a derived one.

The breakdown cannot be recovered later. Each component is floored on its own
base, so recomputing the split from the line's total gives different numbers,
and one of the two would be what the customer paid.

It also cannot travel one hop at a time. The order ignores unknown fields by
design, and so does the reader that decodes an order into a document; a
breakdown sent before the receiving end knew it would be dropped in silence.

## Decision

The per-rate breakdown travels with the line from where it is computed to where
it is stored: tax returns it, the cart and the checkout carry it, and the order
persists it in `order_line_taxes`, one row per component with its position.
Every schema on that path changes in ONE commit, because two of the boundaries
drop what they do not know without saying so.

## Consequences

The line keeps its own `tax_rate_bps` and the list stands beside it. Every
reader written before stacking keeps giving a true answer; only a reader that
prints a document has to learn the new field. The cost is two ways to ask
"what rate", and the answer is that the line's rate is the stack's BASE and the
list is the whole of it.

A breakdown is present only when a stack taxed the line. A list of one would
repeat what the line already says and would cost a row per line in the module's
largest table; its absence is what lets a reader take "there are components" to
mean "a stack taxed this line".

The identity — the components add up to the line's tax — spans rows, so no CHECK
can hold it. It is validated at every boundary the breakdown crosses (tax, cart,
order) rather than once, because each of those boundaries is where a document
could start printing figures that disagree with the amount charged.

Position is stored here although the tax module derives it. What arrives is an
array and the chain that ordered it is gone; a compound component's base is
everything below it, so the order is part of the record.

The invoice still prints the line's base rate: this record carries the breakdown
as far as the order, and the document chain is the next step. The limit stays
written down, narrowed to that hop.

## Rejected

- **A JSONB column on the line** — no CHECK can see inside it; the rate range and
  a component taking more than its base would have moved into the service alone.
- **Replacing the line's rate with the list** — every existing consumer would
  have had to change in the same breath to keep giving a correct answer.
- **Always writing the breakdown, even for one rate** — doubles the module's
  largest table to record what the line already says.
- **Re-deriving the components when a document is printed** — the split is
  floored per component; a recomputation is a different claim from the charge.
- **Landing the receiving ends first, senders later** — correct against silent
  drops, but it would ship a table with no producer and a schema no caller fills.
