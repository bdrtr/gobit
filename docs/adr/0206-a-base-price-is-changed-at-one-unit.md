# ADR 0206 — A base price is changed at one unit

**Summary:** Pricing's base-price write changes the one price in a currency
that is on no list, carries no rules and covers one unit, and the panel offers
a form for that price alone. It closes D143, where a tier took the amount too.

- **Status:** Accepted
- **Date:** 2026-09-27

Measurement: [measurements/0206](../measurements/0206-one-price-at-one-unit.md)

## Context

The panel's price form names a currency and nothing else. The write behind it,
`SetBasePriceAmount`, set every base price in that currency to the amount, so
an edit of the price at one unit overwrote a price for ten or more. The page
gave every price the read layer lists such a form, a quantity tier's and an open
list's among them, and saving one of those changed the base price instead
(D143). The catalog import's price columns need the write this form should have
had: the export writes ADR 0041's price at one unit, and an import has to put
back that price and no other.

## Decision

Pricing's base-price write changes, in each currency it is given, the price on
no list, with no rules, covering one unit; it adds one below the currency's
lowest tier when there is none, refuses when there are two, and writes nothing
when the amount already stands. The panel's variant page offers a form for that
price alone and lists every other price with the quantities and list it
applies to.

## Consequences

A quantity tier, a list price and a price with rules keep their amounts. A
price added to a currency priced only from ten units runs from one to nine; to
a currency with no base price, from one upwards.

Pricing allows overlapping quantity ranges, so a currency can hold two prices
at one unit. The write then answers 409 `pricing_unit_price_ambiguous` and
writes nothing, and the page shows both without a form; the price set's own
endpoint changes them.

An amount that already stands is not written. The write replaces the whole set
and regenerates its price ids, so an unchanged save no longer does that.

A set with prices only from ten units, or only on lists, shows no form. The page
says the price at one unit is added through the admin API, as it said for an
empty set.

The write takes several currencies at once, and stays unexported until the
catalog import's price columns call it.

## Rejected

- **The form naming its price by id.** The write regenerates every id on the
  set, so a form rendered before another save would name nothing.
- **A form per tier.** It needs a quantity range in the form and a write that
  matches ranges; nothing has asked to edit tiers from the panel.
- **Taking the narrowest of two prices at one unit.** The calculation charges
  it, but the export and the catalog filter read any price at one unit, so the
  write would change one those two may not show.
