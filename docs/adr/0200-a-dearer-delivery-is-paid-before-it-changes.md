# ADR 0200 — A dearer delivery is paid before it changes

**Summary:** A delivery change that costs more is applied once the operator
names a payment collection opened for the order that holds exactly the
difference, and the order journal books the difference as shipping owed. It
costs a collection per upgrade, and holds an exchange's funding to the same rule.

- **Status:** Accepted
- **Date:** 2026-09-26

Measurement: [measurements/0200](../measurements/0200-money-for-a-faster-courier.md)

## Context

ADR 0199 refused a dearer delivery because nothing could take the difference.
An exchange takes its own by naming a collection the operator collected
through the payment module's endpoints (ADR 0120), and that funding accepted
any collection holding the right amount: one opened for another order, or the
checkout's own (D142).

## Decision

A dearer change names in `payment_collection_id` a collection whose reference
is the order, in the order's currency and holding all it was opened for, and
the order module writes the change only when that equals the difference and no
change or exchange took the collection before. An exchange's funding is held to
the same reference and the same check, and both run under the order's lock.

## Consequences

Without a collection the refusal, `order_delivery_costs_more`, carries the
option, its amount, the difference and the currency in its details: what to
collect. The collection is opened and captured on the payment module's own
endpoints, as an exchange's is, and the flow moves no money. The quote is asked
again when the collection is named, and a price that moved in between is
refused as `order_delivery_payment_mismatch`.

The change records the collection's id and not what it holds (ADR 0119); the
difference beside it is the order's own arithmetic. The table holds a
collection exactly when the difference is positive and a credit line exactly
when it is negative, and one collection pays for one change.

The order journal books the change as `delivery_upgraded`, receivable debited
and shipping credited, and the payment journal's capture of the collection
credits receivable, so the two books close over it. An exchange's difference
still does not (ADR 0188).

The order's total and its summary do not move. The summary reads the sale's
collection over `order_payment` (ADR 0117), which this one is not.

Money given back from the collection afterwards is not seen by the change,
which stays applied, as a refund on an exchange's collection is not seen until
its dispatch asks again (ADR 0124). Going back to a cheaper service is a change like any
other, with a credit line for its difference.

An exchange funded with a collection opened for anything but its order is now
refused. The funding locks the order before the exchange, and no flow of the
module takes the two the other way round, so an exchange and a delivery change
naming one collection run one after the other and the second is refused.

## Rejected

- **Letting the order owe the difference and collecting it later.** The
  order's total and lines are fixed and its summary reads the sale's
  collection, so nothing on the order could say it was still owed.
- **The flow opening and capturing the collection.** Taking money is the
  payment module's, through a session the customer authorizes; the exchange
  keeps the same split.
- **A pending change waiting for its payment.** A quote held for money that
  may never come is a second state to expire; the quote is asked again instead.
- **The amount without the reference.** A collection that holds the difference
  by coincidence belongs to whatever it was opened for.
