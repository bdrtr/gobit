# The API surfaces: the generated document and the GraphQL storefront read

What the two machine-facing surfaces of this repository are, and what they
refuse to do. The first is the OpenAPI document generated from the router tree;
the second is the GraphQL storefront read endpoint, together with the limits
that make it safe to expose.

Who this is for: somebody integrating against gobit who needs to know what the
schema promises and where the storefront read surface stops, and somebody
operating a deployment who has to set the GraphQL limits. The entry point is
still [README.md](../README.md); this document is the reference the README used
to carry inline.

This file is in English because ADR 0012 makes language a property of the file
and every new file is English.

## What this document assumes

Three facts live in the README and are needed here:

- **Two surfaces, two identities.** `/admin/v1/**` is authenticated with a
  session token or a secret key; `/store/v1/**` is authenticated with a
  publishable key (`pk_…`) sent in the `x-publishable-api-key` header. The
  publishable key is not a secret: it carries no permission, and its only job is
  to bind the request to a sales channel.
- **The catalog is filtered by sales channel.** The sales channel is read from
  the request's identity (`Principal`), never from the query string — otherwise
  the filter would stop being an authorization and turn into a display
  preference.
- **The protection stack is bound to the prefix, not to the module.** Rate
  limiting, authentication and idempotency come from `corehttp.APIGuards`, which
  wraps the `/admin/v1` and `/store/v1` prefixes in the composition root.

## OpenAPI

The schema is **generated** from the router tree, not hand-written:

```bash
curl -s localhost:9000/openapi.json | jq '.paths | keys'
```

The endpoint publishes only the route patterns, not data. A hand-written schema
would start lying silently at the first route change.

Body schemas are **not hand-written either; they are derived from the Go types**
— for the same reason: a hand-written field list falls short the day a field is
added to the DTO, and nobody notices. The derivation imitates `encoding/json`'s
behavior (the tag, `omitempty`, unexported fields, embedded struct flattening
and shadowing); where the imitation is incomplete the schema is worse than no
schema at all — the client sends a field name it believes to be right.

Modules describe their own endpoints through the optional `openapi.Describer`
interface:

```go
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }
```

A method was NOT added to the `module.Module` contract: an undescribed module is
a valid model too, and making it mandatory would have broken every module.

`core/openapi` is a **published** package ([ADR
0035](adr/0035-the-schema-vocabulary-is-published.md)), so a module written
outside this repository describes its endpoints the same way. Until 2026-09-07
it was not: the package sat under `internal/`, an out-of-tree module could not
name the type, and its endpoints entered the document bodiless with nothing
saying so.

**A bodiless endpoint does not look missing.** It appears with its path, its
method and its security, only without a body — a generated client for it
compiles and sends nothing, and a reader cannot tell "takes no body" from
"nobody wrote it down". Two things now say so out loud: the server logs the list
at startup, and `internal/e2e/testdata/undescribed_routes.txt` holds it as a
ledger that **may only shrink**. It opened at thirty-eight and is at ZERO — the
empty file stays, because deleting it would delete the ratchet along with the
debt and the next forgotten endpoint would have nothing to fail against.

A component's name carries the module that owns it — `CustomerAddress`,
`CartAddress` ([ADR 0036](adr/0036-a-component-name-carries-its-module.md)).
Without that, two modules could not both have a Go type called `Address`, and
the failure was not proportional: a name clash makes the WHOLE document
unbuildable, so four modules left twenty-five endpoints bodiless rather than
take `/openapi.json` down for everyone. The prefix is dropped when the type name
already begins with it, so `Product` stays `Product`.

You can generate a client from the schema:

```bash
make openapi-validate              # validate with the real generator
make openapi-client DIL=go         # or typescript-fetch, python, …
```

The SDK is **not vendored** into the repository: since the schema is generated
from the router, versioning a second artifact and keeping it in sync with the
schema is needless weight. The command is documented; whoever wants one
generates it in their own language.

## The GraphQL storefront read surface

The catalog can also be read from a second surface:

```bash
curl -s localhost:9000/store/v1/graphql \
  -H "x-publishable-api-key: pk_…" -H 'content-type: application/json' \
  -d '{"query":"{ products(limit: 5, q: \"t-shirt\") { count items { handle variants { sku priceSet } } } }"}'
```

The surface is kept **narrow**: the `products` and `product` queries, and **no
mutation**. The contract is the file
`internal/modules/product/graph/schema.graphqls` — an inspectable artifact just
like the OpenAPI document; the Go side is **generated** from it (`make gen`,
gqlgen).

The decisions and their rationale:

| Decision | Rationale |
|---|---|
| Resolvers call the **storefront service** (they do not descend to the repository and write no new SQL) | The sales channel visibility rule lives in ONE place; a second implementation drifts silently and one of the surfaces leaks the catalog |
| The sales channel is **not an argument**, it is read from the `Principal` | As an argument the filter would stop being an authorization and turn into a display preference (the same rationale as REST) |
| The endpoint lives under `/store/v1` | Publishable key verification and the rate limit come **automatically** from the prefix stack; a separate prefix would mean writing the identity and quota rules a second time |
| **POST only** (GET gets 405) | Since the response varies with the sales channel, GET has no caching gain; it does have costs (the query lands in logs and browser history, a long query dies with a 414) |
| The endpoint is **exempt from idempotency** (`Idempotency-Key` is ignored) | Being a POST does not make it a write: there is no side effect for a record to protect, and what it stored would only be a stale catalog. The real rationale is this: by the GraphQL contract an internal error also returns **200**, so the "a 5xx is not recorded" protection never engages on this surface and a transient fault would be replayed for the whole `IDEMPOTENCY_TTL` — the client would keep receiving the same error body even after the fault was fixed. The exemption also ends the fingerprinting of a 900 KiB query into memory **before** the 64 KiB body gate |
| `priceSet` / `inventoryItem` are **JSON scalars** | Typing them would mean copying the pricing/inventory schema into `product`; the record already arrives at this module loosely typed. The price was accepted: the field names cannot be learned from the schema, they are read from the owning module's documentation. In return the field may be `null` — if the provider is not installed we do not have to invent a "product with a price of zero" |
| **Seven gate families** on a single document: fragment expansion, depth, complexity, field repetition, introspection, parsing and **response bytes** | On this endpoint the cost is decided by whoever **writes** the query; the rate limiter, meanwhile, counts a document carrying hundreds of root queries under aliases as **one** request. The detail and the measurements are below |
| **Introspection is on** (`GRAPHQL_INTROSPECTION=false` turns it off), but behind gates of its own | The schema is a file that sits inside this repository: turning it off hides nothing from an attacker and blinds code generators. For a deployment that adds its own fields to the schema the calculation changes, which is why the switch exists. The switch is no longer an emergency valve: the root count and the depth of introspection are limited separately |
| The error body goes through the core's `WriteError`; the distinction looks not at the error's **type** but at its **source** | A second implementation of the rule "which error is handed to the client as-is" would leak server-internal detail the day it drifted — and it did leak: the old type-based distinction was passing an unclassified driver error (the connection string, the password, the SQL text) through unmasked and unlogged. The codes are the same as REST's (`extensions.code`) |

### Hardening: when the client decides the cost, the server sets the limit

In REST the server decides the cost of a request — the path is fixed, the body
is fixed, one request is one query. In GraphQL the client writes the **shape**
of the query, that is, its cost; the rate limiter, meanwhile, counts the same
thing on both surfaces: **one request**. There are seven gate families and each
one catches the document the others cannot see:

| Gate | Default | What it counts | What it catches |
|---|---|---|---|
| Expansion | `GRAPHQL_MAX_SELECTIONS=10000` | selections after the fragments have been expanded | **The fragment bomb.** An `f(k) = ...f(k-1) ...f(k-1)` chain writes **1,127 bytes** at 26 levels and expands to 2^26 selections; every calculation that walks the tree (depth, repetition, gqlgen's complexity) would hang there. It runs first and protects the others; when the budget runs out the traversal is cut short |
| Depth | `GRAPHQL_MAX_DEPTH=10` | levels | The nested query. The schema is not cyclic today (the deepest legitimate path is 5), but the day a field refers back the query descends as far as the client writes, not as far as the schema goes |
| Complexity | `GRAPHQL_MAX_COMPLEXITY=50000` | fields x elements | The shallow but expensive query: the whole tree of a hundred products with `limit=100`, or hundreds of root queries stacked under aliases |
| Field repetition | `GRAPHQL_MAX_FIELD_REPETITION=20` | the same `(type, field)` in the same set | **The stacking of the same field under aliases.** Complexity prices the *number* of fields, not the bytes: a `description` requested 489 times sits exactly on the ceiling of 50,000 and would produce a 204.9 MiB response |
| Introspection roots | `GRAPHQL_MAX_INTROSPECTION_ROOTS=2` | `__schema` / `__type` in the document | Introspection stacked in a single document. The roots are *shallow*, which means the depth gate cannot see them at any setting |
| Introspection depth | `GRAPHQL_MAX_INTROSPECTION_DEPTH=15` | levels | The depth of the introspection tree. It is **separate** from the data ceiling: the standard introspection query is 13 levels deep, and with a single ceiling the data limit would have had to be raised above 13 as well |
| Body | 64 KiB (fixed) | bytes | Everything above can be measured only **after** the document has been parsed; the cost of parsing is bound by this gate and the token gate alone |
| Token | 8,192 (fixed) | tokens | The document that fits in the body but carries thousands of tokens (64 KiB means 32,000 tokens with the cheapest tokens). It is the cheapest gate: the document is not parsed to the end |
| Response | `GRAPHQL_MAX_RESPONSE_BYTES=4194304` | **the bytes that actually happened** | Everything an estimate misses. The other gates look at the document and *estimate* the cost, and none of them can know a field's **content**; the last word belongs to measurement |

The unit of complexity is "how many fields get resolved" and on list fields it
is **multiplied by the number of elements** — giving a fixed cost would make
exactly the expensive query look cheap. Root queries additionally carry a fixed
base (one database round trip; it does not get cheaper when fewer fields are
selected).

The calibration is **measured** and pinned in the `calibrationDocuments` table
inside `graph/limits_test.go`; the byte column was taken with the measurement
fixture in the same file (a product with a 4 KiB description, three variants,
price and stock records):

| document | request | complexity | response | outcome |
|---|---:|---:|---:|---|
| product page (PDP, everything included) | 643 B | 2,368 | 6.8 KiB | passes |
| category list (24 products, card + price) | 118 B | 2,344 | 15.1 KiB | passes |
| ALL fields on the default page (20 products x whole tree) | 655 B | 28,440 | 136 KiB | passes |
| ALL fields with `limit=100` | 667 B | 138,200 | 680 KiB | complexity |
| `products { count }` with 400 aliases | 9.7 KiB | 408,000 | 8.5 KiB | field repetition |
| **`description` with 489 aliases, `limit=100`** | 8.5 KiB | **50,000** | **204.9 MiB** | field repetition |
| **`description` with 1500 aliases, default page** | 26.8 KiB | 31,020 | **125.7 MiB** | field repetition |
| **`__schema` with 302 aliases** | 44.7 KiB | **0** | **5.00 MiB** | token (9,364) |
| **`__type` with 448 aliases** | 58.5 KiB | 7,168 | 1.32 MiB | token (14,786) |
| **302 small `__schema` roots** | 9.3 KiB | **0** | 0.84 MiB | introspection roots |
| **a fragment folded 26 levels deep** | **1.1 KiB** | not measurable (it hangs) | — | expansion budget |

The response column of the bold rows is the response **measured before those
gates were added**; today none of them is executed (the response column of a
rejected document shows what it would produce if it were run). What the table
really says is this: looked at through the complexity column alone, every one of
these documents is **innocent** — the 489-alias document sits exactly on the
ceiling, the complexity of the introspection documents is zero, and the
complexity of the fragment bomb cannot be calculated at all. Counting fields was
never asking about the very dimension it missed.

The comparison point is the `limit=100` row: pulling the same hundred products
with all their fields is **680 KiB**, and asking REST for it with
`GET /store/v1/products?limit=100` is of the same order. What produces 204.9 MiB
is not more *records* but **the same record serialized 489 times** — and a REST
client cannot ask for that.

When the response limit is hit, **half a JSON is not sent**: while no byte of
the body has gone out (today's POST transport writes the response in one go) the
overflowing body is thrown away and a complete error envelope is written in its
place; if part of it has already gone out a complete document is no longer
possible and the connection is dropped with `http.ErrAbortHandler`. A truncated
body either drops the client into a parse error it cannot diagnose or — worse —
gets mistaken for a short result.

The query cache is limited **not by entry count but by bytes** (8 KiB per entry)
and a document enters the cache only **after it has passed every gate**: gqlgen
stores the document immediately after validation, while the limit extensions run
after that — so rejected documents were taking up room too (measured: 100
rejected documents left 171.8 MiB of permanent heap after `runtime.GC`) and were
evicting the storefront's real documents from the cache.

The limits can be **raised, not removed**: `0` or a negative value does not mean
"unlimited", it means "use the default"; given as an environment variable, a
`0` or negative value stops the application at startup. (Do not confuse this
with `RATE_LIMIT_PER_MINUTE`, where `0` really does mean "off" — see
[ADR 0007](adr/0007-sertlestirme-arizada-davranis.md).) The body and token
limits are fixed: both bind the parser, and loosening them is not a capacity
choice but opening the parser up to the client. All of the remaining **seven
gates** have an environment variable, and because their defaults are repeated in
two places (the core configuration and the `graph` package that enforces the
limit) the tie is pinned by a test (`internal/arch`). The test does not merely
compare today's values; it forces **every** new limit added to `graph.Options`
to have a counterpart in the core: a limit that cannot be set forces a
deployment to fork the code, and the operator finds that out only in production.

### The error policy: the distinction looks at the source, not the type

On the error body side there is also a single rule, and the rule is about the
**source**:

- **Service errors** — everything coming from beneath the resolver, *typed or
  not* — go through the core's `WriteError` path. That is, an unclassified error
  (like the driver's `pq: … password=… ; SELECT …` text) counts as `KindInternal`
  here too: the client sees the generic message and the `internal_error` code,
  and the real text is **logged**. The distinction once looked at the error's
  *type* and handed the untyped one to the client as it was, without logging it
  at all — so an error that was masked in REST could escape from this surface.
- **Protocol errors** — parsing, validation and the limit gates — are not
  masked; masked, the client could not fix its query. For the same reason they
  are **not logged** as server errors: a client's typo would turn the log into a
  pipe the client can fill.
- **Transport errors** — a request that fails before the document can even be
  read — are returned with our own text. When gqlgen's transport could not
  decode the JSON it was appending the **raw body** to the error message; up to
  64 KiB of attacker-controlled text was entering both the response and the logs
  of any middleware that records the response.

`GRAPHQL_INTROSPECTION=false` **also turns off the suggestions**, and this is
the completion of the switch's promise: even with `__schema` closed, the
validator was retailing the schema's names by saying
`Did you mean "products" or "product"?` — and because it collects every error
into a single response, dozens of names could be tried in one request. What
closes is the *enumeration* of the names, not their being *guessed* one by one;
the only way to close that too would be to delete the validation messages
entirely, that is, to make the surface undebuggable for the legitimate client as
well. With the same switch, a document asking for `__schema`/`__type` is now
rejected without being executed (`INTROSPECTION_DISABLED`); `__typename` is not
an introspection root and goes on working.

The codes live under `extensions.code`: `DEPTH_LIMIT_EXCEEDED`,
`COMPLEXITY_LIMIT_EXCEEDED`, `FIELD_REPETITION_LIMIT_EXCEEDED`,
`INTROSPECTION_LIMIT_EXCEEDED`, `INTROSPECTION_DISABLED`,
`RESPONSE_LIMIT_EXCEEDED`, `SELECTION_BUDGET_EXCEEDED`,
`REQUEST_BODY_TOO_LARGE`, `REQUEST_DECODE_FAILED`.
