# ADR 0444 — A plugin's storefront key is the core's proof

**Summary:** A plugin declares a header its storefront routes decide access on with `Host.RegisterStoreProofHeader`, and the core adds it to the store surface's CORS allow-list and keys the store ring's idempotency records on its value.
It costs installing the plugins before the guards are built, and buys a plugin's key-gated storefront route the replay rule and the browser reach an order's token has.

- **Status:** Accepted
- **Date:** 2026-10-15
- **Amends:** [0439](0439-an-id-gobit-hands-out-opens-nothing-at-the-storefront.md), whose store-ring proof named the order's token and the customer alone

## Context

depremtek's backend, a consumer, opens a guest's service request to a key
the shopper's page holds and sends in `X-Service-Request-Key` (D299). The
store ring keys a record on the publishable key, the path, the body and,
since ADR 0439, an order's token and the proven customer: a stranger
repeating the holder's `Idempotency-Key`, path and body was answered the
holder's recorded move without the key, and one writing first under the
holder's key recorded a 404 the holder was then answered. The consumer
could only keep its answers empty of anything worth replaying. And the
store surface's CORS allow-list is closed, so a browser storefront could not
send the header at all. Neither list can be reached from a plugin's routes.

## Decision

A plugin declares such a header in `Setup` with `Host.RegisterStoreProofHeader`,
and the composition root, which now installs the plugins before it builds the
guards, refuses a name `corehttp.CheckStoreProofHeader` refuses: one that is
not a header field name or one the core already reads. The store ring mixes
each declared header's value into the proof it keys records on, and the
store surface's preflight allows it beside the core's own headers.

## Consequences

- A record of a write a declared header opened is replayed only to a caller presenting the same value; a request carrying none of the declared headers is keyed exactly as before.
- The headers enter the proof canonical, once and sorted, so the namespace does not follow the order plugins were installed in.
- A declared header keys every store write off a cart, not only the plugin's routes; that is why a name the core reads, the trace context and the credentials are refused: a request id sent fresh per attempt would turn idempotency off for all of them.
- `GuardOptions.CORSHeaders` is public; a name the check refuses is left out of the preflight answer rather than written into it.
- A cart's routes still ask nothing, a declared header included: the cart's id is their access.
- An identity outage refuses a keyed store write that carries a declared header, as it refuses every keyed store write off a cart (ADR 0439).
- The plugins are installed before the guards. A plugin's callbacks, providers and subscribers were already queued to Start, so nothing a plugin registers needs the guards.
- A module added with `App.Add` declares nothing: the declaration is on the plugin host.

## Rejected

- A proof function a plugin supplies: one plugin's error or slowness would refuse every store write.
- Reflecting the requested headers in the preflight: the asking side is the one being checked.
- A configuration variable listing extra headers: the plugin that reads the header is the one that knows it.
