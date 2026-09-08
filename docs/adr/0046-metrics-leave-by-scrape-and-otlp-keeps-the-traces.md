# ADR 0046 — Metrics leave gobit by SCRAPE, and OTLP keeps the traces

- **Status:** Accepted
- **Date:** 2026-09-07
- **Phase:** after the roadmap

## Context

Gap A13 asks the posture question: OTLP-only, or expose a scrape endpoint. The
measurement taken to answer it found that the question's premise is generous —
in a stock installation **no metrics leave this repository in either
direction**, and the two documents that described the situation both said
otherwise.

What exists, verified rather than recalled:

- **Two instruments, in one file.** `core/http/telemetry.go` builds an
  `Int64UpDownCounter` for requests in flight and a `Float64Histogram` for
  request duration, both from `otel.Meter` under the scope name
  `github.com/bdrtr/gobit/core/http`. Grepping the whole tree for a meter or an
  instrument constructor returns those three sites and nothing else. There is no
  third instrument anywhere.
- **No meter provider in the default install.**
  `internal/core/observability.Setup` returns before it builds EITHER provider
  when `Options.Endpoint` is empty; `otel.SetMeterProvider` is reached only after
  both are built. The endpoint comes from `OTEL_EXPORTER_OTLP_ENDPOINT`, which
  carries no default and stands blank in `.env.example`. So both instruments
  record into OTel's no-ops, and the metric half of the pipeline is switched by
  an address that names NEITHER signal: `OTEL_EXPORTER_OTLP_ENDPOINT`,
  `config.Config.OTLPEndpoint` and `observability.Options.Endpoint` all name a
  transport, and the code is explicit that one blank value silences both —
  `Options.Endpoint`'s godoc says telemetry is OFF when it is empty, and `Setup`
  logs `no OTLP address was given, telemetry is off`. What named tracing alone
  was the PROSE of the `config.Config.OTLPEndpoint` godoc, and that sentence has
  since been corrected in the tree.
- **The only collector this repository ships REFUSES metrics.**
  `deploy/docker-compose.yml` says so in its own comment about the Jaeger
  profile: it accepts traces only, the application sends metrics to the same
  endpoint anyway, and at every export interval the line
  `failed to upload metrics: ... unknown service ...MetricsService` drops. With
  `METRIC_EXPORT_INTERVAL` defaulting to 60s, that is once a minute, forever.
- ~~**No Prometheus anywhere.**~~ **BUILT 2026-09-08.** It was true when it was
  measured — `grep -ci prometheus go.sum` returned 0 and `go list -deps ./...`
  named no Prometheus package across its 652 entries — and it is precisely the
  sentence this record set out to stop being true. The exporter and the modules
  behind it are in `go.mod` now; what that actually cost, counted rather than
  estimated, is at the end of this record.

So today a metric has exactly two states available to it: absent, or failing
every sixty seconds. **Neither of those is a posture**, and that is what A13 is
really being asked to fix.

Two documents had to be corrected before the question could even be read
straight, and both corrections are in the tree: the godoc on
`config.Config.OTLPEndpoint` said an empty value turned TRACING off and never
mentioned that it turns metrics off too, and `docs/gaps.md` stated flatly that
there is a meter provider, an exporter and an export interval, with no
condition. This is the repository's standing defect — prose outliving the code
it describes — and it is worth noticing that it was found by asking a posture
question, not by a test.

One thing was checked before it could be assumed into an obstacle. **Nothing
pins the current behavior.** No test anywhere asserts that an empty endpoint
leaves the global meter provider unset; the six tests in the observability
package cover the sampler, the per-provider shutdown budget and the two accepted
spellings of the endpoint, and the only test in the tree that installs a meter
provider is `core/http/telemetry_test.go`, which hands the two instruments an
in-memory reader and checks that they record. That the instruments work under a
reader that is not OTLP is therefore already proved, by a test that has been
green all along.

## Decision

**gobit exposes its own metrics for scraping, and OTLP is left carrying the
traces.**

1. **The meter provider is built with a PULL reader** —
   `go.opentelemetry.io/otel/exporters/prometheus` — and its output is served as
   `/metrics`.
2. **It gets its own listener, gated by its own address**, `METRICS_ADDR`,
   empty by default. This is the shape `internal/app/profiling.go` already uses:
   no address, no listener, no cost. The handler is built in
   `internal/core/observability`, where the provider is, and `internal/app`
   serves it on a `core/http.NewServer` exactly the way `startProfiling` serves
   `core/http.ProfilingHandler`.
3. **The periodic OTLP metric reader retires.** One signal, one transport.
   Traces keep going over OTLP and nothing about them changes.

`internal/core/observability.Setup`'s early return does not disappear — its
condition WIDENS, from one input to two: it returns before building anything
when neither signal is configured, and each provider is built only for the
signal that asked for it. The package's own promise that when it is off it is
really off stays literally true, and its stated reason survives whole: a pull
reader opens no outbound connection, so a development environment still gets no
stream of connection failures.

**The output was measured, not assumed.** A scratch program built the same
provider shape `newMeterProvider` builds but with a pull reader, created the
exact two instruments through the global meter under the same scope name, and
served the result. It answered HTTP 200 with `http_server_active_requests` as a
gauge and `http_server_request_duration_seconds` as a histogram — sixteen bucket
series over the SDK's fifteen default boundaries, plus `_sum` and `_count`. The
`_seconds` suffix is the exporter reading the unit the instrument declares, and
it is worth knowing before somebody writes a dashboard against the un-suffixed
OTLP name.

Two smaller things fell the same way and both were checked. The exporter at
v0.67.0 resolves against the otel v1.45.0 and sdk/metric v1.45.0 already in
`go.mod` with **no version bump**. And the boot order already works:
observability is set up before the application is opened, and the router — with
`core/http.Telemetry` in it — is built inside that, so the provider is installed
before the instruments are created. Nothing has to be resequenced, which in this
shape is a real hazard that happens not to bite.

## Rejected alternatives

**OTLP-only — the other half of the question A13 asks.** It is the cheaper
answer and it has one argument that the chosen one cannot match: a push needs
only EGRESS. A shop behind NAT, or in a network where nothing may open an
inbound path to the application, can export and cannot be scraped. What kills it
is what it costs an operator to see a single number: they must run a Collector.
This repository ships Jaeger, Jaeger takes traces only, the compose file already
records the failure line it produces and already tells the reader the remedy is a
Collector plus one more configuration file. A posture decides where that burden
sits, and OTLP-only puts a piece of infrastructure between a running shop and the
question "how many requests are in flight right now".

**Keep both readers on one provider.** The SDK allows it, and it is the
alternative that breaks nobody — an installation exporting metrics to a real
Collector today would keep doing so. It is rejected because it does not ANSWER
A13: it declares two postures and lets the configuration pick. It also buys two
aggregation stores over the same two instruments and two collection instants, so
the first time the two disagree the disagreement is discovered during an
incident, by someone who has to decide which one is lying. And the compatibility
it protects is theoretical here: with no meter provider in the stock install and
the shipped collector rejecting metrics, no consumer of the OTLP metric path is
known to exist.

**Mount `/metrics` on the application's router.** One line, and both of the
things that kill it are specific. First, the route would instrument ITSELF:
`core/http.NewRouter` puts `core/http.Telemetry` second in the middleware stack
and registers `/health` and `/ready` beneath it, so a `/metrics` route added
there would take the same path — every scrape opening a span, moving the
in-flight counter and recording a duration into a series named after the scrape
endpoint. Second, it would sit on the listener the shop is served from, at
whatever address the ingress publishes, with no authentication. When this
repository last had an unauthenticated diagnostic endpoint it reached for a
separate listener, and the reasoning written into `core/http/profiling.go` for
why applies here unchanged.

**Copy pprof's rule whole and refuse a non-loopback bind outside development.**
It would make the new listener as safe by construction as the profiling one, and
it cannot be taken, because a scrape comes from ANOTHER HOST by definition. A
loopback-only metrics endpoint is readable by a sidecar or a port forward and by
nothing else — decorative in exactly the deployment it exists for. So the gate is
borrowed from pprof and the loopback refusal is not, and the difference is
justified by content rather than by taste: a heap profile carries live memory,
while the two instruments carry the service name, the HTTP method, the chi ROUTE
PATTERN and the status code. No identifier, no body, no path segment a customer
typed.

**Put the handler in `core/http` beside `ProfilingHandler`.** Symmetrical and
wrong. It would drag the Prometheus client library onto the published surface,
where a dependency is a promise kept until the next major version (ADR 0026).
The provider lives in `internal/core/observability` and the handler can come back
from there, so the new modules stay inside gobit and the published packages gain
nothing.

## Consequences

**Positive**

- **A running installation can answer for itself.** One HTTP request to the
  metrics address returns the current value of both instruments, with no
  collector, no export interval and no second process in the path. That is the
  difference between an observable shop and a shop that would be observable if
  someone deployed something.
- **The two signals get two switches.** Today one address decides both, which is
  how the metric half went missing quietly enough for two documents to describe
  it wrongly. After this, an address that mentions metrics turns metrics on.
- **A collector outage stops being a hole in the record.** Under the periodic
  reader an interval whose export fails is simply gone — that is what the Jaeger
  line means, once a minute. A pull reader holds the current value until someone
  asks for it.
- **The exposed output is known before it is built.** The metric names, the
  types and the `_seconds` suffix came from running it, not from reading the
  exporter's documentation.

**Negative, and accepted**

- **Eight new modules.** `go.opentelemetry.io/otel/exporters/prometheus` itself,
  which nothing in this repository requires today, and the seven it drags with
  it: `prometheus/client_golang`, `client_model`, `common`, `otlptranslator`,
  `procfs`, `beorn7/perks` and `munnerz/goautoneg`. Everything else the exporter
  needs is already present. Counted, not estimated: `go list -deps` over the
  scratch program and over gobit, reduced to module paths and compared, leaves
  exactly those eight. gobit is a library (ADR 0025), so each of those becomes
  the embedding project's dependency too — and **nothing in this repository
  audits third-party requires.** `internal/arch/depguard_test.go`,
  despite its name, checks the module-isolation matrix and nothing else, so this
  addition trips no gate. It is being taken on judgment, with no automated second
  opinion.
- **An installation that WAS collecting metrics over OTLP loses that path.**
  This is a removal, and calling it anything else would be dishonest. The
  direction reverses: such an installation points its Collector's scrape at the
  metrics address instead of receiving a push. Whether any installation is in
  that position is not established — it cannot be, from inside the repository.
- **`METRIC_EXPORT_INTERVAL` stops meaning anything** and has to leave
  `config.Config` and `.env.example` in the same change. Doing half of it fails
  the build in one of two ways, which is the useful part:
  `TestTheEnvExampleAgreesWithTheConfigDefaults` if the field stays with no line,
  `TestNoVariableInTheEnvExampleIsOrphaned` if the line stays with no field.
- **The new listener is unauthenticated and MAY be bound off loopback**, which is
  the one place this decision is weaker than the pprof precedent it copies. What
  it discloses is the route inventory and the traffic over it — counts, latencies
  and error rates per route pattern. Counted in non-test files under
  `internal/modules`, `core`, `plugins` and `internal/app`, there are 184
  registrations of the shape a route registration takes, so the endpoint is also
  a nearly complete map of the API. It must not be published by the ingress, and
  **no code can enforce that**: the address the operator chooses is the whole
  guard.
- **Aggregation memory becomes a real cost whenever the address is set.** The
  histogram's series are route pattern times method times status code, and the
  process holds them for its lifetime. This is the price of having metrics at all
  rather than a price of scraping them — but it arrives with this decision,
  because today it is never paid.
- **The observability package's godoc has to be edited rather than preserved.**
  Its argument survives; the sentence naming one address as the switch does not.
  A record that left that sentence standing would be adding to the exact defect
  this one was written on top of.

## What this deliberately does NOT do

- **It does not add an instrument.** Two exist, two get exposed. Orders per
  minute, cart conversions and job queue depth are not metrics this repository
  records, and deciding to record them is a different decision with different
  costs — chiefly cardinality, which none of the current attributes carry.
- **It does not fix the histogram's buckets, and they are wrong.** Measured in
  the same run: the SDK's default boundaries are 0, 5, 10, 25, 50, 75, 100, 250,
  500, 750, 1000, 2500, 5000, 7500 and 10000, while the instrument declares
  SECONDS — so a twelve-millisecond request and a four-second request land in the
  same bucket and effectively every real request falls in the first non-empty
  one. The defect is in the instrument and is identical under either transport;
  what scraping changes is that it becomes visible in the first dashboard anybody
  builds. The fix is a change to `core/http/telemetry.go` and it belongs in its
  own record.
- **It does not decide whether the Go runtime collectors join the output.** A
  registry of its own yields exactly the two instruments, which is what was
  measured; the client library's default registry would add process and runtime
  series as well. Both are defensible and this record picks neither.
- **It does not authenticate the endpoint, and does not add a validation rule
  refusing an address.** The rule pprof carries rests on what a heap profile
  contains, and that reason does not transfer to two HTTP counters. If a later
  measurement shows the route inventory is worth protecting, that is a rule to
  add deliberately, with its own justification.
- **It does not say where metrics are STORED, aggregated or alerted on.** gobit
  exposes; the embedder scrapes. That boundary is the same one ADR 0025 draws
  everywhere else.


## What was built, 2026-09-08

**Where each piece landed.**

- `internal/core/observability` builds the meter provider from
  `go.opentelemetry.io/otel/exporters/prometheus`, which IS the pull reader —
  the exporter embeds a `ManualReader` — and `newMeterProvider` hands back the
  provider and the handler together, because one is useless without the other.
  `Setup` therefore returns a `Telemetry` value rather than a bare shutdown
  function: the two things in it are answered by the same call and read at
  opposite ends of the process's life, and a pair of loose returns would let a
  caller keep one and drop the other with nothing to notice.
- The early return widened exactly as decided: `Setup` returns before building
  anything only when NEITHER signal was asked for, and each provider is built
  for its own switch. One thing moved that the record did not mention — the W3C
  propagator is now installed inside the trace branch. With no tracer provider
  there is no trace to continue, so a propagator on its own would only carry a
  header between two no-ops. Under the old condition the two were inseparable,
  so there was nothing to decide.
- `internal/app` gained `startMetrics`, which is `startProfiling` with the
  address changed and one budget restored: the profiling server leaves
  `WriteTimeout` at zero because a profile takes as long as it was asked to
  take, while a scrape is a bounded response gathered from an in-memory store,
  so the ordinary write budget applies and a scrape that outruns it is a fault
  worth cutting off.
- `internal/core/config` gained `METRICS_ADDR` and lost `METRIC_EXPORT_INTERVAL`
  together with its validation rule, its line in `.env.example` and the entry in
  the environment list the config tests clear. The godoc on `OTLPEndpoint`
  already carried the correction that started this record; it now has a
  neighbor that says which signal it does NOT decide.

**The switch is a boolean, and the record did not say so.** `Options.Metrics` is
a `bool`, not the address. The observability package builds a handler and binds
no port, so a field holding an address it never listens on invites exactly the
belief that it does; `internal/app` turns `METRICS_ADDR` into that answer, next
to the listener the address belongs to. The same gate treats a nil handler as
the same answer as an empty address, and the reason is worth stating: binding a
port that answers 404 to every scrape reads to an operator like a broken
deployment rather than like a switch left off.

**One thing the record deliberately left open had to be decided by the code.**
Whether the Go runtime collectors join the output was picked neither way, and a
program cannot decline to pick. The private registry was built — the shape the
record MEASURED, which yields exactly the two instruments — and a second reason
turned up while building it that makes the choice more than a coin toss:
`prometheus.DefaultRegisterer` is a package-level global and registering on it
twice fails with `duplicate metrics collector registration attempted`. A second
`Setup` in one process, which is what a test does, would make telemetry look
broken for a reason that has nothing to do with the installation. Adding the
runtime collectors remains a one-line change and still deserves its own record.

**The dependency count, as it actually landed.** The eight are exactly the eight
this record predicted: `go.opentelemetry.io/otel/exporters/prometheus`,
`prometheus/client_golang`, `client_model`, `common`, `otlptranslator`,
`procfs`, `beorn7/perks` and `munnerz/goautoneg`. Two things the count did not
anticipate, both verified rather than assumed:

- **One module LEAVES.**
  `go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc` was the
  push exporter and nothing imports it any more, so `go mod tidy` dropped it.
  The net addition is eight in and one out, not eight in.
- **One version moves.** `github.com/klauspost/compress` goes from v1.18.6 to
  v1.19.1, because `prometheus/client_golang` v1.24.1 requires it and minimal
  version selection takes the higher one; `go mod graph` names the requirer.
  This is a transitive bump of a module already present, not a ninth module, and
  it does not touch what the record checked: the otel modules all stayed at
  v1.45.0 and the exporter resolved against them with no bump at all.

**ADR 0025's claim arrived as a failing test rather than as a sentence.**
`examples/starter` and `examples/plugin` are separate Go modules, built by
`internal/arch` to prove the published facade is enough to run an installation
from outside. Both stopped compiling the moment the exporter entered the tree,
with `missing go.sum entry`, and both had to be tidied in the same change. That
is what "each of those becomes the embedding project's dependency too" looks
like when it is real — **but the two broke for DIFFERENT reasons, and this
paragraph first attributed both to the exporter (corrected 2026-09-08):**

- `examples/starter` is the module that actually pays the eight. Its `go.mod`
  gains `exporters/prometheus` and six of the Prometheus modules as indirect
  requires, and loses `otlpmetric/otlpmetricgrpc`.
- `examples/plugin` gains NO Prometheus requirement at all — grepping its
  `go.mod` for prometheus returns nothing. What broke it was the transitive bump
  the exporter forced elsewhere: the `klauspost/compress` v1.18.6 to v1.19.1
  `go.sum` entry. Its tidy also swept up drift that predates this change and
  belongs to nothing in it — `caarlos0/env/v11` left its `go.mod`, and
  `golang-migrate`, `lib/pq` and `testcontainers-go/modules/postgres` entered
  its `go.sum`. That churn is carried rather than reverted, because it is what
  `go mod tidy` produces and CI diffs the result of `go mod tidy`.

**What the tests pin, and what mutation proved they can fail.** Five claims,
each with a mutation run under `-count=1`:

- `TestEachSignalIsBuiltOnlyForTheSwitchThatAskedForIt` walks the four
  combinations of the two switches and checks the scrape handler against each.
  Narrowing the early return back to the old one-address condition fails the
  metrics-alone case, which is the exact defect this record was written on top
  of. The metrics cases go through the GLOBAL meter, because that is the only
  provider `core/http` can reach; deleting the `otel.SetMeterProvider` call
  fails them too, with a scrape body carrying `target_info` and nothing else.
- `TestTheScrapeOutputCarriesTheNamesADR0046Measured` pins the names, the two
  types and the `_seconds` suffix that this record measured in a scratch
  program. It is the one claim here a dashboard is written against, and the
  suffix is the part somebody will get wrong.
- `TestTheMetricsListenerNeedsBothAnAddressAndAHandler` proves no port is bound
  without both halves of the gate; dropping the handler half leaves the wait
  function blocked on a listener that should not exist, and the test says so
  rather than hanging.
- `TestTheScrapeIsCutOffByTheWriteBudget` pins the one budget this listener
  restores that `startProfiling` deliberately leaves at zero. Added 2026-09-08,
  because until then deleting `WriteTimeout: cfg.WriteTimeout` from
  `startMetrics` left `go test -count=1 ./internal/app -run Metrics` green: the
  decision existed in this record and in two code comments and nowhere a test
  could reach it. It serves a handler that sleeps ten times the budget and
  requires the request to FAIL; with the budget deleted the request answers 200
  and the test fails, which is the mutation.
- `TestTheOperatorListenersCloseWithoutWaitingForASignal` pins the hang
  described below. Starting the two listeners on the process context instead of
  their own leaves the close function waiting past its ten-second budget and the
  two ports still bound, and the test reports both.

**A hang the profiling listener already had, and this one would have doubled
(fixed 2026-09-08).** `serve` returns on the FIRST error, and the commonest one
— an API port that is already bound — comes back from `core/http.Server.Run`
without canceling anything: `Run` hands the listen failure straight back, and
the process context is canceled by SIGTERM alone. The deferred wait on an
operator listener would then block forever, so the process would neither exit
nor report the error it had already diagnosed. `startProfiling` had that shape
from the start and a metrics listener is far likelier to be enabled, so the fix
is taken for both: `startOperatorListeners`, in `internal/app/listeners.go`,
opens the two together under a child context of their own and CANCELS it before
it waits. Found by reading the defer order, not by a test, and now pinned by
one.

**One test-hygiene fix in the same pass.** The test that scrapes the endpoint,
`TestTheMetricsListenerServesTheScrapeEndpoint`, calls `observability.Setup`
with metrics on, which installs a real global meter provider; it shut that
provider down without uninstalling it — leaving a CLOSED provider in `otel`'s
process-wide global for every test that ran after it in the `internal/app`
binary. The observability package keeps its own tests
clean with a `restoreGlobals` helper; the app side now has the same thing, and
the ordering is stated where it matters, since a cleanup that swaps the no-op in
before the shutdown runs would shut down nothing.

**What was left alone, on purpose.** The histogram's buckets are still wrong and
still belong in their own record. No instrument was added. The endpoint is
unauthenticated and no validation rule refuses an address, as decided. And no
SCRAPE TARGET was added to `deploy/docker-compose.yml`: a Prometheus for the
local stack is an operator surface this record does not cover.

**What was NOT left alone, because it was never a surface — it was a false
statement (2026-09-08).** ~~Nothing was added to `deploy/docker-compose.yml`:
the compose file's Jaeger comment describes the push path that just retired.~~
That sentence excused leaving a failure mode documented as current behavior
after the build had made it impossible, which is this record's own Context
reproduced one page later: prose outliving the code it describes. Three
documents were in that position and all three are corrected where they stand.

- `deploy/docker-compose.yml`'s Jaeger comment promised a
  `failed to upload metrics: ... unknown service ...MetricsService` line at
  every export interval and told a reader who wanted metrics to put a Collector
  in between. Neither can happen now. It says so, and it names `METRICS_ADDR`
  as where metrics come from instead. Corrected cleanly rather than struck: it
  is a statement of behavior in a YAML comment, not an argued claim.
- `docs/operating.md` opened its observability heading with "observability shuts
  down **completely**" when `OTEL_EXPORTER_OTLP_ENDPOINT` is unset, and repeated
  the compose file's promise in its Jaeger note. Both are struck and dated, and
  the two switches, `METRICS_ADDR`, the `/metrics` path and the two instrument
  names an operator would grep for are written where an operator reads them.
- `docs/gaps.md` said flatly that there is no `/metrics` endpoint for Prometheus
  to scrape and that an installation wanting gobit to expose one does not have
  that today, and its LLM entry said the meter provider exports over OTLP rather
  than Prometheus. Struck and dated. A13 carries a BUILT marker, and what the
  build changed about the measurement recorded in that row is spelled out in it.

The reason first given for skipping the two documents — that they are not this
change's files and `PROFILING_ADDR` has no entry in them either — does not hold,
and the distinction is worth keeping: an ABSENT entry is a gap somebody may
choose not to fill, while a statement the build made false is a defect. These
were the second kind.

## Related

- [ADR 0007](0007-sertlestirme-arizada-davranis.md) — telemetry serves the
  product's visibility and not its correctness, which is why a metrics listener
  that cannot open must not close the shop.
- [ADR 0025](0025-gobit-is-a-library-not-a-template.md) — why eight new modules
  are the embedder's cost as much as ours, and why storage stays outside.
- [ADR 0026](0026-the-published-surface-is-fourteen-packages.md) — the promise
  that keeps the exporter out of `core/`.
- [ADR 0027](0027-the-composition-root-is-a-library-not-a-binary.md) — why
  `internal/app` starting a second listener is library behavior rather than a
  binary's private business.
