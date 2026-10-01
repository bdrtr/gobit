# ADR 0309 — A variant takes a price in the panel

**Summary:** The product module's admin surface sets a variant's base price in
a currency, creating and linking its price set first when it has none, as an
import does. The variant page offers it for the shop's currencies the variant
has no price in, to an operator holding `product:write` and `pricing:write`.

- **Status:** Accepted — amends [0307](0307-the-panel-creates-a-product-and-its-variants.md)
- **Date:** 2026-10-01

## Context

ADR 0307 let the panel create a product and its variants and left their prices
to the admin API, because a variant without a price set needed a set made by
pricing and a link made by the product module. ADR 0207 had already met that
for the catalog import: the product module holds a narrow price writer that
pricing's service satisfies by name, creates an empty set when a variant has
none, links it, and sets the base prices. The variant page's price form edited
a base price that existed and nothing else.

## Decision

The product admin surface gains `PriceVariant`, the import's act for one
currency of one variant. The variant page offers a currency the variant has no
base price in, among the currencies the shop's regions use, with an amount, to
an operator holding both writes, and returns to the variant.

## Consequences

- A product created in the panel is priced there; with stock it is ready to
  publish.
- The price and its set are pricing's and the link the product's, so the form
  asks for both writes, as an import with price columns does.
- A currency is offered only when the variant has no base price in it; an
  existing price is changed on its own form, which carries the amount it was
  drawn with (ADR 0280).
- A write stopped after the set is created and before it is linked leaves an
  empty set nothing names, as an import's does; the next price creates
  another.
- An installation without pricing refuses the write.

## Rejected

- A catalog flow of its own for the first price: the product module already
  carries the import's price writer, and a second path would drift from it.
- Requiring only the product's write: the price is pricing's, and an operator
  granted products alone would set prices through it.
