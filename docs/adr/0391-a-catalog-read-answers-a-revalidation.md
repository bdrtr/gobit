# ADR 0391 — A catalog read answers a revalidation

**Summary:** Every storefront catalog read tags its body with a hash of its
bytes and answers 304 to a request naming that tag, whatever the TTL.

- **Status:** Accepted
- **Date:** 2026-10-05
- **Amends:** [0151](0151-the-catalog-says-how-long-it-may-be-reused.md), whose "No `ETag`" no longer holds

## Context

ADR 0151 gave the channel-scoped catalog reads a `Cache-Control` and left the
validator to a second decision, "needing a version stamp the body does not
have". The body joins the product and its variants, tags, categories, images
and attributes, the channel and warehouse links, pricing's price sets and
inventory's levels, read in queries that are not one snapshot. ADR 0222's
version counts a product's revisions; a channel, price or stock link, a removed
tag or category, a reservation and a stock level move the body without one, and
a price list window and a reduction's reference days move it with no write.

Measurement: [measurements/0391](../measurements/0391-what-a-catalog-page-is-made-of.md)

## Decision

Every GET of the storefront catalog, the product module's `/store/v1` reads and
the search plugin's, writes its success body through
`corehttp.WriteJSONWithValidator`, which sets a strong `ETag` of the encoded
bytes and answers 304 with no body when `If-None-Match` names it under weak
comparison or is `*`. The tag is computed from the bytes of every success,
whatever the TTL, so no version is stored, published or composed.

## Consequences

- A 304 costs the server what a 200 costs except the write: the read, the
  enrichment, the encoding and the hash all run. It saves the transfer and the
  client's decode; a listing's count still runs unless `with_count=false`.
- A 304 answers only the tag of the bytes assembled now, or `*`, which asks
  only whether a body exists; no write, reservation or opening window is
  confirmed unchanged; a body read across a concurrent write has its own tag.
- The tag is written with a TTL of zero and adds no freshness: a 200 with no
  `Cache-Control` was as storable before, and the tag only lets a held copy be
  confirmed. `Cache-Control` stays where it is.
- The tag is written after the read, so a refusal never carries one, and a
  cache revalidating for a caller the key ring refuses receives the refusal.
- A 304 carries the `ETag` and any `Cache-Control` the 200 would. No `Vary` is
  added: the body reads no request header.
- CORS admits `If-None-Match` and exposes `ETag`, so a browser client on an
  allowed origin can revalidate itself, not only through its HTTP cache.
- A proxy that compresses the body weakens the tag; `W/"…"` sent back matches.
- `core/http` gains `WriteJSONWithValidator`, which takes no status and answers
  200 or 304; a 304 has no body, so the error path's masking has nothing to
  skip. `core/openapi` gains `Revalidated`, which describes the header and 304.
- The reads carrying the policy are derived from the router rather than listed,
  and every channel-scoped route is held to reach the writer: D241.

## Rejected

- A version composed from the modules' stamps: ADR 0222's misses every write
  that is not a revision, pricing and inventory publish none, and the clock
  writes nothing.
- A catalog stamp bumped by every write the body reads: pricing and inventory
  would write a row product owns, which Principle 2.1 forbids; a subscriber
  bumps it after the commit it records, confirming stale bytes in between; and a
  window opening bumps nothing.
- A 304 before the body is assembled: it needs one of the two stamps above.
- `Last-Modified`: the body has no single modification time, and a one-second
  resolution ties two writes.
- A weak tag: the tag is computed over the exact bytes sent.
- A process-seeded hash (`hash/maphash`): two replicas, or one restarted, never
  agree on a tag.
- The tag only when a TTL is set: it adds no freshness, and the installation
  without a TTL is the one whose clients revalidate most.
- Listings without a tag: the mechanism costs the same on every shape, and a
  listing is the largest body.
- The cart, its shipping options and the shopper's orders: each is read back
  after that shopper's own write, which is the read a tag does not save.
- GraphQL: it is POST-only (ADR 0044), and on a POST `If-None-Match` is a
  precondition answered with 412, not a revalidation.
- `no-cache`: it voids the operator's TTL. `must-revalidate`: it governs only a
  stale copy's reuse, which is the operator's cache's choice like `s-maxage`.
