# ADR 0286 — An operator completes a telephone order

**Summary:** The cart's admin surface writes the addresses and the shipping
method and completes the cart with an offline method. An operator can take a
telephone order to the end; the total they read to the customer is the one the
completion compares.

- **Status:** Accepted — amended by [0295](0295-an-operator-may-choose-an-admin-only-shipping-option.md)
- **Date:** 2026-10-01

## Context

ADR 0146 let an operator open a cart and add priced lines to it. The addresses,
the shipping method and the payment stayed on the storefront, so the caller had
to complete the cart themselves through a link. The reason was that the money
would otherwise be taken without the shopper seeing the total. A shop whose
callers cannot open a link was not served, and the known limits said so. ADR
0284 gave the order a way to be paid after it is placed. A completion that
takes no money no longer takes it behind anyone's back: the customer pays by
transfer or at the door, and the total is read to them on the telephone.

## Decision

The admin routes `PUT …/shipping-address`, `PUT …/billing-address`, `POST` and
`DELETE …/shipping-methods` under `/admin/v1/carts/{id}` are the storefront's
handlers behind `cart:write`, and `POST /admin/v1/carts/{id}/complete` runs the
storefront's completion with a channel the operator names and a mandatory
`expected_total`. The checkout refuses that completion with
`checkout_workflow_offline_method_required` unless its provider captures later.

## Consequences

- An operator completes a telephone order from the panel's API and the order is
  placed owing its total; the shop captures it when the money arrives (ADR
  0284).
- A cart whose total moved after the operator read it is refused with 409, so a
  write on a cart the shopper also holds cannot charge them a figure nobody
  confirmed.
- A card payment taken over the telephone is not offered: the operator would
  hold the shopper's card details. A caller who pays by card is sent the cart's
  link, as ADR 0146 had it.
- A gift card or a balance is not taken on the operator's completion; its body
  refuses the fields.
- Quantity changes, line removals, coupons, merges and deletes stay off the
  admin surface: each changes what a shopper holding the cart sees.

## Rejected

- Letting the operator choose any provider: a card provider needs the shopper's
  own payment data, which would pass through the operator.
- An order created from the admin without a cart: its total would be the
  caller's, the reason `CreateOrder` has no route (ADR 0146).
- Admin copies of the address and shipping handlers: two implementations of one
  act drift.
