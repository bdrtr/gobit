# ADR 0292 — A cart lists the shipping options it can take

**Summary:** `GET /store/v1/carts/{id}/shipping-options` and its admin twin list
the options a cart can take, each priced for the cart's own facts by the flow
that prices the shipping method write. The panel's telephone order offers them
as a list.

- **Status:** Accepted — amended by [0295](0295-an-operator-may-choose-an-admin-only-shipping-option.md)
- **Date:** 2026-10-01

## Context

The fulfillment module's storefront eligibility endpoint takes the subtotal,
the item count and the weight from the client. It cannot verify them, so it
leaves out every option whose rule reads them, and a shop's "free over 500"
option was listed nowhere a shopper could see it. The cart flow already priced
an option with the cart's own facts when it added the shipping method, and
refused one the cart did not qualify for. ADR 0291's telephone order made the
operator type the option's id, because no cart-level listing existed.

## Decision

The cart flow lists every option it would accept for the cart: priced for the
cart's facts, neither admin-only nor a return option, in the cart's currency.
The cart module serves the list on the storefront under the cart's id and on
the admin surface under `cart:read`, and the panel's surface reads it for the
telephone order's form.

## Consequences

- A storefront shows the options a cart really has, ruled ones included, and
  every option it shows is one the shipping method write accepts.
- The price shown is the price the write records, both computed from the same
  facts at the moment of the request.
- The list is not paginated; a cart's options are few, and the response is the
  list envelope on one page.
- The panel's shipping form is a list of the options with their prices. When
  the listing fails, the form falls back to the option's id, and a cart no
  option serves says so.
- An admin-only option is not listed for the operator either, since the write
  refuses it on both surfaces.
- The fulfillment module's eligibility endpoint stays, for a client that has
  no cart.

## Rejected

- Trusting the client's facts on the eligibility endpoint: a made-up subtotal
  would open an option closed to everyone.
- Listing on the fulfillment module with the cart's id: the module cannot read
  a cart (ADR 0006), and the cart flow already assembles the facts.
- Listing admin-only options to an operator: the write refuses them, so the
  form would offer what it cannot take.
