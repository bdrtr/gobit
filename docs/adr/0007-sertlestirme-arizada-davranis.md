# ADR 0007 — What happens when the hardening components fail

- **Status:** Accepted
- **Date:** 2026-08-24
- **Phase:** 9
- **Extended:** 2026-09-03 — the "The same question one layer up: the readiness
  probe" section. The decision did not change; the same argument was applied to
  the `/ready` endpoint.

## Context

Phase 9 brings three protective components: rate limiting, idempotency and (from
Phase 8) authentication. All three are middlewares answering the question "does
this request get through or not". All three can be unconfigured or faulty:

- `RateLimit` takes a `RateLimiter`; a Redis-backed implementation can be
  unreachable, or it can be unconfigured altogether (nil).
- `Idempotency` takes an `IdempotencyStore`; the same way.
- `RequireAdmin` takes an `Authenticator`; the auth module may not be
  registered.

The easy thing was a single rule for all three: "no component, let it through"
or "no component, reject". Both are wrong.

## Decision

**Each component behaves according to its own failure model. There is no uniform
rule.**

| Component | Unconfigured (nil) | Runtime fault |
|---|---|---|
| `RequireAdmin` / `RequireStore` | **Reject every request** (401) | Reject |
| `RateLimit` | **No-op** (let through) | **Let through** (fail-open) + warning log |
| `Idempotency` | **No-op** (let through) | On reservation: reject. On recording: release the key |

The reason is that the question "what breaks without this component" is answered
differently on every row:

**If authentication stays open the system becomes silently VULNERABLE.** A hole
nobody notices only becomes visible once it is exploited. That is why
`auth == nil` is a configuration error and must fail loudly: an unprotected
admin surface must never be left silently open.

**If the rate limiter stays off the system is merely UNPROTECTED, not wrong.**
The rate limit exists against abuse, not for the correctness of the product.
Rejecting all traffic when Redis goes down would turn the rate limiter into a
full outage source: it would take down the very service it protects. Not
enforcing the limit during the fault window is the accepted price, and it is
logged at `WARN` level.

**If the idempotency store is off, retries produce DOUBLE PROCESSING — that is
a correctness problem.** Even so, `store == nil` is a no-op, because saying
"idempotency is mandatory" while there is no store would break, overnight, every
existing client that does not send a key.

If the store IS there, the consequence of a runtime error depends on WHICH
MOMENT of the request it happens in, and that distinction is unavoidable:

- **An error during reservation (`Begin`)**: the handler has not run yet, there
  are no side effects. The error is passed to the client and the request is
  rejected. Saying "I could not record it but I processed it anyway" would mean
  silently accepting the risk of a second charge.
- **An error during recording (`Complete`)**: the handler ran, the response has
  ALREADY been written to the client. The status code can no longer be changed.
  The only correct thing left to do is release the reservation; otherwise the
  key stays "in flight" forever and the client can neither get a response nor
  retry. The price of releasing is that the retry may be processed again — which
  is better than a permanent lock.

By the same argument, a response exceeding the buffer limit is not recorded
either: recording a partial body and then replaying it would mean handing the
client a TRUNCATED and corrupt response. A corrupt response is far worse than a
retry being processed again.

## The same question one layer up: the readiness probe

The table above describes the behaviour of the middlewares. The same question is
asked one layer up, at the `/ready` endpoint, and its answer has to be from the
same family: `/ready` answers the question "can this instance take traffic", not
"is everything fine".

Making Redis a GATE there is exactly the "fail-closed for everything" option
this ADR rejects below — only one layer up. And it is worse: Redis is SHARED.
Every instance loses the probe in the same second, empties the Kubernetes
Service, and there is NO healthy replica left to shift traffic to. The gate
turns a partial degradation into a total outage.

The decision was made by measurement, not by inference
(`TestRedisOutageMeasurement`, `GUARD_BACKEND=redis`, Redis down):

| Request | Result |
|---|---|
| storefront catalog read | **200** — the read path never touches Redis |
| write without an `Idempotency-Key` | **200** — rate limiter fails open, logs WARN |
| write with an `Idempotency-Key` | **503** `idempotency_store_unavailable`, the handler DOES NOT RUN |
| exempt write (cart creation) | **200** |

No request is processed WRONGLY: the only class of request that cannot be
protected is the only class that is rejected, and it is rejected per request —
with a code the client can retry on. That is why the Redis probe does not cut
traffic; it is reported in the body as `"status": "degraded"` and as a WARN line
on every poll, and the code stays 200. Postgres remains a gate: without it there
is not a single endpoint that gives a correct answer.

The probe's BUDGET is part of this decision too, not decoration. A single Ping
against an unreachable Redis takes 1.7 seconds (the client tries to connect five
times); kubelet's readinessProbe timeoutSeconds default is 1 second, and a probe
that times out is scored exactly like a 503. So a "degradation" probe without a
budget would bring the same outage back through the back door. The probe budget
for degraded dependencies is therefore 250 ms, and it is not silent: it shows up
as the `budget` field on the WARN line, as an error message naming the budget in
the body, and as `READINESS_DEGRADED_TIMEOUT` in the operator's hands. Being
tunable is essential: if Redis is on the other side of the network a healthy
Ping can exceed 250 ms too and the installation reads `degraded` continuously —
a limit that cannot be tuned forces the installation to fork the code.

The one place left exposed is STARTUP, and it was deliberately left fail-closed:
Redis being unreachable at startup is most likely a wrong `REDIS_URL`, and an
installation running silently with a protection backend it cannot reach is
exactly the situation `guardStack` rejects. The price is that an instance
restarting DURING an outage crashloops until Redis returns — which is noisy, and
does not take the already-serving replicas out of traffic. This startup
behaviour is not a defenceless choice but a nailed-down contract: the
configuration test in `internal/smoke` verifies, on a real process, that with
`GUARD_BACKEND=redis` and a `REDIS_URL` pointing at a closed port the process
writes `redis_unreachable` and DOES NOT COME UP.

## Consequences

**Positive.** Each component's nil behaviour is a direct reflection of what that
component exists for. In the code this asymmetry is documented explicitly in the
godoc with a mutual reference (`RateLimit` refers to `RequireAdmin`) so that the
reader does not take it for an inconsistency.

**Negative.** The three components have similar signatures but behave
differently; somebody who does not know this can write `RateLimit(nil, nil)` and
believe they are protected. The countermeasure: there is a separate test for
each behaviour and the test names state the behaviour
(`TestRateLimitWithANilLimiterIsANoOp` and `TestIdempotencyANilStoreIsANoOp` in
`core/http`).

**The in-memory implementations are single-instance.** When `MemoryLimiter` is
scaled horizontally the real limit is multiplied by the instance count — that is
a *rate* problem, and tolerable. When `MemoryIdempotencyStore` is scaled
horizontally, two requests with the same key landing on different instances are
processed TWICE — that is a *correctness* problem, and not tolerable. So in a
multi-instance deployment a shared idempotency store is MANDATORY, while a
shared rate limiter is optional. Both are swappable through the interfaces in
`core/http`.

## Rejected alternatives

**Fail-closed for everything.** A short Redis outage would close the whole shop.
The protective component itself would become the largest source of outage.
Rejected in the middlewares — but `/ready` actually did this for a while: the
Redis probe was a gate, and when Redis went down the measured result was a 503
in 1.7 seconds — that is, every replica leaving traffic at the same moment. See
the readiness probe section above.

**Fail-open for everything.** Letting requests through on an auth fault would
make authentication entirely meaningless: an attacker would become admin merely
by managing to exhaust auth.

**Letting requests through on an idempotency store fault.** Tempting, because
"at least the request gets processed". But the sole reason idempotency exists is
to catch retries; completing the operation while the store cannot write is
precisely the most likely way to produce the double charge it is trying to
prevent.
