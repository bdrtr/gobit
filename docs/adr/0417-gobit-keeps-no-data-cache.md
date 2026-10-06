# ADR 0417 — gobit keeps no data cache

**Summary:** The catalog is reused over HTTP, by a TTL (ADR 0151) and a validator (ADR 0391); gobit keeps no data cache, no cache provider slot and no purge endpoint.
It costs a deployment whose database becomes the constraint a cache of its own in front, and keeps every read a read of the database's current rows.

- **Status:** Accepted
- **Date:** 2026-10-06

Measurement: [measurements/data-layer](../measurements/data-layer.md)

## Context

Feature row A8.3 asks for a cache module. ADR 0151 gave the catalog reads a
`Cache-Control` an operator sets and refused purge on write, because a price
window moves the body with no write; it left CDN directives to the operator
whose cache they govern. ADR 0391 tagged every catalog body with a hash of its
bytes. What no record held is the data cache and the purge endpoint. The data
cache was measured unwarranted on 2026-09-05: on the large fixture the database
answers a listing well ahead of the Go path that formats it. Redis carries the
bus, rate limits and idempotency, nothing else. A purge endpoint needs the list
of URLs a write invalidates, which is the join ADR 0391 found no stamp for,
read from the other side.

## Decision

**gobit keeps no data cache, no cache provider slot and no purge endpoint; the
catalog's reuse is the TTL and the validator of HTTP. The data cache reopens when
a measurement shows the database is the storefront's constraint, and the purge
endpoint when a write can name every URL it changes.**

## Consequences

- Every read sees the rows as committed; no invalidation path exists to miss a
  write, a reservation or an opening price window.
- A deployment that needs more reuse puts a cache in front: the TTL, `public`
  and the ETag are what it is told, and its directives are its operator's.
- The measurement's trigger stands as written: an index scan past about twenty
  rows, an enrichment leg turning one request into many queries, or a database
  a network hop away.
- The GraphQL handler's cache of parsed documents is not a data cache and stays.

## Rejected

- **A Redis read cache for product and listing reads.** It relieves the side already ahead, and every write and window would need an invalidation.
- **A cache provider contract.** Nothing in the core would call it (ADR 0390).
- **A purge endpoint for one product's URLs.** The listing, facets and related reads carry the product too, and the clock still moves the body.
- **CDN directives.** Decided by ADR 0151: the operator's cache, the operator's choice.
