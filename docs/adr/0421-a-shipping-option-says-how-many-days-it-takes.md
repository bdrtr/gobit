# ADR 0421 — A shipping option says how many days it takes

**Summary:** A shipping option may carry the business days its delivery takes, a minimum and a maximum, published beside its price on the storefront's listings.
It costs two columns and a field on four surfaces; gobit still computes no date and ranks no warehouse by time.

- **Status:** Accepted
- **Date:** 2026-10-06
- **Amends:** [0010](0010-depo-secim-politikasi.md), whose ranking stays coverage then priority

## Context

No record held how long a delivery takes: not the warehouse, its bond to a
region, nor the shipping option. A storefront could show when a variant is
expected on sale again (ADR 0399) but not when a parcel would arrive. A
warehouse's bond to a region is a coverage constraint, and a warehouse with no
bond serves every region, so a time on the bond would edit coverage. An option
is already a carrier's service in a region, which is where a standard and an
express service differ. gobit holds no shop time zone or calendar.

## Decision

A shipping option may carry how many business days its delivery takes, a
minimum and a maximum, and the storefront's option listings publish them beside
its price. gobit turns no day count into a date and ranks no warehouse by time.

## Consequences

- `delivery_days` is written on create and update and on the panel's forms,
  both figures or neither, 0 ≤ min ≤ max ≤ 365, and an update clears it with
  `clear_delivery_days`; a CHECK holds the same range.
- The admin record, both eligibility listings and the cart's two listings
  carry it; an option that says none carries no key.
- The days are the carrier's working days; the storefront renders them, or
  turns them into dates in its own calendar.
- An estimate is the storefront's composition: today, or the variant's
  `restock_expected_at`, then the option's days.
- The checkout reads no days and the order keeps none; an estimate is not a
  promise (ADR 0399).
- A warehouse farther from a region than the option assumes delivers later
  than shown; nothing models a per-warehouse time.
- A return option may carry days; no listing shows a return option.
- The panel's revision reads the days with the other terms, so a revision drawn
  before another operator changed them is refused (ADR 0333).

## Rejected

- A transit time on the warehouse's region bond: a bond is coverage, and a warehouse without one serves every region.
- Ranking warehouses by time: no record holds a per-warehouse time; reopen when a shop's warehouses differ to one region by more than an option's range.
- A per-warehouse handling time: the option's minimum carries it.
- A computed delivery date: gobit holds no time zone, calendar or holidays.
- The days on the order's shipping method: a promise the order would keep; reopen when a notification prints a delivery window.
- A cost axis: no carrier rate per warehouse exists; reopen when a provider plugin quotes one.
- The days in the option's `data` or `metadata`: `data` goes to the provider and `metadata` is unvalidated and unpublished on the storefront.
