# ADR 0134 — Canceled units go back on the shelf

**Summary:** The order module publishes `order.line_canceled` and a new flow puts the
unshipped units back, idempotently on the cancellation's id. It costs a sixth ledger
reason, a reference column and the first subscribing flow, and buys a shop that
stops losing stock whenever it writes a line off.

- **Status:** Accepted
- **Date:** 2026-09-11

## Context

The checkout's last step confirms the reservations, which DEDUCTS the stock. So an
order that exists has had its units taken off the sellable figure, and a line
written off afterwards is a unit nobody will send and nobody counts as stock.

Nothing put it back. `CancelOrder` touches no stock and ADR 0113's partial
cancellation touches none either — the order module cannot, because the units live
in another module (Principle 2.1/2.4). `order.canceled` is a TIMELINE kind and not
an event, so there was nothing to subscribe to, and no caller of `Restock` exists
outside the returns flow, which is driven by goods physically arriving. Gap D70.

Which units may come back is not the canceled quantity: units in a live parcel have
left, the fulfillment module records that per line, and the order may not ask it.

## Decision

The order module publishes `order.line_canceled` — outbox row inside the
cancellation's transaction, direct publish after it — and a new flow subscribes, asks
fulfillment how many units a live parcel holds, and puts back the increment of
`min(canceledTotal, bought − committed)` through an inventory entry point keyed on
the cancellation's id.

## Consequences

The endpoints do not move: ADR 0113's two admin routes are a published promise (ADR
0026), and a second route beside them would leave a shop with two ways to cancel, one
of which loses stock. So the order publishes and a flow listens — the FIRST here that
only listens, with no endpoint, no interop and no caller. Modules subscribed before;
no flow had needed to, because until now no consequence of one module's write reached
two others.

Stock returns are IDEMPOTENT for the first time, and had to be: the bus delivers at
least once while adding stock is deliberately not idempotent elsewhere, because two
restocks mean two arrivals. The cancellation's id is the movement's reference and the
ledger holds it unique, so a redelivery is refused by the DATABASE.

A cancellation is a sixth ledger reason rather than a positive adjustment: the
arithmetic is a restock's and the fact is not — nobody counted anything and nothing
arrived — and the ledger exists to tell those apart (ADR 0068).

`ConfirmReservation` now names the order and the sale movement carries it, which is
what makes the way back one read: the reservation knew the location and is keyed to
the CART's line item, which an order does not carry.

The event carries FIGURES where ADR 0121 refused to, and that record's reason was
specific: a refund is not idempotent, so an amount would be an increment of an unknown
base. A cancellation row is written once, so its counts are facts of that row. What it
cannot carry is how many units shipped — that changes after the event, so the flow asks.

NOT closed: a dispatch can still ship units that were canceled. ADR 0113's ceiling
does not know about parcels and this record does not change it — but a cancellation
that put stock back followed by a dispatch that sends the goods leaves the count
short, and naming that beats a guard in the wrong layer.

Measurement: [measurements/0134](../measurements/0134-the-stock-of-a-canceled-unit.md)

## Rejected

**Moving the cancellation endpoints into a flow.** It breaks a published promise
for a reason integrators had no part in.

**A positive `adjustment` for the units.** It reads as a warehouse correction, and
an operator explaining a month's stock cannot tell the two apart.

**Putting the units back without asking fulfillment.** Goods in a parcel have left,
so returning their stock invents units the shop does not have.

**Restocking to a location the caller names.** Nothing arrives, so there is no
arrival to name one — the right shelf is the one the units left.
