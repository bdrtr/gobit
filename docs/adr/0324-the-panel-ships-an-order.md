# ADR 0324 — The panel ships an order

**Summary:** An order's page opens a parcel through the order module's surface
and the fulfilling flow under `order:write`, and marks a parcel shipped with
its tracking, delivered, back undelivered or canceled through a new
`fulfillment.admin` surface under `fulfillment:write`.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The order page lists its parcels (ADR 0251), and the admin API opens one on
the delivery the order was sold (ADR 0198) and moves it through the
fulfillment module's state machine. The panel could do neither, so the
operator who packs an order left the panel to say it had shipped.

## Decision

The order's page opens a parcel through the order module's surface, which
calls the same fulfilling flow as the API with a key drawn with the page. The
fulfillment module registers a panel surface that ships, delivers, returns and
cancels a parcel, and each parcel's row offers the moves its status takes.

## Consequences

- An order is packed, handed to the carrier and closed where it is read.
- A second press of the open form, or a reload of the page it lands on,
  sends the same key and opens nothing new; the page says so.
- The parcel goes on the delivery the order was sold; another option stays on
  the admin API.
- Each move is the module's own, so the panel is refused where the API is, a
  delivered parcel's cancel included; the page offers no move on a parcel in
  a terminal status.
- Opening is the order module's privilege and moving the fulfillment
  module's, each module's write behind its own scope.

## Rejected

- A parcel's lines chosen on the page: the flow opens the order's parcel, and
  a split shipment is a decision of its own.
- Moving a parcel through the order module: the state machine and the carrier
  are the fulfillment module's.
