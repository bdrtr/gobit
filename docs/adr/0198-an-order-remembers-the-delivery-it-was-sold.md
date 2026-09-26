# ADR 0198 — An order remembers the delivery it was sold

**Summary:** An order keeps the shipping methods its cart was checked out with —
the option, its name and what was charged — and a parcel opened without naming
an option goes on the one the order was sold. It costs a table written with the
order, and it is what changing an order's delivery has to start from.

- **Status:** Accepted
- **Date:** 2026-09-26

Measurement: [measurements/0198](../measurements/0198-a-total-without-a-service.md)

## Context

The checkout wrote the cart's shipping total onto the order and dropped the
methods behind it; its own comments said they were of no use to the order.
Opening a parcel asks the operator for a shipping option, and nothing on the
order said which one the shopper had chosen and paid for. Changing an order's
delivery needs that record first: there is no difference to charge without the
service it differs from.

## Decision

The order writes the cart's shipping methods — shipping option, name and amount
— in the transaction that writes the order, and refuses methods that do not add
up to its shipping total. A parcel opened for the order without an option goes
on the order's one method's option, and one opened for an order sold none or
several still has to name it.

## Consequences

Both order surfaces carry `shipping_methods`. A delivery's name describes the
service and not the person, so the storefront read carries it too; the method's
free-form data stays on the cart, where what the shopper typed stays.

The methods are the cart's as the checkout priced them, from the same revision
as the totals, so their sum is the shipping total by construction. The order
holds that rather than trusting it, as it holds its lines to its subtotal.

An order placed before this record has no methods and says so with an empty
list, and opening its parcel still takes an option. So does an order sold two
methods: which service a parcel goes on is then the operator's to say.

A named option is still taken as given. Whether a parcel opened on another
service than the one sold is a mistake or a correction is not decided here.

Changing the delivery a placed order was sold — and charging or crediting the
difference — is the next record; it needs the method this one keeps and a way
to take money against an order that ADR 0117 leaves to its own binding.

## Rejected

- **The option id alone on the order row.** A cart can hold several methods,
  and the name and the amount are what the operator reads.
- **Reading the method off the cart later.** The cart is reused or deleted after
  checkout, which is why the order keeps its addresses and its lines.
- **Refusing a parcel on another option than the one sold.** A courier out of
  service, or a customer who asked for faster, is a reason the operator has and
  this record cannot see.
