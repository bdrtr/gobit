# ADR 0299 — An operator's writes reach only an operator's cart

**Summary:** Every admin write on a cart in the path, and every write of the
panel's telephone order, refuses a cart no operator opened with 409
`cart_opened_by_shopper`. A shopper's cart is read on the admin side and
changed only by the shopper.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

ADR 0146 let an operator add a priced line to any cart, a shopper's included,
and refused the rest — the address, the shipping method, the completion —
because each changes what the shopper is looking at. ADR 0286 opened those
three for the telephone order on every cart id, so an operator could write a
shopper's address, choose their shipping or complete their cart (D200). Nothing
could tell an operator's cart from a shopper's until ADR 0296 recorded who
opened each.

## Decision

The admin line, address, shipping method and completion writes, and the panel
surface's line, address, shipping and completion, first read the cart and
refuse it with 409 `cart_opened_by_shopper` when no operator opened it. The
panel's cart page offers no form on such a cart and says why.

## Consequences

- A shopper's cart changes only through the storefront, whatever an operator
  holds; a line can no longer be added to it from the admin side either.
- An operator's cart takes every admin write, and its link still lets a
  shopper pay it on the storefront (ADR 0146).
- Each admin write reads the cart once more before it. The opener never
  changes, so the answer cannot go stale before the write.
- Carts opened before ADR 0296 name no opener and are a shopper's to the admin
  side; a telephone order in progress across the upgrade is opened again.
- Reading the cart and listing its shipping options stay open on every cart.

## Rejected

- Keeping ADR 0146's line write open on a shopper's cart: an added line
  changes the total in front of them as surely as an address does.
- Letting an operator take over a shopper's cart: the shopper still holds its
  link and would pay for what they did not choose.
- Refusing in the cart service: the storefront writes through the same service,
  and the opener matters only at the admin doors.
