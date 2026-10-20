# ADR 0461 — A panel may name the version it read

**Summary:** The admin surface's preflight allows `If-Match` beside the headers ADR 0451 names, and an integration test holds every header an admin operation describes to that preflight.
It costs one more header a listed site may send, and buys a panel on its own host the lost-update guard every product write offers.

- **Status:** Accepted
- **Date:** 2026-10-20
- **Amends:** [0451](0451-a-panel-on-its-own-host-calls-the-admin-api.md), whose allowed headers left `If-Match` out

## Context

ADR 0451 let a panel on its own host call the admin surface and named the
request headers its preflight allows: the store surface's list less the
store's keys. That list carries `If-None-Match`, which no admin route reads,
and leaves out `If-Match`, which every product write reads as the version
its caller read (ADR 0222) and which the document describes on each of
them. A browser asked to send a product write with it is refused by the
preflight and sends nothing, so a panel on its own host could only drop the
precondition (D316). The list was written by hand, and nothing held it to
the headers the admin operations describe.

## Decision

The admin surface's preflight allows `If-Match`. An integration test opens
the default installation with a listed origin and sends, for every header
an operation under `/admin/v1` describes, a preflight through the real
router that must allow it.

## Consequences

- A panel on its own host sends a product write with `If-Match` and is answered 412 `product_version_mismatch` on a version another write moved, as a same-origin panel is.
- A header a later admin operation describes and the list does not allow fails the test, not the panel.
- The CORS-safelisted headers are not asked for: a browser sends them without a preflight.
- `If-None-Match` stays allowed, as ADR 0451 wrote it.

## Rejected

- Reflecting the headers a preflight asks for: the asking side is the one the list checks, as ADR 0451's store list argues.
- Deriving the list from the document at startup: the allowed headers would follow every module's description, a typo included, into what any listed site may send.
