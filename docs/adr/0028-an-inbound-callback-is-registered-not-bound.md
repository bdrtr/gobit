# ADR 0028 — An inbound provider callback is REGISTERED, never bound by the plugin

- **Status:** Accepted
- **Date:** 2026-09-05
- **Phase:** after the roadmap

## Context

Four carriers, e-invoice transmission and every payment provider each need the
same thing: an HTTP endpoint the provider posts to, authenticated by a signature
rather than by a credential the caller holds. The repository had exactly one,
and it was measured to be unguarded.

`/paytr/callback` bound itself on the ROOT router. It matched neither the admin
nor the store prefix nor any open prefix, so the entire guard stack fell
through: no authentication, no rate limit, no idempotency, no audit, no CORS and
no request body size limit — `ParseForm` on an unbounded body. Its only
protection was an HMAC inside the handler. The endpoint that moves a payment to
paid was the least protected surface in the repository.

Two facts made this worse than a single oversight:

- **Nothing said so.** A route bound on the root router looks exactly like a
  route bound under a guarded prefix. It was found by measurement, not by a
  test, and no test could have found it.
- **It is not reusable.** The word "webhook" appears in non-test code exactly
  ONCE, in a godoc. The next provider would have re-solved the signature check,
  the replay window and the body limit from scratch, and would have got a
  different subset of them right.

## Decision

**A plugin does not bind a callback route. It registers one, and the core binds
it.**

`core/http.CallbackRegistry` is created before the router, filled during the
plugin Start phase and mounted before the plugin routes. A plugin calls
`Host.RegisterCallback` with a route that names its source, its path, its
verifier, its key derivation, its handler and its answer vocabulary.

Per request, in this order:

1. **Quota**, keyed by path plus client IP. First because a refused request must
   be almost free — after it come a body read and a signature hash, and the
   point of the quota is that an unauthenticated caller cannot make the endpoint
   work.
2. **Body limit**, per route, default 64 KiB. Without it the only ceiling was
   net/http's own 10 MB form default — a standard library decision rather than
   one this repository made.
3. **Timeout**, per route, default 10s.
4. **Signature verification.** Before anything is derived from the payload,
   because everything derived from an unverified payload is attacker-chosen —
   including the replay key, which is precisely the value an attacker would want
   to choose.
5. **Replay suppression**, against the same store the API surfaces use.
6. **The handler.**

**The answer is the PROVIDER's protocol, never gobit's envelope.** PayTR reads
the body, not the status: anything but its token means "not acknowledged" and it
retries. Each route declares five answers — accepted, duplicate, rejected,
malformed, unavailable — and the registry refuses a route missing any of them at
startup rather than at the first callback.

## The replay key, exactly

A carrier sends no `Idempotency-Key`, so the key is derived — and derived ONLY
from fields the signature covers. A field the provider does not sign is a field
an attacker can perturb, and perturbing the key mints a fresh one, which defeats
the window entirely while appearing to work.

- The **bucket** is `callback:<source>`, so two providers cannot silence each
  other's events through a shared namespace.
- The **identity** tuple answers "which event is this" and becomes the key. For
  PayTR it is the order id alone: PayTR signs no event id, no nonce and no
  timestamp.
- The **content** tuple answers "what does it assert" and becomes the
  fingerprint. For PayTR: order id, status, amount — and NOT `failed_reason_msg`,
  which PayTR does not sign.
- Both are joined length-prefixed before hashing, so no identifier containing
  the separator can make two different events produce one key.

**A retry with matching content is answered from the record.** A retry with
DIFFERENT content is a contradiction — the same event asserting a different
outcome — and it is acknowledged and reported at ERROR rather than refused.
Refusing it would make a provider that reads the body retry the contradiction
forever.

## Three deliberate departures from the obvious design

**No reserved prefix.** A `/callbacks` prefix would let the ring be scoped like
every other guard, and it is what the shape of the guard stack suggests. It was
rejected because it forces every existing provider's URL to move, and that URL
lives on the PROVIDER's side — changing it is an operational break, not a
deploy. The ring instead acts on the paths it was given and passes everything
else through, and the prefix policy is enforced by a test rather than by a
string.

**The enforcement is an arch test, not the registry.** The registry makes the
guarded thing easy; it cannot make the unguarded thing impossible, because a
plugin can still call `r.Post` on the router it is handed. That is the whole
failure mode, so `TestEveryStateChangingRouteIsGuarded` reads the source and
refuses any state-changing route outside the guarded prefixes. GET is out of
scope on purpose: a read outside the guards is a decision already made several
times over (`/health`, `/ready`, `/openapi.json`, `/files`), and it is not what
an unguarded endpoint costs.

**No audit row.** A verified callback could be recorded with the provider as the
actor, and the schema would accept it. It is not done here, for two measured
reasons: the audit contract says in four places that the table records
ADMIN writes and that a row on an unauthenticated surface "would say somebody
and mean nothing"; and nothing in the repository reads `audit_log` today — the
only statement against it is the INSERT. Overturning a written contract to
produce rows with no reader is not a trade worth making silently. It is a
decision, and it is left open below.

## What this deliberately does NOT do

- **No separate rate-limit budget.** The ring uses the installation's existing
  limiter with a per-path key, which turns "no quota at all" into "a quota per
  path per client". A second budget needs a configuration shape that does not
  exist; adding one is a small, separate change.
- **No freshness window.** It can only be required of a provider that SIGNS a
  timestamp, and PayTR does not — so a captured genuine PayTR callback stays
  valid as long as its row is pending. The per-route field to express one is not
  invented until a provider that signs a timestamp exists.
- **No reconcile sweep** for a callback that never arrives. There is still no
  plugin-reachable job extension point (gaps.md B13); PayTR's manual
  pending-payment listing remains the answer.
- **No carrier.** A carrier plugin needs things this plumbing does not provide:
  a lookup from the carrier's own shipment id to a fulfillment row, a status
  vocabulary wider than four values, and tolerance for an out-of-order delivery
  event. That is gaps.md B10, and it is a prerequisite for a carrier — not for
  this.

## Consequences

- **The one existing callback is now guarded**, at the same URL, with no change
  visible to PayTR. Its signature check moved out of the handler and into the
  ring, where it runs before anything trusts the payload.
- **`Host.RegisterCallback` is the only supported way to add one**, and the
  registry refuses a route with no verifier at startup. A callback with no
  signature check cannot be expressed.
- **`CallbacksName` is a PUBLISHED container name**, unlike the infrastructure
  names, which are unexported constants in the composition root that every
  plugin re-spells as a literal.
- **The registry freezes when it mounts.** A route registered later would never
  be bound, and a path the provider can reach that this ring does not know is
  exactly the endpoint being removed; the refusal is loud.
- **The dedup window shares the installation's idempotency backend.** With the
  in-memory backend on more than one instance there is, in effect, no window —
  the same warning the API surfaces already carry.

## Decisions left open

1. Whether a verified provider becomes a `Principal` and gets an audit row,
   which means revising the audit contract and giving `actor_kind` a third
   value.
2. Whether failed-signature attempts are audited. A retry storm writes one row
   per attempt, and the audit ring has no dedup.
3. Whether callbacks should require `GUARD_BACKEND=redis`, since the memory
   store is per-process and evicts.
4. A per-route replay TTL: the window is the global `IDEMPOTENCY_TTL`, and a
   carrier that retries for three days is not covered by 24 hours.
5. Where a callback secret comes from. Today it is the process environment,
   through `Host.Setting`; four carriers' secrets with rotation may not belong
   there.

## Reopening

Reopen when the first carrier integration is written. Nothing in this
repository signs a carrier payload yet, so the field set a carrier signs,
whether it signs into a header or the body, and whether it sends an event id are
NOT measured — the verifier takes both the request and the body to cover either
shape, but the identity tuple for a real carrier is a guess until one integration
is read.
