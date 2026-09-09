# Running and developing against gobit

This document covers what an installation needs after it starts: the two health
endpoints and what their answers commit to, where the settings live and why the
document describing them cannot drift from the code, which event bus backend
loses events and which does not, what the framework tells you about itself while
it runs (observability), the loop you develop in (the make targets, what each
layer of tests actually proves, what CI does and does not run), how to move the
module path into your own repository, and what a version number means here.

It is for the operator who has to see inside a running installation and for the
developer working on gobit itself or on a project that embeds it. It assumes you
have already been through the README's quick start (`make up`, `make run`) — the
entry point, and the map of everything else, is [`README.md`](../README.md).

## Health and readiness

`/health` reports only that the process is alive (liveness) and does not test the
dependencies — a transient database outage must not lead to the process being
killed.

`/ready` does test the dependencies, but it does not give them ALL THE SAME VOTE,
because readiness is a routing decision: the question is "can this instance serve
a request", not "is everything fine".

| `status` in the body | code | meaning |
|---|---|---|
| `ok` | 200 | every probe passed |
| `degraded` | 200 | a DEGRADING dependency is down; **this instance keeps serving** |
| `unavailable` | 503 | a dependency that CUTS traffic is down; the instance must be taken out of the load balancer |

Postgres cuts: without it no endpoint answers correctly. Redis DEGRADES, and that
is a measured decision — while Redis is unreachable a catalog read returns 200, a
write carrying no `Idempotency-Key` returns 200, and a write that carries one
returns a 503 that is retryable per request. Putting Redis on the cutting side
would take EVERY replica out of traffic at the same instant (they all share the
same Redis), so it would carry away the requests that return 200 along with the
rest: a partial degradation would be turned into a full outage. The same decision
is taken one layer down, for the protection rings, in
[ADR 0007](adr/0007-sertlestirme-arizada-davranis.md).

The budget for the degrading probes is `READINESS_DEGRADED_TIMEOUT` (250 ms by
default) and it has to be short: a single `PING` thrown at an unreachable Redis
takes 1.7 seconds (the client retries five times) and kubelet's default probe
timeout is 1 second — a probe with no budget would bring back, by failing the
probe itself, exactly the outage it exists to prevent. Every failing degrading
probe also logs at WARN: because the code stays 200 it produces no event in the
orchestrator, which makes that line the ONLY alarm channel for the degradation.

The `version` field is embedded at build time from the output of
`git describe --tags --always --dirty`, so the value in your own working tree
will differ (a dirty working tree gets `-dirty` appended). `dev` is only the
answer of a binary built without ldflags — a plain `go run ./cmd/server`, for
instance; `make run` and `make build` always embed the version.

## Configuration

Every setting is read from an environment variable (12-factor). The defaults
agree with `deploy/docker-compose.yml`, so no `.env` file is needed locally.

`cp .env.example .env` to customise. For the list of variables see `.env.example`
or `internal/core/config/config.go`.

The two CANNOT drift apart: a test walks `Config` by reflection and verifies that
every field carrying an `env` tag is written in `.env.example` and that the value
there is the same as the `envDefault`. The reverse direction is audited too — a
variable that stands in the document but is read by neither the application, nor
compose, nor a plugin cannot be left behind
(`internal/arch/configuration_test.go`). The one deliberate divergence is
`LOG_FORMAT`: the code's default is `json` and this file's is `text`; the
justification is written in the test, and if the divergence disappears the test
asks for the RECORD to be deleted.

A setting whose documentation has gone stale is as harmful as a wrong default,
and quieter: both end with the operator not setting the thing they believe they
set, but no test fails and no log line drops — the difference is made only when
the limit is crossed, that is, in production.

> **Production guard:** the `DATABASE_URL` and `REDIS_URL` defaults are for local
> development only. When `APP_ENV=production` and those two have not been
> overridden the application **fails at startup and stops** — which prevents a
> missing secret injection from silently reaching production with a hard-coded
> credential.

> **`.env` format:** the file is loaded with POSIX shell semantics. Put values
> containing `$` in **single quotes** (`REDIS_URL='redis://:pa$word@…'`), or the
> shell will expand them.

> **Precedence:** a variable given on the command line **overrides** `.env`
> (`PLUGINS=search-pg make run`), not the other way round — the same direction as
> docker compose's rule. Every example written as `VARIABLE=… make run` therefore
> does what it says even if you have created a `.env`. The reverse precedence was
> a **silent** class of fault: the empty `PLUGINS=` line in `.env.example` was
> swallowing the plugin name given on the command line, the application came up
> without an error, and the plugin's endpoints returned nothing but **404**.

## The event bus backends

The event bus backend is chosen with `EVENT_BUS=inmemory|redis`. When `redis` is
chosen and Redis is unreachable, the application stops at startup.

`inmemory` is **not durable**: delivery is asynchronous, and if the process
crashes or shutdown does not finish within `SHUTDOWN_TIMEOUT` the undelivered
events are lost without a trace — an order has been placed and the confirmation
notification never went out. In a shared environment (`APP_ENV != development`)
that risk produces a **warning** at startup; startup does not stop, because in a
single-instance staging setup `inmemory` is still legitimate. The trade is the
same as `GUARD_BACKEND=memory`'s.

When `redis` is chosen, `REDIS_KEY_PREFIX` decides the namespace of the events:
the stream key is `<prefix>:events:<event name>` and the consumer group is
`<prefix>`. **Separating BOTH is essential.** Had only the stream been separated,
two installations would attach to the same group and, by the definition of a
consumer group, only ONE of them would receive a given event — production's
`order.placed` event could be consumed and swallowed by staging.

`EVENT_BUS_CONSUMER` works in the opposite direction: it separates not the
installations but the **processes within the same group**. Left empty,
`<hostname>-<pid>` is used. Giving the same name to two instances leads to double
processing (both read that name's pending list at startup, so each also picks up
the messages the other is still working on) and validation cannot see it — a
single process does not know about the other. That is why the resolved name is
logged at startup; the collision becomes visible only when two startup logs are
put side by side.

## Observability

~~If the address of the OTLP collector (`OTEL_EXPORTER_OTLP_ENDPOINT`) is not
given, observability shuts down **completely** and no outbound connection is
attempted.~~ **Corrected on 2026-09-08 (ADR 0046):** the two signals now have
two switches. `OTEL_EXPORTER_OTLP_ENDPOINT` decides the TRACES alone, and each
provider is built only for the signal that asked for it. With NEITHER that
address nor `METRICS_ADDR` given, observability is off completely and no
outbound connection is attempted — which is still the stock installation,
because both are empty in `.env.example`. The collector being unreachable does
not bring the application down.

**Metrics leave by SCRAPE, not by push.** `METRICS_ADDR` (empty by default;
`METRICS_ADDR=127.0.0.1:9464` is a reasonable local value) opens a listener of
its own, separate from the API server the way `PROFILING_ADDR` is, and it
answers `/metrics` and nothing else, in the Prometheus text format. There is no
export interval and no collector in the path: `curl 127.0.0.1:9464/metrics` is
the whole distance between the running process and
`http_server_active_requests` and `http_server_request_duration_seconds`. The
`_seconds` suffix is the exporter reading the unit the instrument declares, and
it is the part a dashboard written against the OTLP metric names gets wrong.

Unlike the profiling listener this one MAY be bound off loopback, because a
scrape comes from another host by definition. It is unauthenticated, and what it
discloses is the route inventory and the traffic over it — so the address is the
whole guard, and it must not be published by the ingress.

A span is opened for every request; the span name is not the raw path but the
**route pattern**
(`GET /store/v1/sales-channels/{sales_channel_id}/products/{id}`) — had the raw
path been used, every product id **and every sales channel id** would produce a
separate metric series and cardinality would explode. The raw path still sits on the span in the `url.path` attribute, so no
detail is lost; cardinality is limited only in the metrics.

A request rejected in the guard middleware never reaches route matching and its
`http.route` value becomes `unknown`; which endpoint was addressed is read from
that span's `url.path` attribute. (The guards are the stack the composition root
installs in front of the API surfaces — rate limit, then identity, then
idempotency, in `corehttp.APIGuards`; [`security.md`](security.md) — "The order
of the guard stack is deliberate" — and the `corehttp.APIGuards` godoc are where
that order and its justification are written.)

To try it locally:

```bash
make up-tracing        # Postgres + Redis + Jaeger
OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317 OTEL_EXPORTER_OTLP_INSECURE=true make run
# UI: http://localhost:16686
```

> ~~Jaeger accepts **traces only**; the application tries to send metrics to the
> same endpoint as well and a `failed to upload metrics` line drops at every
> interval. It is harmless (an observability failure does not bring the
> application down) but an installation that wants to collect metrics too must
> put an OpenTelemetry Collector in between.~~ **Corrected on 2026-09-08 (ADR
> 0046):** Jaeger still accepts traces only, but nothing offers it metrics any
> more. The push exporter retired with that record, so that line can no longer
> drop and no Collector has to stand in between to stop it. Metrics come from
> `METRICS_ADDR` instead, on a listener that has nothing to do with this
> address.

## Development

```bash
make test              # unit tests (race + coverage)
make test-integration  # integration + end-to-end tests against a real Postgres
make smoke             # starts the real binary, exercises process behaviour
make load-test         # baseline load test (tuned with REQUESTS=… CONCURRENCY=…)
make fuzz              # fuzz targets, one at a time (tuned with FUZZTIME=…)
make lint              # golangci-lint
make vuln              # govulncheck over the root and both example modules
make fmt               # gofmt -s + go mod tidy
make build             # build the distributable binary as bin/gobit
make psql              # connect to the running Postgres with psql
make logs              # follow the logs of the compose services
make down              # stop the infrastructure
```

**Smoke tests** (`internal/smoke`) go one step further: they compile the server
binary and run it **as a process**. The end-to-end tests drive the router with
`httptest`, which means they SKIP `main.go`'s wiring, the migrations at start-up,
config loading and signal handling. In this repository, the four faults found by
running the application by hand while the tests were green were hiding in exactly
that place; the smoke tests close that class permanently (the concurrent start-up
race, misconfigurations halting at start-up, OTLP address formats, SIGTERM
behaviour).

The end-to-end tests (`internal/e2e`) set the modules up with **the production
wiring**: the same container names, the same module order and the same guard
stack (`corehttp.APIGuards`). The protection a test proves is the very one that
runs in production; had the test carried its own copy, the order in production
could change and the test would go on verifying the old order and stay green.

~~CI (`.github/workflows/ci.yml`) runs `gofmt`, the `go mod tidy` diff,
`golangci-lint`, `go vet` and the race-enabled tests on every push and PR.~~
**Corrected on 2026-09-06:** CI (`.github/workflows/ci.yml`) runs `gofmt`, the
`go mod tidy` diff, `golangci-lint`, `go vet` and the race-enabled tests on every
push to `main` and on **every** PR. The push trigger is limited to `main`
(`on.push.branches: [main]`), which means a push to a branch with no open PR
starts no workflow at all; that branch's signal arrives when the PR is opened.
This is the only workflow file in the repository (`ls .github/workflows/`), so
there is no second trigger closing the gap.

## Changing the module path

The default module path is `github.com/bdrtr/gobit`. To move it to your own
repository:

```bash
make rename-module MODULE=github.com/kullanici/repo
```

## Version

Current version: **v0.8.0**. For the changes, see
[`CHANGELOG.md`](../CHANGELOG.md). Which roadmap phase covers what is in the
README's phase-status table.

- **v0.1.0** — all of Phases 0–9.
- **v0.2.0** — what was found after the roadmap ended: sales-channel catalog
  filtering, multiple warehouses, domain events and the first real plugin
  (search).
- **v0.3.0** — the API describes itself: 196 endpoints defined in the schema
  together with their bodies, and a working client can be generated from the
  schema.
- **v0.4.0** — Section 10 (the GraphQL storefront surface, the B2B spending
  limit) and the architectural invariants that structurally close three fault
  classes. Actually running the application revealed that the path turning a cart
  into an order was WIRED UP IN NO INSTALLATION; pulling on that thread showed
  that the authority over price and currency lay with the client. **There are
  breaking changes in the store API.**
- **v0.5.0** — warehouse selection gained a POLICY (scope is a constraint,
  preference is an order; [ADR 0010](adr/0010-depo-secim-politikasi.md)) and
  opening a cart was bound to a workflow: the server now derives the region and
  the currency from `country_code`. Two trust boundaries were written down — the
  customer identity is not verified
  ([ADR 0008](adr/0008-musteri-kimligi-guven-siniri.md)), every installation is
  single-tenant ([ADR 0009](adr/0009-cok-kiracililik-kurulum-siniri.md)) — and
  the real hole the same hunt turned up, the sales-channel rule not being
  enforced on the write path, was closed. The documents came under audit too:
  godoc links and markdown references resolve (`TestTheGodocLinksResolve`,
  `TestTheReferencesInTheDocsResolve`), a reference by line number is forbidden
  (`TestTheDocsCarryNoLineNumberReference`) and the money invariant's blind spot
  was closed (`TestMoneyIsAnInteger`). **There are breaking changes in the store
  API.**
- **v0.6.0** — the framework gained a FACE and an EAR. The admin panel arrived as
  a fourth tree ([ADR 0011](adr/0011-yonetim-paneli-dorduncu-agac.md)), first
  reading, then starting to write: a module publishes a primitively typed
  `<module>.admin` surface and the panel resolves it CONDITIONALLY
  ([ADR 0013](adr/0013-panel-write-surface.md)). Writing a price had to be
  lossless, because the module's only writer is destructive and the panel sees
  only a part of the prices. Error reporting became a contract in the core and an
  implementation in a plugin, with the feed going through the existing log path
  ([ADR 0014](adr/0014-error-reporting.md)); what is NEVER to be sent is decided
  by the core, not by the plugin. The repository's working language became
  English and the migration was bound to a ledger that can only shrink
  ([ADR 0012](adr/0012-repository-language-and-solid.md)).
  **Error MESSAGES and error detail keys were translated into English; error
  CODES did not change.**
- **v0.7.0** — the round that faced production: five faults, all of them
  MEASURED. Two hot paths in the storefront were fixed — search was scoring EVERY
  matching document with `ts_rank_cd` (663 ms → 25 ms across 52,000 matches) and
  the price read while building a cart grew with the SQUARE of the line count (a
  100-line cart: 10,300 → 400 queries). Two unbounded resources were bounded: the
  PostgreSQL pool is now a dial (`DB_MAX_CONNS`) and the in-memory idempotency
  store carries a byte budget. `/ready`, meanwhile, stopped counting Redis as a
  GATE; a failover was pulling every replica out of traffic at the same instant,
  which made the protection layer itself the largest source of outage
  ([ADR 0007](adr/0007-sertlestirme-arizada-davranis.md) was extended with that
  section). **There are breaking changes**: `/ready` no longer returns 503 for
  Redis, a cart carries at most 100 lines, and the order of search results
  changed.
- **v0.8.0** — the round of the half-finished saga: v0.7.0 had stopped an
  interrupted payment from being silent, and this round made it VISIBLE and
  REVERSIBLE. `gobit stuck` lists the half-finished executions (read-only;
  [ADR 0016](adr/0016-operator-read-surface-for-half-done-sagas.md)) and the
  compensation of an abandoned saga now runs FROM THE RECORDS
  ([ADR 0017](adr/0017-recovering-abandoned-sagas-from-the-record.md)) — the
  reserved stock is released and the customer can pay for their cart again.
  Recovery deliberately STOPS at the capture step: counting an unrecorded capture
  as "did not run" is a door to double capture.

  Recovery itself gave birth to two faults, and both were found by running the
  path CONCURRENTLY: the engine could return SUCCESS having run no step at all
  (measured: `err=nil`, zero steps) and every caller that found the abandoned
  record was running the compensation chain (four concurrent callers, four
  compensations → one). The second was closed by the engine CLAIMING the record
  before recovering it (`workflow.ClaimingStore`, an optional capability).

  Alongside it: two operator surfaces (`gobit migrate status` / `migrate down`),
  two measured speed-ups (cart line totals in a single statement: lock time
  ~14.2 → ~6.8 ms; the storefront list's counter made optional: 67 → 0.65 ms) and
  four rounds of the language ratchet (the ledger went from 742 to 715 files).
  **There are breaking changes**: the signature of
  `cart/service.Store.SetLineItemTotals`, the English error messages of the
  engine and of pgstore (the CODES did not change), and the `Step` contract now
  saying that `Compensate` can be called concurrently.

Throughout `0.x`, **breaking changes may arrive in minor versions**: the API
surface is not frozen yet. It freezes with `1.0.0`.
