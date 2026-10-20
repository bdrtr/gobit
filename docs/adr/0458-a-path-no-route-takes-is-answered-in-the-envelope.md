# ADR 0458 — A path no route takes is answered in the envelope

**Summary:** `corehttp.NewRouter` answers a path no route takes with `corehttp.RouteNotFound`, a 404 carrying `route_not_found` in the error envelope, on the root and every router mounted under it.
It costs an embedder that wants chi's plain answer a line of its own, and buys a client that parses every error answer as JSON one it can parse.

- **Status:** Accepted
- **Date:** 2026-10-20

## Context

A route an installation leaves unbound is a documented path that is not
there: `contrib/identity-session`'s `POST /store/v1/auth/register` and its
password reset without a mailer, `POST /store/v1/webpush/subscribe` without
a key. depremtek's storefront, a consumer, met them in production as chi's
own answer, `404 page not found` as text/plain, and its client failed
parsing the body (D313). `docs/commerce-flows.md` says every error body is written by
`corehttp.WriteError`; the admin surface's unbound paths were (ADR 0434),
the root's and the storefront's were not.

## Decision

The root router's not-found handler is `corehttp.RouteNotFound`, which
writes a 404 with code `route_not_found` and the message `no route answers
<method> <path>` through `corehttp.WriteError`, and chi hands it to every
router mounted under the root. The admin surface's own answer for a path
none of its routes takes is the same handler.

## Consequences

- An unbound path carries `request_id` like any other error, and the request log records it as one.
- A path that matches with another method is still chi's 405 with an `Allow` header and no body, on every surface, as ADR 0434 chose for the admin one.
- An embedder that sets the root's not-found handler after `NewRouter`, or mounts a router with its own, answers its paths with it; a single-page app on the root's catch-all answers every path it takes, as before.
- The answer names the path the request asked for; it names no route that exists.

## Rejected

- Binding `/store/v1` and its catch-all as ADR 0434 binds the admin prefixes: a module or plugin mounting a router at `/store/v1` itself would panic at startup, and the root handler reaches the same paths.
- An envelope on the 405 too: the method a path takes is the client's bug rather than an installation's state, and the admin surface's answer stays the one every surface gives.
- Binding every documented route and answering 503 when its dependency is not configured: each module decides whether its route exists, and the routes left out are listed in its own documentation.
