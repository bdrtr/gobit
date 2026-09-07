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
- **No Prometheus anywhere.** `grep -ci prometheus go.sum` returns 0 and
  `go list -deps ./...` names no Prometheus package across its 652 entries. This
  is a new dependency, not a latent one already paid for.

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
