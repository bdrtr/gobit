# ADR 0135 — A parcel cannot hold more than the order owes

**Summary:** The fulfillment module asks a flow how many units of each line an order
still owes and refuses a parcel that exceeds it. It costs one read on the order
module, one method on the fulfilling flow and a lazily resolved dependency pointing
upwards, and closes an admin endpoint that validated nothing.

- **Status:** Accepted
- **Date:** 2026-09-11

## Context

`POST /admin/v1/fulfillments` takes a line identifier and a quantity per item, and
`normalizeItems` refuses a blank identifier, a quantity outside a global range and the
same line twice. Nothing else — read rather than assumed: not that the line belongs to
the order, not that the quantity is within what was sold, not that units of it are
already in another parcel, and not that they were written off.

So an operator could ship goods a customer had been told were canceled, and since ADR
0134 those units' stock goes back on the shelf — the same goods left twice, the count
short by the difference, nothing failed and nothing logged. ADR 0134 named this
unclosed; measuring it found the hole wider than cancellations.

The module cannot check any of it: it does not know the order module
(Principle 2.1/2.4) and its own record says the reference it carries is free text it
never validates.

## Decision

The fulfilling flow answers `DispatchableQuantities(orderID, lineIDs)` as
`bought − canceled − committed`, and the fulfillment module resolves it by name at
request time and refuses an item over its line's answer or on a line the answer omits.

## Consequences

The endpoint stays where integrators found it, and the cross-module decision lives in
a flow the module resolves lazily — the shape the cart module uses for pricing, with
the circle broken the same way: the flow is built after every module registers, so
resolution happens on first use.

It fails CLOSED: a bound that cannot be read is not a bound, so an unresolvable flow
refuses the parcel — ADR 0007's row for an unconfigured authenticator, and the cart
module's answer for a missing pricing flow. The module's own tests had to gain a
bound before they passed again, which is that direction being real rather than
asserted.

The check runs BEFORE the transaction, because asking another module while holding this
one's locks takes a second connection from the same pool (measured in ADR 0130).

A RETRY asks nothing. The idempotency key is looked up first: a key that already names
a fulfillment is answered with that shipment, whose units are already counted as
committed, so re-checking would refuse the very request that must succeed. A retry
carrying a different item list is guarded by the mismatch check the transaction makes.

Returns are not subtracted: a returned unit shipped, came back and was restocked on
receipt, so whether it ships again is a new decision. And a line the answer omits is a
refusal rather than an unbounded quantity — the flow answers only for the lines asked
about, because zero could not be told apart from a line that is fully shipped.

NOT closed: a parcel already open is not re-checked when a line is written off after
it. The cancellation is recorded, its stock goes back, and the parcel still holds the
units — a real state a shop resolves by canceling the parcel, and a framework that
withdrew somebody's shipment on its own would be deciding what the shop has to.

Measurement: [measurements/0135](../measurements/0135-what-a-parcel-may-hold.md)

## Rejected

**Validating inside the fulfillment module.** It would have to know the order module,
which is the isolation this repository is built on.

**Moving the endpoint to the flow.** ADR 0113's lesson: a published admin route is a
promise, and integrators had no part in this defect.

**Subtracting returns as well.** It bounds a parcel by goods that already left
once and came back, which is a new decision rather than an outstanding quantity.

**Answering for the whole order.** The map would grow with the order and say nothing
more; the caller is opening one parcel.
