# ADR 0313 — The panel shows a promotion

**Summary:** A promotion's page shows what it gives, its rules, its campaign
and its latest twenty uses, read through the `promotion.admin` surface under
`promotion:read`. The uses are read newest first through a new
`(promotion_id, id)` index.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

ADR 0311 listed the promotions with their codes and usage, and ADR 0312 let
an operator publish or pause one from the list. Neither said what a promotion
does: its discount, the rules a cart must meet and its campaign are read only
through three admin API calls, and which orders used a coupon only through a
fourth, oldest first. An operator about to publish a draft, or wondering why a
cart got a discount, had no screen for it.

## Decision

The `promotion.admin` surface reads one promotion with its discount, rules,
campaign and latest twenty uses, and the Promotions list links each row to a
page that shows them under `promotion:read`. The uses are read newest first by
a new query, served by an index on `(promotion_id, id)` that replaces the one
on `promotion_id` alone.

Measurement: [measurements/0313](../measurements/0313-latest-uses.md)

## Consequences

- The page gives the discount in the shop's terms: a percentage from its
  basis points, a fixed amount in its currency's scale or, when the scale is
  unknown, in minor units and saying so.
- A promotion with no discount says it applies nothing, and one with no
  campaign or rule says that too, rather than leaving the section out.
- A use given back stays on the page, marked with when.
- "Newest first" is to the millisecond: a use's identifier carries the
  millisecond it was written in, and two uses in one are in no order.
- The latest uses of a coupon whose uses are all older than the others' are
  read without walking the table; the index serves the admin API's oldest
  first listing as well, and the old index is gone rather than kept beside it.
- The page writes nothing. Publishing and pausing stay on the list (ADR 0312),
  and editing the discount or the rules stays with the admin API.

## Rejected

- Four reads from the panel: the surface already holds the service, and one
  call keeps the contract to one JSON shape.
- Paging the oldest-first listing from its end: it needs the count first and
  reads two pages around a moving total.
- Keeping the `promotion_id` index beside the new one: the new one serves
  every read the old one did.
