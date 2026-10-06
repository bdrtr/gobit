# ADR 0414 — GraphQL reads the catalog and nothing else

**Summary:** The GraphQL surface stays the product module's storefront read; no mutation and no cart, checkout, order, customer or admin type joins it.
It costs a client that reads the catalog in GraphQL a second protocol for everything else, and keeps one implementation of every write.

- **Status:** Accepted
- **Date:** 2026-10-06

## Context

`POST /store/v1/graphql` answers the catalog: products, the vocabulary, facets
and option values (ADR 0225, ADR 0226), each a call to the same service as its
REST address. Feature row B5.1 asks for a GraphQL-first API. The schema has no
Mutation type, and the only reason written for it was the schema's own header:
the writes rest on contracts the REST side solved, the `Idempotency-Key`
middleware, the error class to status mapping and the event a write publishes.
The GraphQL endpoint is exempt from idempotency because a GraphQL error answers
200 and a replayed fault would be stored. Neither storefront in reach reads
GraphQL: the example shop calls `/store/v1` (ADR 0159), and the Next.js
storefront generates a REST client from `/openapi.json`.

## Decision

**The GraphQL surface is the catalog's read, and it grows only by records that
add catalog reads to it; no mutation and no cart, checkout, payment, order,
customer or admin type joins it. This reopens when a client in this repository
must read through GraphQL what only REST serves.**

## Consequences

- Every write has one implementation, behind the REST contracts: idempotency,
  the status mapping, the event, and ADR 0051's audit of unidentified writes.
- A mutation would be a write class ADR 0051 has not audited, on an endpoint
  exempt from idempotency; reopening takes both on.
- A client that reads the catalog in GraphQL reads a cart, an order or a
  customer over REST, with the same publishable key.
- The schema header's reason is this record, and the header cites it.
- The limits `docs/api-surfaces.md` lists stay the read surface's.

## Rejected

- **Mutations mirroring the REST writes.** Two implementations of one act drift apart, and the idempotency exemption would have to be reversed.
- **Cart and order queries now.** Each is read back after the shopper's own write, and no client in reach reads them in GraphQL.
- **An admin GraphQL surface.** The panel is a client of `/admin/v1` (ADR 0030).
- **Reopening when an installation asks.** That consumer is outside the tree, and REST already serves it (ADR 0390).
