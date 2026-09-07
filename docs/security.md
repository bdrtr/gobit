# Security: identity, authorization and the hardening rings

This document covers one path: how an HTTP request is authenticated, what it is
then allowed to do, and what each hardening layer does when it is not configured
or when it fails. It carries the two API surfaces and the identities that open
them, the order of the guard rings, the sales-channel scoping of the catalog, the
scope vocabulary, and the hardening settings with the measurements behind them.

It is written for whoever **runs** a gobit installation or **embeds** it: the
operator who has to know which knob does what and what it costs to get it wrong,
and the integrator who has to know which header opens which endpoint. The entry
point to the repository is [`README.md`](../README.md); why the architecture is
built this way is in [`docs/mimari.md`](./mimari.md).

## The two surfaces

Two surfaces, two identities. The guard is attached not in the modules but on
**the side that builds the router** (`internal/app`, the composition root: it
reads the configuration, builds the container, mounts every module's routes and
then listens). Modules register their routes with their full path on a flat
router, the scoping is done with `corehttp.Scoped`, and the order is written in a
single place, inside `corehttp.APIGuards`.

| Surface | Identity | Rate limit | Header |
|---|---|---|---|
| `/admin/v1/**` | Session token (HS256 JWT) **or** a secret key (`sk_…`) | yes | `Authorization: Bearer …` |
| `/store/v1/**` | Publishable key (`pk_…`) | yes | `x-publishable-api-key: …` |
| `/files/**`, `/openapi.json` | none | **yes** | — |
| `/health`, `/ready` | none | no | — |

**Identity and quota are SEPARATE decisions.** File serving and the schema
endpoint carry no identity because their clients cannot send a header (the
`<img>` in a storefront, a code generator) — but they are not free either: one
reads the disk and the database, the other walks the route tree. The health
endpoints are outside both; the caller there is the orchestrator, and a health
endpoint that hits a quota gets a healthy instance pulled out of traffic — that
is, the limit itself produces the failure. The coverage is pinned in a real
process in `internal/smoke` (`TestQuotaCoverageInRealProcess`), because a missing
prefix fails nothing: the endpoint keeps working, it just works without a quota.

**The only unguarded admin endpoint** is `POST /admin/v1/auth/login`: the request
whose identity is to be checked is the one about to establish it. The exemption is
not spelled out by hand, it is read from the `authapi.LoginPath` constant.

The order of the guard stack is deliberate:

1. **Rate limit** — *before* authentication. Otherwise an attacker trying
   passwords would make us pay the bcrypt + database cost on every attempt, and
   their quota would only drop after that.
2. **Identity** — the whole admin surface except the login endpoint, and the
   whole store surface without a key, is rejected. An undefined `/admin/v1/...`
   path also returns **401** (had it been 404, the endpoint map would leak
   through the status code).
3. **Idempotency** — *after* identity; the record key is held together with the
   caller's identity. Individual paths can be exempted from this ring (and from
   this ring only); today TWO paths are — `POST /store/v1/graphql` and
   `POST /store/v1/carts` — and the rationale for both is in the "Hardening"
   section below.

A publishable key is **not a secret**: it is visible in the browser and its only
job is to bind the request to a sales channel — it carries no authority.

## The catalog is filtered by the sales channel

`GET /store/v1/products` reads the channels bound to the request's key from the
`Principal` and filters the catalog by them. (`corehttp.Principal` is the identity
the guard resolved for this request; on the storefront the only thing it carries
is the set of sales channels the publishable key is bound to.) The rule is one
sentence:

> A product with **no** channel assignment is visible in all channels; a product
> **with** an assignment is visible only in the channels it is assigned to.

The channel is **not taken from the query string**, it comes from the identity —
had it been taken from there, the filter would stop being an authorization and
turn into a display preference, and a client arriving with any publishable key
would read another storefront's catalog. The single-item endpoint
(`/store/v1/products/{id}`) is subject to the same filter, and a hidden product
returns the **same** error code as a product that never existed.

The binding is made from the admin side:

```
POST   /admin/v1/products/{id}/sales-channels
DELETE /admin/v1/products/{id}/sales-channels/{sales_channel_id}
GET    /admin/v1/products/{id}/sales-channels
```

> **Careful:** the way to take a product off the storefront is **not** to delete
> its last channel binding — by the rule, a product left with no assignment
> becomes visible in *every* storefront, that is, the exact opposite happens. To
> hide it, use the `status` field (`draft` / `archived`).

### The rule is applied on ADD TO CART too

Because the scope is not a display preference but an **authorization**, stopping
at the read surface is not enough. `POST /store/v1/carts/{id}/line-items` carries
the request's channels when it asks the catalog for the variant: for a variant out
of scope the catalog returns no record and the line is **never written**.

The rule is not written a second time — the flow asks `product` "is this variant
visible in these channels", and the answer is produced by the very SQL template
the storefront listing uses. The channels again come from the **identity**; the
nil / empty set / non-empty set distinction is identical to the one on the read
surface.

An out-of-scope variant returns the **same** error as a variant that never existed
(`404 cart_workflow_variant_unknown`) — a different class would give away the
product's existence.

> **Limit:** the check is where the variant **enters** the cart. The quantity
> update and cart completion paths do not ask about the scope again; the only path
> that can put a variant into a cart is adding a line, and a line already in a cart
> does not become unpayable because of a later edit moving the product to another
> channel. The decision is protected by
> `TestVariantReadsGoThroughTheChannelDecision` (see `internal/arch`): a new variant
> read either makes the channel decision or writes down its reason.

The strict alternative — "a product with no assignment is visible in no channel" —
is the more correct one and is the industry habit (publishing becomes an explicit
act). It was not implemented because the day it is turned on, every existing
installation's catalog empties at once; it has to be announced at a release
boundary (see [`CHANGELOG.md`](../CHANGELOG.md)). A secret key carries authority and
is not bound to a sales channel; input that mixes the two is not silently
corrected, it is rejected.

## Authorization (scope)

Identity is the question "who are you", authorization the question "what may you
do"; they are separate layers. `RequireAdmin` only resolves the identity, and
`RequireScope` enforces the authority endpoint by endpoint:

The vocabulary derives from a single rule:

| Endpoint | Required |
|---|---|
| `POST /admin/v1/auth/login` | — (identity is about to be established) |
| `GET /admin/v1/auth/me`, `POST /admin/v1/auth/logout` | identity only |
| `/admin/v1/**` **read** (GET, HEAD) | `<module>:read` |
| `/admin/v1/**` **write** (POST, PUT, PATCH, DELETE) | `<module>:write` |
| `/store/v1/**` | — (a publishable key carries no authority) |

`<module>` is the name of the module that owns the endpoint: `product:read`,
`order:write`, `promotion:write` … `admin` is the **super-scope** and covers them
all.

Four scopes name a resource that is not a module, because the surface they guard
belongs to no module: `personal-data:read`, `personal-data:disclose`,
`personal-data:erase` ([ADR 0029](adr/0029-the-embedder-is-the-data-controller.md))
and `audit:read` ([ADR 0037](adr/0037-the-audit-log-gains-a-reader.md)). They are
separate because each is a distinct power: an operator trusted to refund an order
is not thereby trusted to read the trail of every colleague's actions, and
reading the map of what is held about people is not permission to read a person.
There is deliberately no `audit:write` — rows are written by the framework and by
nothing else, and a scope naming a power nobody has is one somebody will try.

**The audit log** records one row per admin write — who called it, what they
called, what came back — and is read at `GET /admin/v1/audit-log`, newest first,
with keyset paging. It records the REQUEST rather than the change; what changed
is read from the record, which carries its own `updated_at`. Storefront requests
are not recorded (that surface is unauthenticated by decision) and neither are
reads — with ONE exception, this endpoint itself. Who read the record of who did
what is the question an incident starts with, and it is the one read somebody
with a stolen admin token makes. There is no endpoint that deletes a row and no
retention window; pruning is an operator's scheduled statement, because a log
the API can prune is a log an intruder can prune.

The one exception is the auth module's write endpoints: there, instead of
`auth:write`, `admin` itself is required. What is written at those endpoints is
authority itself (a user's authority, a key's authority, the channel a key will
see), so an identity that can write authority could make itself an admin in a
single request — it already is one; a separate name would show a boundary that
does not really exist.

**Privilege escalation is blocked in two layers**: the middleware closes the
endpoint, and the service refuses to grant an authority the caller *does not
itself hold*. The second layer is necessary because the first one's map may one day
be loosened.

A user with an **empty** scope set (empty slice, not nil) can log in but can do no
work at any protected endpoint. This is not an accident but a contract, and it is
audited by walking the router tree: `internal/e2e/authorization_test.go` goes to
every `/admin/v1` endpoint with an unauthorized token and expects **403**, so a
module that forgets to add the enforcement cannot stay quiet.

```bash
# 0) The first admin (only on an empty database)
ADMIN_BOOTSTRAP_EMAIL=admin@example.com \
ADMIN_BOOTSTRAP_PASSWORD='…' make run

# 1) Log in -> token
TOKEN=$(curl -s localhost:9000/admin/v1/auth/login \
  -H 'content-type: application/json' \
  -d '{"email":"admin@example.com","password":"…"}' | jq -r .data.token)

# 2) A protected endpoint
curl -s localhost:9000/admin/v1/auth/me -H "Authorization: Bearer $TOKEN"

# 3) The storefront's key: first the sales channel, then the publishable key
SC=$(curl -s localhost:9000/admin/v1/sales-channels \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d '{"name":"Default storefront"}' | jq -r .data.id)

PK=$(curl -s localhost:9000/admin/v1/api-keys \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d "{\"title\":\"storefront\",\"type\":\"publishable\",\"scopes\":[],\"sales_channel_ids\":[\"$SC\"]}" \
  | jq -r .data.key)

# 4) The store surface
curl -s localhost:9000/store/v1/products -H "x-publishable-api-key: $PK"

# 5) Log out (drops ALL of the caller's sessions)
curl -s -X POST localhost:9000/admin/v1/auth/logout -H "Authorization: Bearer $TOKEN"
```

**A publishable key is born bound to a sales channel.** The order cannot be
reversed: the key-creation body asks for `sales_channel_ids` and **a key is
produced even with an empty list** — but that key gets `401 unauthenticated` on
the store surface (`auth_no_sales_channel` in the server log), because the only
thing the identity carries *is* the channel, and a publishable key without a
channel can bind no request. A key can also be bound later:
`POST /admin/v1/api-keys/{id}/sales-channels`.

All three sentences of that paragraph are nailed down on the real binary:
`TestPublishableKeyWithoutChannelIsRejectedByStorefront` in
`internal/smoke/keys_test.go` creates the channel-less key (201), gets **401** on
the store surface, looks for the diagnostic code in the server's **log**, and then
binds the channel and gets in with the **same** key.

The plain key is returned **only in the creation response**; every other endpoint
gives its masked form (`pk_...WnjU`). If you lose it, the only way out is to revoke
it and create a new one. The admin side's key (`sk_…`) comes out of the same
endpoint with `"type":"secret"` and it **is** a secret: it carries authority and is
not bound to a sales channel.

**The first admin** is born through a seed step: because the admin endpoints are
protected, there is no way to create the first user over HTTP. The seed runs
**only when there is no user at all** — restarting is safe and it never changes an
existing installation's authorities. The two variables are given **together**; if
only one is given the application stops at startup.

**Leaving both empty** is legitimate for an installation that is already set up —
the users are already there — but on a **fresh database** the result is an
unmanageable installation: there is no user, the admin surface is entirely
protected except the login endpoint, and there is no way to create the first user
over HTTP; the store surface is closed too, because the publishable key is also
produced by an admin endpoint. The server still starts, and `/health` and `/ready`
return green. That is why zero users plus no seed configuration **stops startup**
in shared environments, and only produces a warning in local development (the
promise of `make up && make run` without a `.env` is kept there). The distinction is
the same as `JWT_SECRET`'s.

**Session revocation** happens two ways and both are **wholesale**: a password
change and `POST /admin/v1/auth/logout`. There is no way to drop a single device —
that would need a jti-based blacklist read on every request. An API key has no
session; it is closed with `POST /admin/v1/api-keys/{id}/revoke`.

If `JWT_SECRET` is not given, a **startup-specific random** secret is generated in
development (sessions drop on restart) and a warning is logged; in shared
environments config validation makes the secret mandatory.

**A session ends at a wall-clock deadline and never renews** ([ADR
0031](adr/0031-the-admin-session-is-twelve-hours.md)). `JWT_TTL` is twelve
hours by default: longer than a working shift, so an operator who starts a task
finishes it, and shorter than a day, so a token taken from a machine in the
evening is dead by the next morning. There is no refresh flow, which means the
lifetime is the WHOLE of the exposure — the two revocations above are the only
thing that shortens it, and nobody performs them for a token they do not know
was taken. That is why `APP_ENV` other than `development` caps `JWT_TTL` at
**twenty-four hours** and refuses to start above it: a week-long admin session
is not a preference the framework can tell apart from a mistake.

What an operator sees at the deadline is a login page saying the session
expired, with the page they were on carried through the sign-in and restored
afterwards. Both halves are panel-only: a JSON client gets a 401 and is expected
to sign in again itself.

## Hardening

### The defaults that ship

Four of the rings are not settings but defaults, and each one is there because
the obvious version of it is not enough:

- The compose ports bind to `127.0.0.1`; on a shared network (an office or cafe
  WiFi) Postgres and Redis are not reachable from outside.
- Redis starts with `requirepass`; a connection without a password is refused.
- The HTTP server defines `ReadTimeout`, `WriteTimeout` and `IdleTimeout`
  alongside `ReadHeaderTimeout` — against the Slowloris variant that drips the
  BODY byte by byte, `ReadHeaderTimeout` alone is not enough.
- If `ShutdownTimeout` runs out during shutdown, the open connections are
  **forcibly closed** with `Close`; `Shutdown` on its own does not tear down
  active connections.

| Component | Setting | If not configured |
|---|---|---|
| Rate limit | `RATE_LIMIT_PER_MINUTE`, `TRUSTED_PROXY_HOPS` | no-op (passes through) |
| Idempotency | `IDEMPOTENCY_TTL`, `IDEMPOTENCY_MAX_MEMORY_BYTES` | no-op (passes through) |
| Identity | `JWT_SECRET` | **rejects every request** |

Why not the same rule for all three: see
[ADR 0007](adr/0007-sertlestirme-arizada-davranis.md).

Under `GUARD_BACKEND=memory` the idempotency records are bounded by a **byte
budget** (`IDEMPOTENCY_MAX_MEMORY_BYTES`, 64 MiB by default). When the budget
fills, the OLDEST record is dropped and a retry arriving with that key is
processed AGAIN — that is, a duplicated side effect. This is a deliberate trade:
rejecting a new request when the budget fills would hand a single client sending
made-up keys the ability to close the store's entire write traffic, because the
key is chosen by the client. The eviction is logged at WARN and the budget is
written on every startup. Without the budget the only bound was the TTL and it
stopped the growth nowhere (measured: 10,000 records with 64 KiB bodies came to
630.69 MiB).

A POST/PUT/PATCH/DELETE carrying an `Idempotency-Key` header is processed once; a
repeat gets the same response with `Idempotency-Replayed: true`. Sending a
**different** body with the same key returns `409` — silently replaying the first
response would hide from the client that its second request was never processed.
The record key is **namespaced by the caller's identity**: of two callers who pick
the same key, neither sees the other's response. That separates two callers only
as far as the resolved identity names THE CALLER — on the storefront it names
the STORE, so every shopper is one caller there; what holds that surface apart
anyway, and the one place it did not, is in the "Hardening" section below.

The decision to record looks **only at the status code** — deriving it from the
body would mean teaching the core every surface's error shape. The price of that is
that a surface which reports its errors with `200` too stays outside the
protection; today there is exactly one such endpoint in the repository
(`POST /store/v1/graphql`) and the answer is not to make the record smarter but to
**take the endpoint out of the stack**: `GuardOptions.IdempotencyExempt`. The
exemption is **only** to the idempotency ring — an exempt path keeps going through
the rate limit and through identity — and it applies to the **full path**, not to a
whole prefix. The path is not written in the core (the core cannot import modules);
it is passed in from the composition root, from the module's `graph.Path` constant.

The other exempt path, `POST /store/v1/carts`, is out of the ring for a different
reason: not a wasted record but a **leak**. The record is namespaced by the
caller's identity, and on the storefront that identity is the PUBLISHABLE KEY —
the store's, identical for every shopper and visible in every browser — so all
shoppers share one namespace and the key that selects a record inside it is a
header the client chooses. A storefront POST whose path carries an id of its own
survives that, because the fingerprint includes the path: a second shopper
reusing a key on their own cart, their own customer record or their own order
gets `409 idempotency_key_reuse`, not somebody else's data. Where the path
carries no id, only the BODY is left to tell two shoppers apart, and that is
enough for `POST /store/v1/customers`: a guest registration must carry an e-mail
address (an empty one is refused), so two shoppers do not send the same bytes.
Cart creation demands nothing that has to differ — the server derives the region
and the currency — so two guests in the same country send byte-identical bodies,
and its response **creates** a capability: a second shopper sending the same key
and the same body was handed the first shopper's cart id, and a cart id is a
capability URL, since a cart has no ownership check (see
[`known-limits.md`](known-limits.md)). Measured, not deduced: two independent
callers, `Idempotency-Key: cart-9`, identical bodies, the same cart id in both
responses and `Idempotency-Replayed: true` on the second. The exemption costs a
duplicate cart when a client retries a creation that timed out — an abandoned
row, against handing a stranger someone else's cart. This path too comes from the
module's constant, `cartapi.StoreCartsPath`.

`TRUSTED_PROXY_HOPS` is the number of **trusted** reverse proxies between us and
the request, and it can be got wrong in both directions — but the two costs are
**not in the same class**:

| Value | Result | Class |
|---|---|---|
| Too high | The address the client wrote into `X-Forwarded-For` itself is taken for the real one; the attacker gets a fresh bucket on every request and bypasses the limit **entirely** | Security hole |
| Too low (`0`, behind a proxy) | `X-Forwarded-For` is never read and the key falls back to the connection's address; that address is the proxy's on every request, so `RATE_LIMIT_PER_MINUTE` becomes a ceiling not "per customer" but **"for the whole store"** | Capacity problem |

That is why the default is `0` and **has not been changed**: for an installation
facing the internet directly it is the right answer, and the configuration cannot
know which case applies. But a value that is too low cannot stay silent either —
in headless commerce, running behind a reverse proxy is very nearly the only
deployment shape — so a shared installation that has left `TRUSTED_PROXY_HOPS=0`
while the rate limit is **on** produces a **warning** at startup. If you are behind
a proxy, write the number of hops you trust in between (`1` for a single ingress).

`RATE_LIMIT_PER_MINUTE <= 0` does **not build the limiter at all** (in ADR 0007
zero means "off"). It is a legitimate choice, but it also leaves the login endpoint
without a quota, and an "off" nobody knows about is indistinguishable from a zero
typed by accident; in shared environments this is warned about too.

### One instance or several?

`GUARD_BACKEND` selects both at once:

| Value | Rate limit | Idempotency |
|---|---|---|
| `memory` (default) | **multiplied** by the number of instances | does **not work at all** across instances |
| `redis` | shared | shared |

Two gobit installations sharing the same Redis instance (staging and prod, say)
must use different `REDIS_KEY_PREFIX` values — that prefix is the only thing
separating two installations in one Redis; otherwise they spend each other's rate
limit and — worse — move each other's idempotency records.

The difference is not merely one of degree but of **kind**: the rate limit going
slack is a *speed* problem, no request is processed wrongly. Idempotency not
working is a *correctness* problem — two requests with the same key landing on
different instances are processed twice, which means two orders and two charges. If
you run more than one instance, `GUARD_BACKEND=redis` is mandatory; leaving
`memory` in a shared environment produces a warning at startup.

`GUARD_BACKEND=redis` and `EVENT_BUS=redis` share the **same** client; if both are
off, no connection is opened at all.

Several **instances** do not mean several **tenants**: the instances share the same
database and the same catalog, they are the horizontal copies of one installation.
The framework **recognises** no boundary between tenants — in none of the 82
tables the repository's migrations create (the modules' 72, plus the core's and
the plugins' ten) is there an answer to the question "whose row is this", and no
query carries such a filter. If you want to serve two customers from one
installation, the answer is two installations: one tenant = one installation =
one database = one process. Why this is a decision rather than a gap, which
options were rejected, and what would reopen the decision, is written in
[ADR 0009](adr/0009-cok-kiracililik-kurulum-siniri.md).
