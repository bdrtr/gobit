# ADR 0199 — A delivery can be changed before it ships

**Summary:** An operator puts a pending order's delivery on another shipping
option, at the fulfillment module's price for the order, and a cheaper one
writes the difference off as a credit booked against shipping. It costs a table
of changes beside the sold method, and a dearer change waits for a way to charge.

- **Status:** Accepted
- **Date:** 2026-09-26

Measurement: [measurements/0199](../measurements/0199-a-cheaper-courier.md)

## Context

ADR 0198 gave the order the delivery it was sold. A customer who asked for a
pickup point instead of a courier had no answer on the order: a parcel could be
opened on another option, and the order went on charging the first with nothing
recording the difference.

## Decision

`PUT /admin/v1/orders/{id}/shipping-methods/{shippingMethodId}` puts the method
on another option, priced by the fulfillment module on the order's own facts,
while no parcel of the order is pending, shipped or delivered. The change is a
row beside the method; a cheaper one writes the difference off as a credit line
in the same transaction, and a dearer one is refused.

## Consequences

The method keeps what the order was sold. Its `changes` list the options it was
put on since, and the last one is the delivery the order is on: a parcel opened
without an option goes on it. The same option again writes nothing.

The difference is computed under the order's lock against the method's latest
change, so two changes at once are priced one after the other.

A cheaper delivery's credit line carries the reason `delivery_change` and notes
the change. What the order owes falls by it, and money already paid goes back
as any credit's does, by a refund (ADR 0105). The order journal books it as a
`delivery_changed` entry under the change's id that debits shipping rather than
credit_allowances.

The quote reads the sale as the cart's quote did, the goods after discount and
the units, with the country of the current shipping address, which ADR 0195
holds to the country the order was placed in. Admin-only options are quoted,
since the operator chooses; return options are not. The quote and the write are
not one transaction, and the price written is the one the operator was quoted.

A dearer change is refused with `order_delivery_costs_more`. Charging it needs
money taken against a placed order, which today only an exchange records, on a
collection of its own and outside the order's books (ADR 0188). That record, and
the widening of this table's CHECK, is the next one.

The timeline dates each change with `order.delivery_changed`, which the
customer sees as well. A parcel's carrier is never told of a change, which is
why a live parcel refuses one, as it refuses an address correction; a canceled
or returned parcel does not stand in the way.

## Rejected

- **Editing the method in place.** The sold delivery is what a dispute starts
  from, and the order's records are added to rather than rewritten.
- **The price from the request.** The cart's quoted flow exists because a
  shopper could post an option with an amount of zero; the operator names the
  option and the fulfillment module names the price.
- **The credit as a credit allowance.** A cheaper service is shipping the shop
  did not earn, and the books would call it a concession.
- **Taking a dearer change and leaving the order owing.** After payment nothing
  could collect the difference, and the order would owe it for good.
- **Refusing only once a parcel has shipped.** A pending parcel was already
  handed to its carrier on the old service.
