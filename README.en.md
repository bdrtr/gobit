# gobit

[T&uuml;rk&ccedil;e](./README.md) · **English**

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
| curl + jq | — | the examples in the documents |

`curl` and `jq` are a dependency of the **documents**, not of the application:
the shell examples in the documents use both (without `jq` the
`TOKEN=$(… | jq -r .data.token)` line produces an empty token and the next
request gets a `401`).

`make tools` installs `golangci-lint` and `sqlc` under `./bin` at pinned
versions.

## Configuration

Every setting is read from an environment variable (12-factor) and the defaults
agree with `deploy/docker-compose.yml`, so no `.env` file is needed locally.
Customise with `cp .env.example .env`.

**The written record of the settings is `.env.example`.** That file and
`internal/core/config/config.go` cannot drift apart: a test walks `Config` by
reflection and verifies that every `env` tag is written in the document and that
the value there is the same as the `envDefault`; in the reverse direction, a
variable with nothing reading it cannot be left behind either.

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
internal/modules      # seventeen isolated commerce modules (product, pricing,
                      # inventory, cart, order, payment, …)
internal/workflows    # cross-module sagas (cart, checkout, invoicing,
                      # fulfilling, returns, datasubject)
internal/adminui      # the admin panel: a fourth tree (ADR 0011)
plugins               # in-tree plugins (search-pg, error-sentry, file-s3, …)
examples/plugin       # a SEPARATE module: proof that the published surface
                      # compiles from outside
examples/starter      # a SEPARATE module: an example application that imports
                      # gobit and is COMPILED AND RUN
migrations            # the global (core) migrations
deploy                # docker-compose, Dockerfile
```

## The enforced architecture rules

Isolation is checked before the build by `depguard` in `.golangci.yml`: `core/**`
and `internal/core/**` cannot import the modules (the plan's Principle 2.4), no
module can import another module (Principles 2.1 / 2.4 — seventeen modules x
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
| Publishing is a deliberate choice | `TestThePublishedPackagesAreTheDeclaredOnes` | Opening a directory would have turned into a permanent public commitment |
| No published package imports `internal/` | `TestNoPublishedPackageImportsAnInternalOne` | A "published" package that cannot be compiled from outside |
| The surface really runs from out of tree | `TestTheOutOfTreeStarterRuns` | An example that compiles but does not run proves nothing about running |
| Every module is registered in the composition root | `TestEveryModuleIsRegisteredInTheCompositionRoot` | The whole of phases 8 and 9 was written, its tests were green, and **not one** of the `/admin/v1/**` endpoints had been mounted |
| Every registered module is set up in the e2e harness | `TestEveryRegisteredModuleIsSetUpInTheE2EHarness` | The registration line compiling and the module actually running are not the same thing |
| Every registered `*.interop` is resolved | `TestTheInteropSurfacesHaveAConsumer` | A dead contract; `Host.AddModule` was never called |
| Every published event topic has a subscriber | `TestTheEventTopicsHaveASubscriber` | `order.placed` went a long time with no subscriber and the event did nothing |
| Every declared link is **read** | `TestTheLinkDefinitionsAreTraversed` | The sales-channel link was being written and never read; on its first run the test found **four dead links** |
| Every `env` tag is in `.env.example` with the same default | `TestTheEnvExampleAgreesWithTheConfigDefaults` | `.env.example` said "the **two** limits below"; there were seven |
| No variable in the document is orphaned | `TestNoVariableInTheEnvExampleIsOrphaned` | A deleted setting left in the document promises the operator a lever that does nothing |
| The plugin names in the documents are registered names | `TestThePluginNamesInTheDocsAreReal` | An example calling a plugin by its directory name; an installation copying it stopped at startup with "unknown plugin" |
| The error body is written only by `corehttp.WriteError` | `TestErrorResponsesAreWrittenInOnePlace` | The GraphQL server tried to repeat the rule and drifted; a DSN with its password reached the client and was never logged |
| Every GraphQL `Max*` limit has a counterpart in the core | `TestTheGraphQLLimitDefaultsAgreeWithTheConfig` | Five hardening limits had no environment variable; the operator could not set them |
| The panel's status list is the module's accepted vocabulary | `TestThePanelStatusOptionsAgreeWithTheModules`, with `TestTheProductStatusReaderIsNotBlind` under its reader | Both sides of the comparison came from the same place — the module's side was a three-element slice written inside the test — so a fifth status added to the module left the gate green and the operator could never have selected it |
| Every path reading a `variant` makes a sales-channel decision | `TestVariantReadsGoThroughTheChannelDecision` | The scope was enforced on the read surface and not on add-to-cart: channel A's variant could be bought with channel B's key |
| Every path and symbol in the documents resolves | `TestTheReferencesInTheDocsResolve` | An independent verification broke both a symbol and a path in an ADR and `internal/arch` stayed green |
| Every ADR reference names a real record | `TestTheADRReferencesResolve` | A reference to a renumbered record silently points at another decision |
| No Turkish outside the ledger | `TestNoTurkishOutsideLedger` | A rule that looked only at diacritics would lie after a single transliteration pass (ADR 0012) |

All of these tests are **verified by mutation**: they have been shown to fail
when the invariant is deliberately broken. An architecture test that cannot be
made to fail is worse than one that does not exist — it gives the feeling of a
guarantee without giving the guarantee.

If an **exemption** from an invariant is needed, the mechanism is in the code and
a justification is mandatory; and exemptions **fail the test when they go stale**:
once the exempted thing no longer breaks the rule, the line has to be deleted. An
exemption is a debt, and a debt that has been paid does not stay in the book.

## Personal data: the mechanism is ours, the responsibility is yours

gobit can erase one person from the whole system, assemble that same person's
file, and map what it keeps about people and where:

| Endpoint | What it does |
|---|---|
| `GET /admin/v1/personal-data` | Lists which column of which table holds a person — about nobody in particular |
| `POST /admin/v1/personal-data/disclosure` | Gathers one named person's file from every holder |
| `POST /admin/v1/personal-data/erasure` | Erases that person and reports what each module did, with its reason |

An erasure gets one of three answers from every module — **deleted**,
**anonymized** or **retained** — and a module that retained says *what* it kept
and *why*: an issued invoice is a legal document and is not deleted, and being
able to say that in one sentence is exactly what answering a data subject
requires.

**None of this runs by itself, and that is deliberate.** gobit is not the data
controller; the application embedding it is
([ADR 0029](./docs/adr/0029-the-embedder-is-the-data-controller.md)). A library
cannot choose your retention period, your lawful basis or the wording of your
consent. gobit gives you the mechanism and the declaration of what it holds;
calling those endpoints, accepting a request and deciding the window are yours.
**A gobit that is installed and never called leaves a compliance problem the
framework cannot see.**

## Where to read further

| Document | What it answers |
|---|---|
| [`docs/adr/README.md`](./docs/adr/README.md) | The INDEX of the decisions: eighty-seven records, each with its decision in one sentence. In case of conflict with the plan, **the ADR wins** |
| [`docs/measurements/`](./docs/measurements/) | Measurements: numbers, probe output, reproductions. An ADR links to one in a single line; nobody has to read them end to end |
| [`docs/mimari.md`](./docs/mimari.md) | The architecture narrative: layers, the life cycle of a request and of a module, data, sagas, technology choices, the core packages |
| [`docs/gaps.md`](./docs/gaps.md) | The defect ledger: every fault this repository found in itself, one sentence and the ADR that closed it |
| [`docs/security.md`](./docs/security.md) | Identity and authorization: the two surfaces, the catalog filtered by sales channel, the scope dictionary, an end-to-end curl walkthrough, the hardening rings and the one-instance / several-instances distinction |
| [`docs/commerce-flows.md`](./docs/commerce-flows.md) | From cart to order: who owns a flow's HTTP surface, who decides the price and the currency, which warehouse it ships from, and where the B2B spending limit is checked |
| [`docs/api-surfaces.md`](./docs/api-surfaces.md) | The generated OpenAPI document and the GraphQL storefront read surface; the limits the server sets when the client decides the cost, and the error policy |
| [`docs/extending.md`](./docs/extending.md) | Plugins, the file upload provider and the domain events — how a new capability is added |
| [`docs/operating.md`](./docs/operating.md) | Running and developing: `/health` and `/ready`, the whole of the configuration, the event bus backends, observability, the make targets, changing the module path and the version history |
| [`docs/known-limits.md`](./docs/known-limits.md) | The known limits: twenty-seven items in six groups — identity and authorization, sales channel scope, the category tree, tax, installation and operation, the limit of the invariants |
| [`docs/measurements/catalog-search-cost.md`](./docs/measurements/catalog-search-cost.md) | The measured cost of catalog search |
| [`CHANGELOG.md`](./CHANGELOG.md) | What changed, release by release |

## Phase status and version

**All ten phases** of the roadmap are complete: the project skeleton (0), the
core infrastructure (1), Module Links and Query (2), the saga engine (3), the
catalog (4), the cart (5), payment and order completion (6), fulfillment,
promotion and tax (7), auth, admin user, API key and RBAC (8), the plugin
system, observability and hardening (9), the GraphQL storefront surface and B2B
(10). What was found after the roadmap ended is tracked in the releases.

The current version is **v0.8.0**. Throughout `0.x`, **breaking changes may
arrive in minor versions**; the surface freezes with `1.0.0`. What changed
release by release is in [`CHANGELOG.md`](./CHANGELOG.md); what each release
brought and why is in [`docs/operating.md`](./docs/operating.md).
