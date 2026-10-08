# ADR 0428 — An addition's units travel as its parcel's items

**Summary:** An addition that joins its parent's pending parcel puts the units it names, or every unit it owes on one delivery, into the parcel as items it owns under its own dispatch lock, and every count of what an order's parcels hold sums the items the order owns.
It costs a migration, a join that names its units as an open does and an itemless past for joins made before, and buys a joined addition's units counted like any other.

- **Status:** Accepted
- **Date:** 2026-10-08
- **Amends:** [0197](0197-an-addition-travels-in-its-parents-parcel.md), whose parcel's items stayed the lines of the order it was opened for; [0409](0409-an-orders-parcel-holds-the-units-it-ships.md), whose count summed a parcel's items for the order it was opened for

## Context

ADR 0197 lets an addition travel in a pending parcel of the order it adds to:
`PUT /admin/v1/orders/{id}/fulfillments/{fulfillmentId}` binds the parcel to
both orders through the `order_fulfillment` link, and the parcel's items stayed
the lines of the order it was opened for. ADR 0409, ADR 0420 and ADR 0423 count
what an order's parcels hold by the reference the fulfillment module stores on
a parcel, under that order's dispatch lock, and the link is written after the
module's transaction. A joined addition's units were therefore held by no
count: the order route and the panel offered them to a second parcel, which
took them, and a write-off of the addition's line put units that were in the
box back on the shelf (D264). An open names its units, or takes every unit
owed only on an order sold one delivery (ADR 0409).

## Decision

A join puts into the parcel, as items the addition owns and under the
addition's dispatch lock, the units it names within what each line still owes,
or every unit owed when it names none and was sold exactly one delivery, and a
join of an addition already in the parcel naming nothing or the units it holds
writes no item and binds it again in any state. Every count of what an order's
parcels hold sums the items that order owns, in whatever parcel they travel.

## Consequences

- `fulfillment_items.reference` names the order an item belongs to; fulfillment
  migration 000009 gives every item already written its parcel's reference, so
  every count reads as it did.
- The open's bound, the offer on the order route and the panel, the write-off
  restock and the recount of a parcel canceled or come back count a joined
  addition's units for the addition and not for the parent.
- The route takes an optional `items` body in the open's shape and refuses as
  an open does: 422 `fulfilling_items_required` naming none on an addition not
  sold exactly one delivery, 409 `fulfillment_line_not_dispatchable` past what
  a line owes, 409 `fulfillment_nothing_owed` when the default finds none. A
  unit not named, a backordered one among them, stays owed to another parcel.
- A second join cannot add units to a parcel already carrying the addition's:
  one naming other units answers 409 `fulfillment_join_items_differ`, and
  canceling the parcel is how its contents change.
- The items commit before the link, and a link that fails answers
  `fulfilling_link_failed`; asking again while both orders are pending binds
  the addition whatever the parcel's state. A cancel or a come-back of the
  parcel recounted before then is not recounted: its written-off units of the
  addition stay off the shelf until the line's next act.
- The parent's lock is not taken: the join writes no item of the parent's, and
  the parcel's row lock orders it against a cancel or a dispatch.
- The addition's ceiling is read before its lock, as an open's is, so the
  window ADR 0430 keeps is a join's too.
- A join made before this record rode itemless; asking it again while the
  parcel is pending puts the units in. One whose parcel has left stays so.
- No carrier is called: the label already printed is the label for both.
- During the upgrade an instance on the old code fails an open, joins without
  items and misses a join's items in a write-off restock; upgrade with nobody
  opening, joining or writing off. Migration 000009 rolled back and applied
  again gives a joined addition's items the parent's reference.

## Rejected

- Counting through the link: it is written after the transaction, and the module does not read links.
- Counting by line id: which lines are an order's is the order module's answer, not readable under the lock.
- Joining without units, for the label alone: the parcel would answer for an order whose goods no count holds in it.
- Packing every owed unit on every join: a backordered unit and another delivery's goods would ride in the parent's box with no way to leave them out.
- Taking the parent's lock as well: it guards no figure the join changes, and two locks need an order to take them in.
