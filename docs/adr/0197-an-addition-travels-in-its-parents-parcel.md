# ADR 0197 — An addition travels in its parent's parcel

**Summary:** An addition joins a pending parcel of the order it adds to, when it
goes to the same address or records none, and the parcel is then bound to both
orders. It costs the link's one-order-per-parcel index and one-way release
compatibility, and an addition no longer needs a parcel and a label of its own.

- **Status:** Accepted
- **Date:** 2026-09-26

Measurement: [measurements/0197](../measurements/0197-one-box-two-orders.md)

## Context

ADR 0192 left an addition to ship in parcels of its own. A parcel was bound to
exactly one order: `order_fulfillment` was one to many, with a unique index on
the parcel side, and the parcel's items were checked against that order's
lines. A customer who added a unit to an order still in the warehouse got two
boxes and the shop two labels.

## Decision

`PUT /admin/v1/orders/{id}/fulfillments/{fulfillmentId}` binds an addition to a
pending parcel of its parent, after the order module confirms both orders are
pending and the addition goes to the parent's address or to none. The
`order_fulfillment` link is widened to many to many (ADR 0116), and the parcel's
items stay the lines of the order it was opened for.

## Consequences

Nothing reaches the carrier: the parcel's destination is the parent's address,
which is the addition's too, so the label already printed serves both. The
parcel is among the addition's shipments and on its timeline, and its status is
the addition's shipping status. Joining twice binds nothing new. A shipped,
delivered, canceled or returned parcel takes no one.

The addition's goods ride as an itemless share, the shape the order endpoint
opens every parcel in. The dispatch bound still counts only the parcel's own
order's lines.

While the parcel waits, neither order's shipping address can be corrected: both
are bound to a parcel whose carrier holds the address (ADR 0195). The addition's
address is compared with its parent's field by field, metadata included, and a
difference refuses the join. The check and the binding are not one transaction
across the two modules; a correction written between them is ADR 0195's window
seen from the other side.

What the unique index held, one order per parcel, is held by this route alone:
it binds only an addition of the parcel's own order. When a parcel is canceled,
each line it held is put back against the bound order that has the line, rather
than against the first order the link names, which since this record may be the
addition.

A release carrying this record widens the link at startup, and the release
before it then refuses to start against the same database (ADR 0116).

## Rejected

- **Opening the addition's own parcel as part of the parent's.** The carrier
  would print a second label for the same box.
- **Binding any two orders of one customer.** The index's guarantee would be
  gone with nothing holding it; an addition is the relation that names its
  parent.
- **Putting the addition's lines among the parcel's items.** Items are checked
  against the lines of the order the parcel was opened for, and naming another
  order's lines would be a second bound the dispatch check has to keep and the
  parcel's cancellation has to split.
