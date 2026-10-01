# ADR 0304 — A customer's cart starts from their default address

**Summary:** The customer provider offers a customer's default shipping
address, and the telephone order's cart page draws its address forms with it
when the cart is a customer's and has no shipping address yet. Nothing is
written until the operator saves the form.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

ADR 0297 let an operator open a telephone cart for the caller's account. The
customer module kept that customer's addresses, one of them marked their
default shipping address, but the read layer offered none of them: a
module's own records are joined within the module, and the panel reads
nothing else. So the operator asked the caller for an address the shop
already held and typed it again.

## Decision

The customer provider offers `default_shipping_address`, keyed as a cart's and
an order's address is, read for a page of customers in one query and only
when asked for. The cart page of a customer's cart with no shipping address
reads it under `customer:read` and draws the shipping and billing forms with
it, saying so.

## Consequences

- An operator confirms a returning customer's address with the caller
  instead of typing it.
- The address is a starting point: the forms are saved by the operator, and
  the cart holds no address until then.
- An operator without `customer:read`, a guest's cart, and a cart that has a
  shipping address read nothing of the customer; a read that fails draws the
  forms empty, as before.
- The default billing address is not offered; the billing form starts from
  the shipping form's values (ADR 0303).
- Any reader of the customer entity may ask for the field, under the
  customer module's privilege, as for the customer's other details.

## Rejected

- Writing the default address into the cart when it is opened for the
  customer: the caller may want this order delivered elsewhere, and the
  operator would undo a write nobody asked for.
- Offering every address of the customer: the default is the one the
  customer chose, and a list belongs on the customer's own page.
