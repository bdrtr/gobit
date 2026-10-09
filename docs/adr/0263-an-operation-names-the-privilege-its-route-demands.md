# ADR 0263 — An operation names the privilege its route demands

**Summary:** The served OpenAPI document names, in each operation's security
requirement, the privileges the route's own guards demand, read off the router
rather than written in a describe block; the model client's tools say them.

- **Status:** Accepted; amended by [0434](0434-an-admin-route-demands-a-privilege.md), whose installation reads the same guard at startup and does not start with an admin route that demands none
- **Date:** 2026-09-30

## Context

Every admin route is wrapped in `corehttp.RequireScope`, and the document said
nothing of it: each operation's requirement was the bearer scheme with no
scope. A client generated from the document, and a model offered its reads as
tools (ADR 0161), learned the privilege only from the refusal. Writing each
privilege into a describe block would have been a second copy of 333 guards,
held to the first by nothing.

## Decision

`RequireScope` wraps a route in a guard whose type says which privilege it
demands, `corehttp.ScopeDemandedBy` reads it off a middleware, and the
document's walk of the router names every guard's privilege in the operation's
security requirement, in the order the guards run. The `mcp` verb ends each
tool's description with the privileges its operation names.

## Consequences

- The privilege is read from the route, so a guard added, moved or changed
  changes the document with no describe block edited. OpenAPI 3.1 allows a
  requirement of any scheme to list roles, and all of them are required.
- An end-to-end test holds the document to the router on the production
  wiring: an operator holding no privilege is refused naming the document's
  first, one holding the first alone is refused naming the second, and one
  holding all it names reads every admin read. Four operations name none, and
  the test names them with why.
- `ScopeDemandedBy` learns a middleware's privilege by applying it to a handler
  that does nothing, which is safe for middleware that only builds a handler;
  the ones this repository ships build their state before they are applied.
- `ScopeDemandedBy` is a new published name of `core/http`.
- A describe block's security requirement is copied before the privileges are
  written into it, so one value shared by two operations keeps each its own.

## Rejected

- A `Privilege` field in each describe block: a second copy of every guard,
  which is the drift the panel's privileges needed a gate for (ADR 0260).
- An `x-` extension instead of the security requirement: generators and
  clients read the requirement, and the standard has a place for roles.
- Probing each route with a scopeless credential while building the document:
  a document built by requests would run handlers for the identity-only routes.
