# ADR 0192 — An order can add to another

**Summary:** A customer adds to a pending order by opening a cart that names it,
and the checkout places an ordinary order that says what it adds to. It costs a
column on the cart and on the order and a share lock on the parent, and it adds
goods to a sale without editing an order or an issued invoice.

- **Status:** Accepted
- **Date:** 2026-09-26

Measurement: [measurements/0192](../measurements/0192-goods-bought-afterwards.md)

## Context

An order's lines and amounts never change after it is placed, and corrections
are records of their own (`order/models`). The subtracting half of an order edit
was complete: cancellations, credits, returns. Nothing could add goods to a sale.
The nearest act was an exchange whose difference the operator types in, which is
untaxed, outside both journals, and moves stock as goods nobody bought
(ADR 0090).

## Decision

A cart opened on either surface with `adds_to_order_id` becomes, at checkout, an
order whose `adds_to_order_id` names that order, with its own lines, totals,
payment, stock movements and invoice. The named order must be pending, of the
same customer and currency, and not an addition itself, which the order module
checks when the cart is opened and again under a share lock on it in the
transaction that writes the addition.

## Consequences

Nothing new prices, taxes, discounts, collects, reserves or books. The addition
is an order placed, so both journals, the invoice series and the timeline read
it as one.

The customer gets two orders, two invoices and, for now, two parcels. Shipping
an addition with its parent is not decided here, and the addition's cart
charges whatever shipping its shopper chooses.

A guest's order cannot be added to: a guest proves nothing that ties a second
purchase to the first. On the storefront the customer is the one the body has to
prove (ADR 0125), and the stored-claim register records the cart's column as
CONFINED on that proof.

The parent's cancellation locks it for update, so it and an addition's write run
one after the other. An addition that waited is refused, and one written first
stands when the parent is canceled later. The foreign key alone would lock the
parent too late, after the status was read. Two additions to one parent do not
wait for each other.

A parent closed between the cart's opening and its checkout refuses the
checkout at the order step, before any payment. The saga gives back the
reservation and the coupons.

Two carts merge only when they add to the same order or both to none.

The admin listing reads an order's additions with `adds_to_order_id`; the parent
carries no list of them.

## Rejected

- **An edit record on the order.** It needs a second pricing, tax and discount
  path beside the cart's, and a payment outside the journals, as the exchange
  difference already is.
- **Editing the order's lines in place.** The order and its invoice are the
  permanent answer to what was sold (ADR 0024).
- **Adding to a completed order.** The sale is closed, and a new order is a new
  order.
- **Chains of additions.** Every addition names the order it adds to directly,
  so a parent's additions are one read.
- **The same region.** Reading the two together needs one currency, and the
  addition's own cart already applies its region's tax.
- **Checking only at checkout.** A shopper would build a cart they could never
  check out.
