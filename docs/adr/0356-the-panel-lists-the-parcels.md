# ADR 0356 — The panel lists the parcels

**Summary:** A Parcels screen lists the fulfillment module's parcels across
every order through its shipment entity under `fulfillment:read`, one
status at a time, the ones still to be shipped first; the order each was
opened for is read through the `order_fulfillment` link only for an
operator who may read the orders too.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

A parcel is opened and moved on its order's page (ADR 0324, ADR 0332), so
a warehouse found the parcels still to ship only by opening every order.
The fulfillment module publishes its parcels, filtered by status, for the
order timeline, and the read layer expands the `order_fulfillment` link
from either side.

## Decision

The panel's Parcels section reads the parcels in one status a page at a
time through the shipment entity, and names each one's order through the
link when the operator may read the orders. The parcels still to be
shipped are listed when no status is chosen.

## Consequences

- A warehouse sees what is to ship, and what was shipped or came back, on
  one screen; a parcel is still moved on its order's page.
- An operator who may read the fulfillments and not the orders sees the
  parcels without their orders: the order is the order module's, read
  under its privilege (ADR 0251).
- The order is the link's, not the parcel's own reference field, which
  the module offers as a convention and never validates.
- No module changes.

## Rejected

- Moving a parcel from this screen: the moves carry a tracking number and
  a reason on the order's page, and a second place for them is a second
  form to keep in step.
- Naming the order from the parcel's reference: the fulfillment module
  says the reference is a convention, and the link is the binding.
