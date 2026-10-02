# ADR 0357 — The panel lists the payments

**Summary:** A Payments screen lists the payment module's collections
across every order through its collection entity under `payment:read`, one
status at a time, the authorized ones first; the order each was taken for
is read through the `order_payment` link only for an operator who may read
the orders too.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

An order's page shows its payment (ADR 0251), so the money held and not
yet captured, or paid offline and not yet recorded, was found only by
opening every order. The payment module publishes its collections,
filtered by status, and the read layer expands the `order_payment` link
from the collection's side.

## Decision

The panel's Payments section reads the collections in one status a page at
a time through the collection entity, each with its amount and how much was
authorized, captured and refunded, and names each one's order through the
link when the operator may read the orders. The authorized collections are
listed when no status is chosen.

## Consequences

- Money held and still to be captured or recorded is on one screen; it is
  captured or recorded on the order's page.
- An operator who may read the payments and not the orders sees the
  collections without their orders (ADR 0251).
- The order is the link's: the collection's own reference carries the
  cart's id, not the order's.
- No module changes.

## Rejected

- Opening on the collections awaiting a payment: a collection awaits while
  its shopper's session is open, which is the checkout's moment, not an
  operator's task.
