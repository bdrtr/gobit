# ADR 0387 — A tax rate can be tried on past orders

**Summary:** An operator asks what a tax rate at another value, or with rules
added or dropped, would have charged on a past period's orders; each line is taxed with today's tables and with the change, and nothing is written.

- **Status:** Accepted
- **Date:** 2026-10-05

Measurement: [measurements/0387](../measurements/0387-a-rate-against-its-period.md)

## Context

A promotion and a price list can be tried on a period's orders before they are
switched on (ADR 0176, ADR 0220); a tax rate could not. A tax rate has no
draft: a written value, rule or correction (ADR 0378) is charged on the next
cart, and only a ruled rate with no rule yet waits, because it matches no line.
The cart flows read orders and send the tax module what a cart sends
(ADR 0006); which rate a line gets, and its arithmetic, are the local
provider's rate table.

## Decision

`GET /admin/v1/tax-rates/{id}/trial?from=&to=` with `rate_bps`, repeated
`rule=reference:reference_id` and repeated `drop_rule` taxes every taxable line
of the period's uncanceled orders in the rate's country twice through the local
rate table, as it is and as amended, from the amount the cart sent for the line.
It reports per currency, and for the hundred largest changes, what was charged,
the baseline and the trial, writes nothing, and asks for `order:read` beside
`tax:read`.

## Consequences

- The change's effect is trial minus baseline. Both are today's tables, so the
  figure isolates the change from every rate written since the sale; `charged`
  is reported beside them and not compared.
- Which lines were sent, and at what amount, is the sale's: unit price times
  quantity less its discount, a line the order kept as a gift card left out
  (ADR 0211). How they are taxed is today's: the product and type the catalog
  gives, the region's country, the tax classes, every other rate and rule, and
  whether prices include their tax, each named in the report; no province is
  sent, as a cart sends none.
- Before any order is read the tax module refuses what a write refuses — a rule
  on a default or a stacked rate, a stack taking more than its line (refused on
  a value write since gap D237), a rate outside zero to a hundred percent — and
  what no cart reaches: a province's rate, since the cart sends no province,
  and a rate under an external provider.
- An order in another country is counted apart and not taxed; one whose region
  does not resolve to one country was taxed at the region's rate and is
  counted apart.
- `tax.interop` gains `CompareRateJSON` and the cart flows' `Taxes` surface
  gains it with it, so a registration without it fails wiring as any mismatch
  does. The flow shares the other trials' bounds, 93 days and 5,000 orders,
  and the comparison takes at most 100,000 lines, which is known and refused
  only once the orders are read; the tax module resolves the flow by name on
  first use, and the interop pins hold both directions.
- A new default rate, a rate standing on another, and a region's provider or
  inclusion are charged once written and cannot be tried here.

## Rejected

- A draft status on tax rates: every read of the rates would filter it, and a
  ruled rate with no rule already holds a new rate back.
- A body carrying a whole configuration: the question is about one rate, and a
  body describing every rate is a second tax model.
- A POST: the change is a value and at most a hundred rule keys, which a query
  string carries, and the question is still a read.
- Comparing the trial with what was charged: the difference would mix the
  change with every rate written since each sale.
- Inclusion as the order kept it: it is a setting of the region chain, and a
  baseline under another one is a figure no cart is charged today.
- Running the trial in the cart flows: the rate table and its amendment are
  the tax module's.
- Taxing through the configured provider: an external provider cannot be
  amended.
