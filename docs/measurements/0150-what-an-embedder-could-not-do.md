# What an embedder could not do

Evidence for [ADR 0150](../adr/0150-an-embedder-can-bring-gobit-up-in-a-test.md).

Measured 2026-09-12, taking the first slice the feature list's A8.17 row names.

## The surface an outside program had

The facade is five methods and one of them runs anything:

```
$ grep -l 'func (a \*App)' gobit.go
gobit.go        ← Version, Add, Use, Main
```

`Main(args, out)` binds a port and blocks. Everything behind it is internal:

| What a test would need | Where it lives |
|---|---|
| the migrations | `internal/app` (per module, through `module.Module`) |
| the module registry | `internal/app` |
| the router and the guard rings | `internal/app` + `core/http`, assembled in `serve` |
| the admin panel binding | `internal/app` |
| the plugin start and route mount | `internal/app` |

`TestNoPublishedPackageImportsAnInternalOne` makes that unreachable on purpose,
with the facade as the single exemption — so whatever answers this had to be the
facade's.

## What the alternative costs, measured on this repository

`internal/e2e` is the alternative: it builds its own registry and its own router.
The cost is on record — [ADR 0141](../adr/0141-the-end-to-end-ground-wires-what-production-wires.md)
exists because that copy drifted, and the gate it added compares the flow packages
`internal/app` imports with the ones `internal/e2e` imports.

So the shape of the answer was decided before the code: ONE assembly, both callers
through it. What was left to measure was where to cut.

## Where the cut is, and what stayed on each side

`serve` was configuration, observability, `openApplication`, a hundred lines of
assembly, jobs, listeners, server. The extracted middle is everything between the
pool and the server:

| Step | Side |
|---|---|
| `config.Load`, logger, error sink | both callers, separately |
| observability (OTLP, metrics) | serve only — it needs endpoints and a port |
| `openApplication` (pool, core migrations, container, modules) | shared |
| panel bind, authenticator bind, admin seed | **shared (the new function)** |
| plugin start, provider checks, callbacks, plugin routes | **shared** |
| erasure surface, audit reader, schema build and endpoint | **shared** |
| shutdown-vs-saga warning | serve only |
| scheduled jobs | serve only |
| pprof and metrics listeners | serve only |
| HTTP server | serve only |

The three "serve only" tails are the ones that need a port or a clock, which is
exactly what a test must not need.

## Why the configuration is not a parameter

A `Config` handed in would be a second configuration path. `config.Load` parses
the environment AND validates, and the defaults live on the struct tags, so a
harness that built a Config by hand would run under values no deployment has — and
the first divergence would be invisible: a test passing on a default the real
binary does not use.

The price is that the caller sets environment variables, so such a test cannot be
`t.Parallel`. That is a property of a process-wide environment rather than of this
design, and it is stated in the proof's own helper so the next person does not add
`t.Parallel` and watch two installations fight over `DATABASE_URL`.

## The proof is written as an EMBEDDER

`inprocess_integration_test.go` imports `github.com/bdrtr/gobit` and
`core/container` + `core/module` (to add a module) and NOTHING else of the
framework. That constraint is the assertion: a test that reached into `internal/`
to bring the installation up would prove nothing about a claim whose whole
difficulty is that an outside program cannot.

Four things are asked of the assembled handler and each catches a different
failure:

| Request | What it proves |
|---|---|
| the schema carries the channel-scoped product listing | the document is built from the ROUTER, so the modules registered and bound |
| a storefront read with no publishable key answers **401** | the guard rings are attached |
| the health endpoint answers 200 | the endpoints outside the rings are reachable |
| the added module's own path answers its own body | `Add` reached the registry the assembly walks |

The embedder's module binds a prefix of its own rather than one under `/store/v1`,
and that is a measurement rather than a preference: the guard ring answers 401 for
an UNKNOWN path under that prefix exactly as it does for a known one without a key
(the storefront product listing is channel-scoped and lives at
`/store/v1/sales-channels/{sales_channel_id}/products`), so a 401 would not
distinguish a module that is present from one that is absent.

## The mutations

| # | Mutation | Result |
|---|---|---|
| 68 | the harness returns the router WITHOUT the assembly | **bit** |
| 69 | the facade drops the caller's modules and plugins | **bit** |
| 70 | the names gate walks `core/` only again | **bit** |

Mutation 68 is the one this whole slice is about. The router alone still serves the
schema and the module routes — the modules bind during Bootstrap, inside
`openApplication` — so the harness looks perfectly healthy. What it stops doing is
REFUSING: the storefront answers 200 with no publishable key. Every authorization
test an embedder wrote against such a harness would pass for the wrong reason,
which is why the proof asks for a 401 and not a 200.

Mutation 70 is the gate D86 closed. Before this slice it was not a mutation at all
but the live state of the tree: the facade's seven exported names were outside the
inventory, and `App.InProcess` was added and audited by nothing.

## What is NOT closed

The e2e harness still builds its own registry and router. Moving it onto
`InProcess` is the obvious next step and it is a separate decision: that harness
reaches into module services directly (it holds `productSvc`, `cartSvc` and a
dozen more), and `InProcess` deliberately publishes no container.

There is no fixture builder, no test database helper and no HTTP client. The row
that asked for this named those too; each is a second way to create a record, and
the first one that drifts from the service it imitates teaches an embedder a shape
gobit does not have.

## What was not measured

Whether an embedder exists today who tried and gave up. The repository has two
out-of-tree examples under `examples/` and neither has a test that boots gobit,
which is consistent with "it was not possible" and proves nothing on its own.
