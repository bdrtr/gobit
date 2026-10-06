# ADR 0422 — The badge counts the warehouses that serve the region

**Summary:** A storefront catalog read may name the shopper's region, and the badge, the in-stock filter and the restock date then count only the warehouses the checkout would rank for it.
It costs one fulfillment read per page that names a region, and the badge stops promising stock the region's checkout refuses.

- **Status:** Accepted
- **Date:** 2026-10-06
- **Amends:** [0040](0040-in-stock-is-a-catalog-answer-over-an-inventory-fact.md), whose answer left the region out, and [0093](0093-the-badge-counts-the-channels-warehouses.md), whose narrowing was the channel's alone

## Context

ADR 0040 kept the region out of the in-stock answer because nothing asked which
warehouses serve which region. ADR 0010's bonds now answer it at the checkout:
a warehouse bound to other regions is not ranked, and a cart in a region no
stocked warehouse serves is refused with `fulfillment_no_serviceable_location`.
The badge, narrowed to the channel's warehouses by ADR 0093, still counted that
stock, so a page said in stock where the till would not ship it (D267). The
storefront knows its region.

## Decision

A storefront catalog read may name the shopper's region, and with it the stock
badge, the in-stock filter and the restock date count only the warehouses the
checkout would rank for that region among those the request's channels ship
from. Without a region the reads answer as before.

## Consequences

- `region_id` on the listing and the single product read, `regionId` on the
  GraphQL `products` and `product` queries. The id is not looked up.
- The answer is fulfillment's own ranking, asked once per page for the
  warehouses the breakdown names and once more for a restock date's warehouse
  the breakdown left out. A warehouse bound to no region serves every region,
  so a region no warehouse is bound to counts the unbound ones.
- When the ranking fails the read keeps the channel's answer and logs, for
  the badge and the restock date alike, as a failed channel read does; an
  installation without fulfillment narrows by nothing.
- A page that names a region reads the per-warehouse breakdown even when no
  channel binds warehouses.
- Related products, add-ons, search and the stock alert name no region and
  count the channel's warehouses: a stock mark records no region, and a
  shopper's region on it reopens this for the alert.
- The badge still sums a variant's units across the counted warehouses while
  the checkout reserves a line at one at a time (ADR 0040, ADR 0093).
- A cache that drops query strings would mix regions; one keyed on the URL
  does not.

## Rejected

- The region in the catalog path: a channel is a gate the key decides, a region a narrowing the client chooses.
- Validating the region id: one more read per page for the answer the checkout gives anyway.
- Failing closed when the ranking fails: a transient fault would take every product off sale.
- Inventory filtering by region: it does not hold the bonds (ADR 0010).
- A stored availability per region: stale the moment stock moves (ADR 0093).
- A region on the stock mark now: a new column and write for a consumer the storefront's own page already serves.
- Taking an order's lines from one warehouse: the checkout reserves per line; reopen when a shop needs one shipment per order.
- A priority per warehouse and region: priority is per warehouse; reopen when a shop needs A first for one region and B first for another.
