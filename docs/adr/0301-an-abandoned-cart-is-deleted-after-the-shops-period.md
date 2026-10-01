# ADR 0301 — An abandoned cart is deleted after the shop's period

**Summary:** `CART_RETENTION_DAYS` names how long an open cart is kept after
its last change, and an hourly job deletes for good the open carts untouched
for longer, with their lines and addresses. Zero, the default, keeps every
cart.

- **Status:** Accepted
- **Date:** 2026-10-01

Measurement: [measurements/0301](../measurements/0301-the-carts-nobody-came-back-for.md)

## Context

A storefront opens a cart for every shopping session, and most are never
completed. Nothing removed them: a guest's e-mail and shipping address stayed
in an abandoned cart for as long as the database did, and every listing of
carts walked them. ADR 0029 made the embedder the controller of that data and
gobit the publisher of mechanisms, so the period could not be gobit's. Every
write to a cart moves its `updated_at`.

## Decision

The cart service deletes, in batches of 500 and the oldest first, the open
carts whose `updated_at` is older than the configured period, skipping a cart
a write holds locked; the foreign keys' cascade deletes their children. The
`cart-retention` job runs it hourly, and with no period set it deletes nothing
and says so.

## Consequences

- A shop that names a period keeps a guest's cart data no longer than it; a
  shop that names none keeps every cart, as before, and upgrading deletes
  nothing.
- A completed cart is never deleted: it is the record an order rests on.
- A soft-deleted cart is deleted too, since its row still holds what the
  shopper wrote; the rows go for good rather than being stamped.
- A cart only read is not changed, so a shopper who views a cart every day for
  longer than the period without changing it loses it.
- A partial index holds the open carts by their last change, so a batch reads
  500 rows instead of sorting every open cart.
- A cart locked by a write is skipped and taken by the next run.

## Rejected

- A default period: an installation would start deleting by upgrading, and
  the period is the controller's (ADR 0029).
- Soft deletion: the stamped row keeps the e-mail and the address the period
  exists to remove.
- Counting the period from the cart's creation: a cart a shopper keeps
  filling would be deleted while in use.
