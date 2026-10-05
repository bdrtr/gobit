# ADR 0400 — A shopping agent is a storefront client

**Summary:** gobit builds no agent surface; a shopping agent shops through `/store/v1` with the shop's publishable key, reading the open `/openapi.json`.
It costs every bound nothing in the tree holds: no rule, quota or audit row names an agent, and a delegation bounds only what a provider charges.

- **Status:** Accepted
- **Date:** 2026-10-05
- **Amends:** [0161](0161-a-model-client-can-ask-this-installation-questions.md), whose tool reads are recorded only when they list the audit log, and whose names are the operationIds the document derives

Measurement: [measurements/0400](../measurements/0400-a-shopping-agent-is-a-storefront-client.md)

## Context

Feature row C6 asks for an agent-native storefront: a machine-readable store
policy, a delegated credential bounded by budget, category and expiry, and a
rate limit and audit trail per agent. No record builds or refuses it, and
nothing in this repository names a shopping agent or an agent payment protocol.

`/openapi.json` is open, describes every route under `/admin/v1` and
`/store/v1`, gives each operation an `operationId` from its method and path, and
asks every store operation for the publishable key, which is public and names a
sales channel. The server prices lines and shipping (ADR 0021). Completion
carries `payment_data` unread into the session of the one provider it names (ADR
0064), for what a gift card and the balances leave (ADR 0209, 0269); an offline
method is such a provider, and places the order owing (ADR 0284).

A storefront request's customer is proven by the `corehttp.Identity` the
embedder binds, asked only where a request names one (ADR 0043, 0057); cart
completion names none, because a cart id is a capability. The store limit keys
a request before any identity exists, and the audit ring records admin writes
and the audit log's own listing (ADR 0037). A return is a request of any age an
operator decides (ADR 0051), the store profile has no storefront read (ADR
0115), and `gobit mcp` is the operator's: stdio, admin reads, a secret key.

## Decision

**gobit builds no agent surface: a shopping agent shops through `/store/v1` with
the shop's publishable key and the open `/openapi.json`, whom it acts for is what
the embedder's bound identity proves, and a discovery document is the embedder's
to serve. The decision reopens when a module, plugin or example in this
repository refuses a cart completion on a fact about the caller other than its
customer id and sales channel, or when an adapter for a named agent-checkout
protocol is proposed under `plugins/`.**

## Consequences

- **Nothing moves for an installation:** no route, scope, migration or setting.
- **A provider's charge is bounded by its credential.** A plugin reads a
  delegated token from `payment_data`, per checkout and kept only as far as the
  provider returns it, against an amount the client cannot set; ADR 0064's
  trigger is not touched.
- **No category or line bound exists, and nothing else bounds a delegation.**
  Whoever holds a customer's cart id spends their store credit and points, a
  gift card code is a bearer credential, and an offline method places the order
  owing; an expired delegation still completes a cart it opened. The one
  cross-order bound, a B2B employee's spending limit, caps the customer's spend
  in its period whoever completes the cart.
- **The principal is an `Identity` implementation**, as `contrib/identity-session`
  is, and a guest cart asks it nothing.
- **An agent shares its address's quota and no audit row names it**, because a
  store request carries no person before the limiter or after the handler.
- **Return terms and the seller are the embedder's to publish**; the profile's
  tax number stays off the publishable surface.
- **ADR 0161 is amended (D259).** A tool's read is recorded only when it lists
  the audit log. Its names are operationIds from method and path, so a tool
  moves with its endpoint; what 0161 rejected was hand-written ids.
- **Schema tests hold what an agent reads:** every operation has an
  `operationId` no other has, and every store operation asks for the key in the
  header the guard reads.

## Rejected

- **A policy read under `/store/v1`.** Its enforced facts are store reads already; the rest are promises nothing keeps.
- **A storefront read of the store profile.** No storefront in the tree prints the seller, and the profile carries the tax number.
- **`/.well-known/*` or `/llms.txt` from gobit.** The shop's origin is the embedder's, gobit's process or another host, and a module binds a root GET.
- **Expiry, budget or category on `api_key`.** A publishable key is public and names a channel, not a delegator.
- **A rate limit keyed by the publishable key.** Whoever reads the page source spends the channel's quota.
- **Store tools on `gobit mcp`.** It is the operator's, over stdio, and ADR 0161 refused the HTTP transport.
- **A mandate contract beside `Identity` now.** Nothing issues or asks for one; the bar is a counted consumer (ADR 0018, 0390).
- **A reference agent under `examples/`.** Scripted it proves what `examples/storefront` proves; with a model it brings a vendor.
