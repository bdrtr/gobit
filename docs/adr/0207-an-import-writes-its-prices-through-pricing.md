# ADR 0207 — An import writes its prices through pricing

**Summary:** A catalog import's price cells set each variant's base price at
one unit through pricing's service, giving a variant without a price set one.
A file with price columns takes `pricing:write` as well as `product:write`.

- **Status:** Accepted
- **Date:** 2026-09-27
- **Amends:** [0205](0205-a-catalog-import-is-a-record-a-job-works-through.md), whose price columns were read and not applied

Measurement: [measurements/0207](../measurements/0207-prices-in-a-file.md)

## Context

ADR 0205 kept the export's `variant_price_<currency>` columns and applied none of
them. Pricing owns a price, and the product module owns the link from a variant
to its price set. ADR 0206 gave pricing a write that changes the price at one
unit and nothing else, which is the price the export writes. What was left was
how the product module reaches pricing, what a variant without a price set
gets, who may send prices in a file, and what an installation without pricing
does with one.

## Decision

A row's non-empty price cells set its variant's base price at one unit through
pricing's service, which the product module resolves by name on first use, and
a variant with no price set is given an empty one and linked to it first. A file
with price columns takes `pricing:write` beside `product:write`, and is refused
with 422 when it is sent to an installation without pricing.

## Consequences

A cell holds whole minor units, as the export writes them. A cell that is not a
whole number, or a price on a row that names no variant, refuses the row before
it writes anything. An empty cell leaves that currency as it is, so an import
cannot clear a price, and it never writes a quantity tier or a list price.

A price that already stands writes nothing and the row counts as unchanged; a
price that changes makes the row updated. A row's product, variant, price set
and prices are separate writes: a price refused after the variant changed
leaves the variant changed. A run stopped after a set is created and before it
is linked leaves an empty set nothing names, and the row, applied again,
creates another.

The scope is checked when the file is sent. The job applies it later on that
authority, and a scope withdrawn in between does not stop an accepted import.

An unchanged export now reads each variant's link and price set as it is sent
back; the measurement gives what that costs on 52,004 products.

Creating a variant still creates no price set. Pricing's godoc said the product
module did (D144); the import is that method's first caller.

## Rejected

- **A workflow between the two modules.** The link is the product module's, so
  a flow would hand back the id the module writes itself; it resolves pricing
  as it resolves the file module's read-back.
- **Pricing's `SetBasePrices`.** It replaces the set and deletes every price the
  row does not name, a tier and a campaign's among them.
- **Refusing row by row without pricing.** A catalog's file would be refused
  54,000 times for one reason.
- **`product:write` alone.** An operator allowed the catalog and not its prices
  would change prices through a file.
