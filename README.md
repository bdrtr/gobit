<p align="center">
  <img src="./logo.png" alt="the gobit logo" width="220">
</p>

# gobit

A modular, headless commerce **library** written in Go. It is not a template you
copy and edit: the embedding project tracks gobit as a dependency in `go.mod`,
adds its own module and its own plugin, and assembles the installation through
the published facade (ADR 0025). At run time it is **one process** carrying a
**modular monolith** — the modules do not know each other at compile time, the
decision about who talks to whom is made in a single package (`internal/app`),
and because the isolation holds, any one module can later be extracted into a
separate service.

This is the whole of the surface an embedding program sees:

```go
gobit.New().Version(version).Add(myModule).Use(myPlugin).Main(os.Args[1:], os.Stdout)
```

**Why** the architecture is built this way: [`docs/mimari.md`](./docs/mimari.md).

## Quick start

```bash
make up      # Postgres 16 + Redis 7 (waits until they are healthy)
make run     # starts the server on :9000
curl -s localhost:9000/health
# {"status":"ok","version":"v0.8.0"}
curl -s localhost:9000/ready
# {"status":"ok","version":"v0.8.0","checks":{"postgres":{"status":"ok"}}}
```

`/health` reports only that the process is alive; `/ready` tests the
dependencies but does not give them all the same vote — Postgres cuts traffic,
Redis degrades it. The difference between the two, the `degraded`/`unavailable`
table and why the `READINESS_DEGRADED_TIMEOUT` budget has to be short are in
[`docs/operating.md`](./docs/operating.md).

For every target, `make help`.

## Requirements

| Tool | Version | What for |
|---|---|---|
| Go | 1.26+ | building and the tests |
| Docker + Compose | v2+ | Postgres, Redis, the tracing collector, the client generator |
| make | GNU Make | every target |
| curl + jq | — | the shell examples in the documents |

`make tools` installs `golangci-lint` and `sqlc` under `./bin` at pinned
versions.

## Configuration

Every setting is read from an environment variable (12-factor) and the defaults
agree with `deploy/docker-compose.yml`, so no `.env` file is needed locally.
Customise with `cp .env.example .env`.

**The written record of the settings is `.env.example`**, and it cannot drift
from `internal/core/config/config.go`: a test walks `Config` by reflection and
checks every `env` tag against the document in both directions.

The handful that has to be set by hand:

| Variable | When |
|---|---|
| `DATABASE_URL` | **mandatory** when `APP_ENV=production` — if it has not been overridden the application stops at startup |
| `REDIS_URL` | the same rule |
| `JWT_SECRET` | without it the identity layer **rejects every request** (ADR 0007) |
| `JWT_TTL` | how long an admin session lasts; twelve hours by default and it **never renews** (ADR 0031). Capped at twenty-four hours in a shared environment |
| `APP_ENV` | any value other than `development` counts as a shared environment and turns the warnings on |
| `EVENT_BUS` · `GUARD_BACKEND` | both must be `redis` if you run more than one instance |
| `PLUGINS` | the names of the plugins to install, for example `PLUGINS=search-pg` |

Precedence, the production guard, the shell semantics of `.env` and all the rest
are in [`docs/operating.md`](./docs/operating.md).

## Directory layout

```
gobit.go              # the PUBLISHED facade: New().Version().Add().Use().Main()
core                  # the PUBLISHED contracts — eighteen packages (ADR 0026,
                      # widened by ADR 0069): errors, db, container, module,
                      # eventbus (+outbox), link, query, provider, plugin,
                      # http (+redisguard), audit, errorreport, personaldata,
                      # openapi, jobreport
internal/app          # the COMPOSITION ROOT (ADR 0027): config -> logger ->
                      # container -> router -> listen; the operator subcommands
                      # (migrate, stuck, recover, jobs, deadletters, seed)
cmd/server            # the binary: the smallest program that can run gobit —
                      # and the example to copy
internal/core         # the unpublished core: config, logger, job, workflow,
                      # observability, page
internal/modules      # eighteen isolated commerce modules (product, pricing,
                      # inventory, cart, order, payment, …)
internal/workflows    # cross-module sagas (cart, checkout, invoicing,
                      # fulfilling, returns, datasubject)
internal/adminui      # the admin panel: a fourth tree (ADR 0011)
plugins               # in-tree plugins (search-pg, error-sentry, file-s3, …)
examples/plugin       # a SEPARATE module: proof that the published surface
                      # compiles from outside
examples/starter      # a SEPARATE module: an example application that imports
                      # gobit and is COMPILED AND RUN
deploy                # docker-compose, Dockerfile
```

## The enforced architecture rules

Isolation is checked before the build by `depguard` in `.golangci.yml`: `core/**`
and `internal/core/**` cannot import the modules (the plan's Principle 2.4), no
module can import another module (Principles 2.1 / 2.4 — eighteen modules x
sixteen prohibitions = complete isolation), and cross-module access goes through
a narrow interface resolved from the container.
When a module is added, the `depguard.rules` list is updated with it; the list is
kept **by hand**, but forgetting it does not leave the rule unenforced —
`TestModulesDoNotImportEachOther` walks the module tree, looks at the real import
graph, and knows nothing about that list.

The tests under `internal/arch` enforce the **behavioural** invariants. Their
common rule is this: **they walk the structure, they keep no list of names.** A
test that keeps a list applies the rule only for *today* — the case added
tomorrow silently stays outside it.

| Invariant | Where it is enforced | The fault that happened |
|---|---|---|
| Modules do not import each other | `TestModulesDoNotImportEachOther` | — (a second line of defense alongside depguard) |
| Every module is registered in the composition root | `TestEveryModuleIsRegisteredInTheCompositionRoot` | The whole of phases 8 and 9 was written, its tests were green, and **not one** of the `/admin/v1/**` endpoints had been mounted |
| Every declared link is **read** | `TestTheLinkDefinitionsAreTraversed` | The sales-channel link was being written and never read; on its first run the test found **four dead links** |
| Every published event topic has a subscriber | `TestTheEventTopicsHaveASubscriber` | `order.placed` went a long time with no subscriber and the event did nothing |
| Every `env` tag is in `.env.example` with the same default | `TestTheEnvExampleAgreesWithTheConfigDefaults` | `.env.example` said "the **two** limits below"; there were seven |
| Every path reading a `variant` makes a sales-channel decision | `TestVariantReadsGoThroughTheChannelDecision` | The scope was enforced on the read surface and not on add-to-cart: channel A's variant could be bought with channel B's key |
| Every path and symbol in the documents resolves | `TestTheReferencesInTheDocsResolve` | An independent verification broke both a symbol and a path in an ADR and `internal/arch` stayed green |
| No Turkish outside the ledger | `TestNoTurkishOutsideLedger` | A rule that looked only at diacritics would lie after a single transliteration pass (ADR 0012) |

That is eight of them. The rest — the published surface, the interop consumers,
the panel's vocabulary, the GraphQL limits, the error body, the ADR references —
are in `internal/arch`, one test per invariant, each with the fault that made it
exist written in its godoc.

All of these tests are **verified by mutation**: they have been shown to fail
when the invariant is deliberately broken. An architecture test that cannot be
made to fail is worse than one that does not exist — it gives the feeling of a
guarantee without giving the guarantee.

If an **exemption** from an invariant is needed, the mechanism is in the code and
a justification is mandatory; and exemptions **fail the test when they go stale**:
once the exempted thing no longer breaks the rule, the line has to be deleted. An
exemption is a debt, and a debt that has been paid does not stay in the book.

## Personal data: the mechanism is ours, the responsibility is yours

Three endpoints — `GET /admin/v1/personal-data` maps which column of which table
holds a person, `POST /admin/v1/personal-data/disclosure` assembles one person's
file from every holder, and `POST /admin/v1/personal-data/erasure` erases them
and reports what each module did. Every module answers an erasure with
**deleted**, **anonymized** or **retained**, and one that retained says what it
kept and why: an issued invoice is a legal document.

**None of it runs by itself, and that is deliberate.** gobit is not the data
controller; the application embedding it is
([ADR 0029](./docs/adr/0029-the-embedder-is-the-data-controller.md)). A library
cannot choose your retention period or your lawful basis. **A gobit that is
installed and never called leaves a compliance problem the framework cannot
see.**

## Why it is built this way

This repository started from a brief: an architecture note dated 6 September
2026. It was a BRIEF and not a description — what follows is what became of it,
and where the tree decided otherwise. In case of conflict the ADR wins.

**A library, not a fork.** The embedding project tracks gobit in `go.mod` and
assembles the installation through the published facade (ADR 0025). The fork
model makes projects diverge and turns upgrading into a nightmare; that was the
brief's first sentence and it held.

**The panel becomes a client of the API — half built.** ADR 0030 decided the
panel becomes a single-page client of `/admin/v1`, ADR 0076 moved the first
screen there, and five screens are still rendered on the server. It stands open
in the defect ledger (D34), and that is why this line exists: a decided future
is not written as a present fact.

**Measure, do not guess.** The brief's list of common mistakes had *offset
pagination* on it. The measurement took it off: over 52,000 rows the first page
costs 0.31 ms, about 50,000 rows deep it costs 34.71 ms, and a keyset seek stays
flat at 0.06–0.08 ms (`internal/core/page`). So offset is not a mistake, it is a
mistake at DEPTH — and the cursor went to the listings whose rows grow with the
shop's trade. The brief carried that correction itself; so does the way this
repository works.

**The technology choices held.** PostgreSQL + `pgx` + `sqlc` (no ORM, type-safe
SQL), golang-migrate for migrations, and tests against a real Postgres through
`testcontainers` — the repository does not mock its own repository. Logging is
`slog`; OpenTelemetry and error reporting sit in the plugin slots
(`plugins/errorotlp`, `plugins/errorsentry`).

**Three places the brief did not hold, by name.** Redis is here but NOT as a
cache: it carries the event bus and the request guard, the product/category
cache was never built, and to this day no ADR mentions one. Search did not go to
Meilisearch; it stayed on PostgreSQL full-text (`plugins/searchpg`). NATS
appears nowhere — the outbound event became `plugins/webhookout` and the outbox
relay, the relay with a backoff and a dead letter behind it.

**Money, stock, idempotency — the brief's three tightest lines.** Money is never
a float, it is integer minor units. Stock moves under a lock, and overselling is
something the schema refuses. An idempotency key is mandatory on payment and on
order creation; a repeated request does not produce a second order.

**AI is not a tool called from outside, it is a subsystem.** The first task was
review moderation and that is how it arrived: the model produces a SUGGESTION,
the suggestion is stored (ADR 0066), a filter shows it to the operator (ADR
0073), and a human has the last word — whether the human agreed with the
suggestion is recorded too (ADR 0074).

**Half of the Turkey-specific list stands.** The PayTR plugin is here
(`plugins/paymentpaytr`), so are VAT and tax regions, and so are the disclosure
and erasure endpoints on the KVKK side (ADR 0033). The e-invoice integration and
the domestic carrier APIs are NOT — the invoice module produces documents, it
does not connect to e-fatura.

**What the brief did not foresee, and what really grew.** The decision ledger
(`docs/adr/`), the defect ledger (`docs/gaps.md`), and the gates that read the
prose itself: a route address, a count or a cross-reference written in a
document is verified by a test. What sets this repository apart is not a feature
on a list; it is that.

## Where to read further

| Document | What it answers |
|---|---|
| [`docs/adr/README.md`](./docs/adr/README.md) | The INDEX of the decisions: 115 records, each with its decision in one sentence. In case of conflict, **the ADR wins** |
| [`docs/mimari.md`](./docs/mimari.md) | The architecture narrative: layers, the life cycle of a request and of a module, data, sagas, the core packages |
| [`docs/gaps.md`](./docs/gaps.md) | The defect ledger: every fault this repository found in itself, one sentence and the ADR that closed it |
| [`docs/known-limits.md`](./docs/known-limits.md) | The known limits: twenty-seven items in six groups — identity and authorization, sales channel scope, the category tree, tax, installation and operation, the limit of the invariants |
| [`docs/security.md`](./docs/security.md) | Identity and authorization: the two surfaces, the scope dictionary, the hardening rings, an end-to-end curl walkthrough |
| [`docs/commerce-flows.md`](./docs/commerce-flows.md) | From cart to order: who owns a flow, who decides the price, which warehouse it ships from |
| [`docs/api-surfaces.md`](./docs/api-surfaces.md) | The OpenAPI document and the GraphQL storefront surface, with the limits the server sets |
| [`docs/extending.md`](./docs/extending.md) | Plugins, providers and domain events — how a capability is added |
| [`docs/operating.md`](./docs/operating.md) | Running it: `/health` and `/ready`, the configuration, the bus backends, observability, the make targets |
| [`docs/measurements/`](./docs/measurements/) | The numbers behind the decisions: probe output and reproductions |
| [`CHANGELOG.md`](./CHANGELOG.md) | What changed, release by release |

## Phase status and version

**All ten phases** of the roadmap are complete, from the project skeleton to the
GraphQL storefront surface and B2B; what was found after the roadmap ended is
tracked in the releases. The current version is **v0.8.0**, and throughout `0.x`
**breaking changes may arrive in minor versions** — the surface freezes with
`1.0.0`.
