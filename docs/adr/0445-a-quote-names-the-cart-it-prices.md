# ADR 0445 — A quote names the cart it prices

**Summary:** A calculated shipping option's provider is handed the cart a quote is asked for, its traveling lines priced as the round priced them and measured as the catalog records them, and nil when no cart was named.
It costs two published types, two catalog reads on every quote and three product fields on the read layer, and buys a carrier that prices the goods without finding the cart in a route.

- **Status:** Accepted
- **Date:** 2026-10-17
- **Amends:** [0065](0065-the-quote-input-waits-for-a-parcel-the-tree-can-measure.md), whose parcel the tree could not measure

## Context

depremtek's backend, a consumer, prices a calculated option by the desi of
each seller's goods (D300). `coreprovider.QuoteInput` carried the option, the
currency, the country, a zero weight and an item count, and no cart, so its
provider found the cart through chi's route pattern `/carts/{id}` and its
`{id}` parameter, then read the cart, each variant's product and each
product's box over the admin API. A rename of the route or the parameter
would have refused every quote and dropped the option from every list, and
a quote asked on any other path named no cart at all. ADR 0065 waited for a
variant with a box; the consumer's tariff reads the variant's weight and its
product's box, which the catalog already holds.

## Decision

`QuoteInput.Cart` carries a `QuoteCart` — the cart's id, currency and the
lines that travel, every line but a gift card's, each with its variant, its
product, its quantity, its unit price, its subtotal after discount, the
variant's weight and the product's weight, length, width and height as the
catalog records them, zero where it records none — and the cart flow fills it
on every quote it asks. It is nil on the eligibility lists that take their
facts from the caller, on an order's delivery change, and when the cart's
goods could not be read; gobit derives no parcel or desi and picks no measure
for another.

## Consequences

- A provider pricing by the goods refuses a nil cart, and its option drops out of that list as an unreachable carrier's does; the flat options stay.
- Each provider is handed its own copy of the lines.
- The cart's id is the cart's access (ADR 0439); a provider keeps it out of what it logs and returns.
- The product record of the read layer gains `length`, `width` and `height`, zero for none, as `weight` reads.
- Every quote reads the variants' and the products' measures, two catalog reads, whether or not a calculated option is listed; the reads are not channel-scoped, as a line already in the cart is not.
- `TestEveryQuoteInputFieldIsFilledByTheTree` holds `QuoteCart` and `QuoteLine` too.
- `TotalWeight` stays the zero ADR 0065 describes: summing a weight would pick the variant's or the product's for the provider.
- The district is still nowhere; the city on `CreateFulfillmentInput.Destination` is the label's.

## Rejected

- A context value carrying the cart's id: unpublished and untyped, the consumer's fault in another spelling.
- The cart's id alone: every provider would read the cart, its variants and its products back over HTTP on every quote.
- One `Weight` per line, the variant's or else the product's: ADR 0065 refused gobit choosing the parent's measure for the variant.
- A desi or volume field: one country's carriers' arithmetic in a published struct.
- The lines' tax and the cart's address: no consumer's tariff reads them.
