# ADR 0097 — A document prints every rate it charged

**Summary:** The per-rate tax breakdown finishes its journey: the invoicing flow
carries it out of the order and the invoice module stores it in
`invoice_line_taxes`. It costs a fourth copy of the same five fields, and it
closes the limit ADR 0095 opened.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

ADR 0096 carried the breakdown as far as the order and said the document chain
was the next step. Until it was taken, a row charged under 5% + 8% printed "5%".
The figure is a rate really applied on an amount really recorded, and it is
still not what the buyer paid under; in Turkey the KDV rate is a required field
on an e-fatura rather than a derived one.

Two boundaries stood between the order and the printed row and both ignore what
they do not know: the invoicing flow decodes an order into a document, and the
invoice module accepts that document. A breakdown sent across either before it
was taught the field would have been dropped without a word, and every total
would still have added up.

## Decision

The invoicing flow reads the order's `tax_components` and puts them on the
document row, and the invoice module validates and stores them in
`invoice_line_taxes`, one row per component with its printed position. Both ends
learn the field in the same change.

## Consequences

The same five fields now exist in four modules and three workflows. That is the
price of Principle 2.1: no module imports another, so a schema that crosses a
boundary is copied and converted at it. Each copy is a place the shape can drift
and each conversion is where a rename stops being silent.

The identity — the components add up to the row's tax — is checked a fourth
time, and this is the last one: what passes here is printed and handed to a
buyer.

Position counts from ONE here and from zero in the order. The invoice module's
rows are printed and its first row is row 1; a sibling table counting from zero
would make one reader answer "which is first" two ways. The conversion happens
where the two meet.

The breakdown is part of the retained document, so it is guarded like one: the
new table takes the delete and truncate refusals ADR 0032 put on the other two,
and the sanctioned escape becomes three pairs of statements instead of two. The
three refusal functions are REPLACED rather than left naming two guards, because
a hint that lists two of three sends an operator into a transaction that fails
halfway.

Nothing had been asking that question. A gate now derives the population by
walking the foreign keys out of `invoices` and requires a guard on everything it
reaches, so the next table cannot arrive unguarded (D49).

The limit ADR 0095 opened is closed and drops out of `known-limits.md`.

## Rejected

- **A JSONB column on the row** — the rate range and "a component cannot exceed
  its own base" would have left the database and lived only in the service.
- **Re-deriving the breakdown when the document is rendered** — each component
  was floored on its own base; a recomputation prints figures nobody charged.
- **Asking tax for the rates at print time** — a document states what was
  charged, and a rate table changes.
- **Counting positions from zero for symmetry with the order** — symmetry
  between modules is worth less than agreement inside one that prints.
- **Leaving the new table outside the retention guard** — the foreign key stops
  a TRUNCATE and stops no DELETE; the row would keep its total against
  components that no longer exist.
