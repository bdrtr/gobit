# ADR 0300 — An operator corrects their own cart

**Summary:** The admin cart API removes a line from a cart and deletes a cart,
each the storefront's own act, on a cart an operator opened only. The panel's
telephone order offers both on the cart's page.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

An operator taking a telephone order could add lines and choose the shipping,
but a line added by mistake stayed until the order was placed, and a call that
ended without an order left its cart open in the panel's list (ADR 0296).
ADR 0146 kept line removal and cart deletion off the admin side because they
change a cart the shopper is holding. Since ADR 0299 every admin write reaches
only a cart an operator opened, so that reason no longer covers the
operator's own cart.

## Decision

`DELETE /admin/v1/carts/{id}/line-items/{line_item_id}` and
`DELETE /admin/v1/carts/{id}` run the storefront's removal and deletion on a
cart an operator opened, and refuse a shopper's with ADR 0299's 409. The
panel's cart page gives each line a remove button and the cart a discard form,
through two more methods of the cart module's surface.

## Consequences

- An operator corrects a telephone order while the caller is on the line and
  discards one the caller abandoned, which then leaves the open carts.
- The removal reprices the cart, as the storefront's does. An add-on goes with
  its line and has no remove button of its own (ADR 0229).
- A discarded cart is soft-deleted with its lines, addresses and methods and
  has no page afterwards; a completed cart offers neither form and refuses
  both writes.
- A quantity is lowered by removing the line and adding the variant again;
  the admin side has no quantity write.

## Rejected

- A quantity write on the admin side: the storefront's asks the channel again
  (ADR 0281), and the panel's add form already raises a quantity.
- Removing a line from a shopper's cart: ADR 0299's reason holds for it as for
  every other write.
- Discarding by marking the cart in place: the storefront's deletion already
  takes the cart out of every listing.
