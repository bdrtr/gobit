# ADR 0409 — An order's parcel holds the units it ships

**Summary:** A parcel opened for an order's goods holds the items named, or every unit still owed on an order sold one delivery, and no outgoing parcel holds none.
It costs a refusal where the order owes nothing or names no items without that default, and a lock per order on every outgoing parcel.

- **Status:** Accepted; amended by [0420](0420-every-parcel-waits-for-its-orders-lock.md), whose return parcels and cancellations take its lock, and [0423](0423-a-parcel-that-came-back-holds-only-what-a-return-or-a-replacement-speaks-for.md), whose parcel that came back holds only what a return or a replacement speaks for, and [0428](0428-an-additions-units-travel-as-its-parcels-items.md), whose count sums the items an order owns in any parcel, a joined addition's among them
- **Date:** 2026-10-06
- **Amends:** [0140](0140-a-parcel-records-which-order-it-is-for.md), whose order-route parcel held no unit, and [0324](0324-the-panel-ships-an-order.md), whose page chose no line

## Context

The fulfillment module's endpoint took an item breakdown, or none; the
fulfilling flow, behind `POST /admin/v1/orders/{id}/fulfillments` and the
panel, opened every parcel with none. `committed` sums items, so for those
parcels a write-off restocked the units in the box, the dispatch bound let a
second parcel take them, and a cancel released nothing (D264), though ADR 0140
recorded the three as made true. The bound counts parcels through the order's
link, written after the parcel's transaction, and was read under no lock, so
two parcels opened at once could together hold more than the order sold
(D265).

## Decision

A parcel opened for an order's goods holds the items its request names, or, on
the order route or the panel for an order sold exactly one delivery on a
shipping option, every line's units still owed, and the module's own route
refuses an outgoing parcel that names none. Every outgoing parcel takes its
order's lock inside its transaction and may hold, per line, what the order sold
less what was written off less what the order's live outgoing parcels hold
under that lock.

## Consequences

- A second parcel cannot take units a first one holds. A write-off restocks no
  unit a parcel holds and a canceled parcel releases what it held, for every
  parcel opened from now on whose link to its order is written: both find
  parcels through that link, so a parcel whose link write failed is restocked
  by a write-off and released by nothing (D264).
- The order route answers 422 `fulfilling_items_required` for an order it
  cannot default and 409 `fulfillment_nothing_owed` for one that owes nothing;
  the module's route answers 422 `fulfillment_items_required` for no items. The
  panel bounds each line by what it owes, prefills it on an order sold one
  delivery and leaves it at zero on several, and a form naming no unit opens
  nothing.
- The default packs what is owed, not what is on the shelf: a backordered unit
  is owed (ADR 0392), and the operator lowers it.
- A retry is answered from its key and reads nothing, so a write-off between
  two presses changes no parcel.
- An after-sale replacement's parcel holds no order line, as before, through a
  call of the fulfilling flow the order route cannot reach.
- Parcels opened at once never hold together more than the order sold less what
  was written off when each read it: the later one waits on the lock and counts
  the earlier one's units by the reference this module stores, linked or not.
- A write-off landing between an open's read of what was written off and its
  write is not seen by the open, nor the open by the write-off's restock, and a
  parcel bringing a return back keeps its unlocked bound (D265 stays open for
  both). A parcel opened before holds nothing the bound counts, and an
  addition riding in its parent's parcel (ADR 0197) commits none of its units.

## Rejected

- The flow computing the default: a retry would carry a list other than its parcel's and be refused.
- Stock in the default: the bound reads the order's own records, and a parcel of what is on the shelf is the operator's call.
- The default on an order sold several deliveries: one parcel would take every delivery's goods.
- A migration filling old parcels: their contents were never recorded.
- Counting an itemless parcel as the whole order: a partial shipment would commit units it does not hold.
- A difference against the link-counted bound under the lock: a committed parcel not yet linked, or a cancel and an open in between, escape it.
- Reading the bound inside the transaction: it asks another module for a second connection while this one's locks are held (ADR 0135).
