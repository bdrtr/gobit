# ADR 0372 — A buyer's review says they bought it

**Summary:** A review written by a request that proves a customer is a verified
purchase when one of that customer's orders carries the product, and the
customer is asked about and not stored.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The review module left "verified purchase" unexpressible on purpose: an order
id in a request body proves only that the writer holds one, and ADR 0008 then
left customer identity to the embedder; checking one would also have put the
order module on the write path of an unauthenticated endpoint. Since ADR 0043 a
storefront request can prove a customer, and ADR 0371 says who a request proves,
if anybody. A shop's product page shows which reviews come from buyers; a
storefront built on gobit had no way to say so.

## Decision

A storefront review whose request proves a customer is marked a verified
purchase when one of that customer's orders that was not canceled has a line of
one of the product's variants. The customer is asked about when the review is
written and not stored; a request proving nobody writes an unverified review, as
before.

## Consequences

- The review migration 000004 adds `verified_purchase`; the storefront and the
  admin read it. Nothing names the writer: the badge, not the customer, is kept,
  so the review module still cannot find a person's reviews and its personal
  data declaration is unchanged.
- The order module answers `CustomerBoughtAnyOf` over its interop surface with
  one EXISTS over the customer's own orders. Its lines carry no product, so the
  review module reads the product's variants from the catalog first.
- A composition without the order module or the read layer writes every review
  unverified and warns once; it does not refuse the review.
- A failure to read the purchase ends the submission: a buyer's badge cannot be
  added afterwards. An identity that could not check ends it too (ADR 0371).
- The badge is fixed when the review is written. A buyer whose order is later
  canceled keeps it; a reviewer who buys afterwards does not gain it.
- A variant the catalog no longer lists is not asked about, so its buyers write
  unverified reviews.

## Rejected

- Storing the customer on the review: every review would become a person's
  record, an erasure and a disclosure to answer, for a badge that needs the
  customer only once.
- Taking an order id from the body: it proves possession of an id, which is the
  objection the module was built on.
