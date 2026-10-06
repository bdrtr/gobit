# ADR 0413 — A return's parcel is opened from its return

**Summary:** The order page lists each return's parcels and opens one on a return option for what the return still awaits; the panel moves it like any parcel, and moves only the order's own.
A return label waits for the first carrier plugin that opens one; until then the shop buys none and the shopper is shown none.

- **Status:** Accepted
- **Date:** 2026-10-06
- **Amends:** [0384](0384-a-return-parcel-brings-back-the-return-it-names.md), whose parcel only the admin API opened, and [0324](0324-the-panel-ships-an-order.md), whose moves named any parcel

## Context

ADR 0384 bound a parcel on a return option to the return it brings back,
bounded it by what the return still awaits and kept it out of the order's
shipments, so the panel never showed one and only `POST /admin/v1/fulfillments`
opened it. A return label needs a carrier's API: the provider contract has no
verb for it, the tree's one provider is manual, and ADR 0384 declined a
return-shipment table and a provider verb named with no carrier to test them;
nothing recorded what would reopen that. The panel's move named a parcel by its
id and moved it whatever order the page was (D266).

## Decision

The order page lists each return's parcels under the return and opens one on a
return option for what the return still awaits, through the fulfillment
module's panel surface, and a move is made only on a parcel whose reference is
the order or which the order's link names. A return label, a provider verb for
it and a return-shipment record wait for the first carrier plugin whose API
opens a return label, which reopens this record.

## Consequences

- The shipment read layer publishes and filters `return_id`, and reads one
  parcel by `id`; a return parcel is found by its return, not by the order id
  it also carries.
- Opening one takes `fulfillment:write`, the API's privilege, and offers the
  units the return names less those its live parcels hold; the module's bound
  and direction check stay the authority, and the form's key opens it once.
- The options offered are the order region's return options, admin-only ones
  included; no cart stands behind a return, so an option ruled on a cart's
  facts is not offered, and the API still opens a parcel on it.
- "Mark as shipped" records the tracking number the customer or their carrier
  gave; the provider is still handed no destination and no direction.
- A move reads the parcel under its own privilege: its reference answers for
  the order route's parcels and every return's, whose return the fulfilling
  flow holds to that order. A parcel an addition joined is the addition's
  through the link, read only for an operator who may read the order; one who
  may not moves it from its own order's page.
- Another order's parcel named under this order's page answers 404 and moves
  nothing.
- The order's own parcels, its timeline and the shopper's pages still show no
  return parcel: what a shopper needs is a label.
- The trigger is testable: a carrier plugin that opens a return label brings
  the verb, a conformance case in `core/providertest` and the record.

## Rejected

- Opening it through the order module: the order chooses an outgoing parcel's delivery, and a return parcel names its return.
- Finding it by the order id: a convention the read layer warns against; `return_id` is the binding.
- Asking `order:read` of every move: an operator holding the fulfillment write alone moves the order's own parcels today (ADR 0260).
- A deferral record alone: the one act an operator can do without a carrier would stay unbuilt.
- A label verb and table now: names chosen with no carrier to test them (ADR 0384).
