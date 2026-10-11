# ADR 0439 — An id gobit hands out opens nothing at the storefront

**Summary:** The storefront's order routes answer one of the order's access tokens or a proof of its customer, a cart's id leaves gobit only toward its shopper, and the storefront takes no payment collection's id.
It costs a guest's order page its token and a webhook receiver its cart id, and an order placed before this record opens to its customer's proof alone.

- **Status:** Accepted; amended by [0438](0438-a-shoppers-payment-is-opened-before-the-cart-is-completed.md), whose storefront names a payment session under its own cart and answers a completed cart's reported session once
- **Date:** 2026-10-11
- **Amends:** [0100](0100-the-customer-sees-the-goods-and-not-the-ledger.md), [0153](0153-a-shop-can-see-where-its-carts-go.md), [0193](0193-an-order-says-where-it-went.md), [0218](0218-a-webhook-receiver-can-narrow-what-it-gets.md), [0367](0367-a-customer-lists-their-own-orders.md), [0390](0390-an-analytics-product-hears-gobit-through-the-bus.md) and [0435](0435-a-webhook-receiver-builds-against-a-published-contract.md); narrows the historical [0008](0008-musteri-kimligi-guven-siniri.md) and [0051](0051-an-unidentified-write-is-confined-or-inert.md)

Measurement: [measurements/0439](../measurements/0439-what-a-receiver-could-open.md)

## Context

The storefront read an order, read its timeline and opened a return on it for
whoever held the order's id and the shop's publishable key, and the served
document told a client to keep that id out of anywhere it could be shared.
gobit shares it itself: every order topic it forwards to a webhook receiver
carries it, and a parcel's names it as its reference. A receiver holding one
`order.placed` delivery read the shopper's e-mail, the customer id webhook-out
withholds, the lines and the totals, read off the timeline the tracking number
ADR 0437 keeps off the wire, and opened a return that refused the owner's own
(D280). The record named its cart, and a completed cart answered the addresses
ADR 0193 keeps on the admin record (D281). `cart.created` forwarded a live
cart's id, which is its access, and a receiver read its e-mail, home address
and phone and moved its delivery (D282); `payment.captured` forwarded a
collection's id, and the storefront answered its figures and its cart (D283).
The customer's own order list refused the same caller.

## Decision

The storefront's order routes answer a request that presents one of the
order's access tokens in `X-Order-Token` — 32 random bytes the order keeps as a
digest, issued to the completion's answer and to every order mail — or that the
bound identity proves to be the order's customer, and every other request the
404 an unknown id gets. A cart's id leaves gobit only toward the storefront
holding the cart, so webhooks withhold it as they withhold a customer's and
every storefront route answers a completed cart the unknown cart's 404, and no
storefront route takes a payment collection's or session's id.

## Consequences

- A guest reaches the order through the completion's or the mail's
  `order_token`, a signed-in shopper through the identity, and the record names
  neither cart nor customer. A refusal says nothing an unknown id's does not;
  an identity that reaches no verdict answers its own class for both (ADR 0371).
- `order_access_tokens` keeps a SHA-256 of each token, compared in constant
  time; no event, webhook or other record carries one. Each issue is a new
  token, the earlier ones stay valid, and an erasure deletes the person's on
  every order, a retained one too.
- The token is issued after the checkout's saga, so its record holds none; a
  completion repeated under its Idempotency-Key answers the recorded token. An
  earlier order holds none: a guest's opens to nothing, a customer's to proof.
- The store's idempotency ring keys a record off a cart on the token and the
  proven customer too, and refuses a keyed write whose proof it cannot read. An
  order answer is `no-store`, and a cart opened to add to an order tells nothing
  to anyone but its customer.
- Webhooks name `cart_id` under `redacted`; cart topics keep region, currency
  and moment, and their event id names the cart by a digest, pairing its two
  events. A product joining page events by cart id behind webhook-out loses
  that join (ADR 0390); one on the bus keeps it, inside the installation.
- The three payment routes had no caller outside their tests. Ids still reach
  gobit's logs and the shop's pages, where an order id opens nothing and a cart
  id is still its access. Guest order pages, `cart_id` receivers and payment
  route clients break.
- Gates hold every route under `/store/v1/orders/{id}` to the order's access
  decision and under `/store/v1/carts/{id}` to the completed-cart refusal, and
  no field webhook-out forwards may key a storefront route that proves nothing.

## Rejected

- One token per order, written with it: the saga's record would hold the plaintext, and the mail, built after the commit, gets nothing back from a digest.
- The token in plain text on the order: a read of the table would hand out every order's link.
- The order's e-mail as a guest's proof: whoever checks out types it, and every system that mails the shopper knows it.
- A token signed with an installation key: one key opens every order, and no single link can be withdrawn.
- Withholding the order id from webhooks: a receiver correlates by it, and logs and pages carry it anyway.
- The identity alone: a guest's order has no customer, and an installation binding none loses every order page.
- 401 or 403 for a refusal: they tell an id's holder that the order exists and has a customer.
- A token beside every cart id: every storefront cart call changes for a hole that withholding the id closes.
- The payment routes behind the order's proof: nothing called them, and a collection's reference is a cart.
- A known-limits sentence: the fault is gobit handing out the key, which a sentence does not stop.
