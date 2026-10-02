# ADR 0355 — The panel lists the taxes

**Summary:** A Taxes screen lists the tax module's tax regions through its
tax region entity under `tax:read`, a country's own and its provinces',
each with the rates it charges, the default marked, and its provider or
that it inherits one; nothing is written there.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The Regions screen shows a region's own tax rate (ADR 0354), but what a
country or a province of it charges, and under which rule, is the tax
module's, read only through the admin API. The module publishes its tax
regions for exactly this question: in which countries tax is configured.

## Decision

The panel's Taxes section reads the tax regions a page at a time, in the
module's order, through the tax region entity the tax module publishes,
and shows them to an operator who may read the taxes.

## Consequences

- No module changes: the entity publishes each region's rates with it, in
  one call.
- A rate with a rule is listed beside the default but its rule is not:
  the entity does not publish the rules.
- A province's row says the province; its country's row says the whole
  country.
- Writing a tax region or a rate stays the admin API's while the tax
  module's files wait in the Turkish ledger.

## Rejected

- Folding the taxes into the Regions screen: a commerce region and a tax
  region are two modules' records, neither naming the other, and joining
  them would put a guess in the panel.
