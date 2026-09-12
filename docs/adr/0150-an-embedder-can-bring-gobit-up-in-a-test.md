# ADR 0150 — An embedder can bring gobit up in a test

**Summary:** The facade gains `InProcess`, which assembles the whole installation
and returns its HTTP handler instead of listening. It costs one exported method
and the discipline that the assembly lives in one function, and it buys an
embedder the ability to test its own module against a real gobit.

- **Status:** Accepted
- **Date:** 2026-09-12

## Context

gobit is a library (ADR 0025) and a program that embeds it had no way to test
against it. The facade offers `Main(args, out)`, which binds a port and blocks,
and everything behind it — the migrations, the module registry, the router, the
guard rings — is under `internal/`, unreachable from outside.

So an embedder's options were to run the binary and talk to it over a socket, or
to reimplement the assembly in its own test. This repository knows what the second
costs: `internal/e2e` builds its own router, and the gate that exists because that
copy drifted is [ADR 0141](0141-the-end-to-end-ground-wires-what-production-wires.md).

Measurement: [measurements/0150](../measurements/0150-what-an-embedder-could-not-do.md)

## Decision

`App.InProcess(ctx)` returns the handler `Main` would serve, assembled by the
same function, plus a shutdown. The assembly between the database pool and the
HTTP server is now one function that both callers go through, and neither starts
the scheduled jobs or the operator listeners.

## Consequences

There is ONE assembly, which is the point: a harness with an assembly of its own
would be a second answer to "what is an installation", and the one the tests
trusted would be the one nobody deploys. The extraction moved a hundred lines out
of `serve` unchanged; `serve` now reads as configuration, assembly, jobs,
listeners, server.

Nothing with a PORT or a CLOCK starts. A test must not have to reserve a port, and
a relay ticking under a test's own assertions makes a failure depend on when the
test looked — while the work those jobs do, publishing what the outbox promised
and sweeping a stuck saga, is usually what such a test asserts about. The cost is
stated where a caller reads it: an event reaches a subscriber through the direct
publish only, and the outbox row waits for somebody to relay it.

The configuration is read from the ENVIRONMENT, as `Main` reads it. A second
configuration path would mean a second set of defaults and a test running under
values no deployment has; what it costs is that the caller sets environment
variables, so such a test cannot be `t.Parallel`. The database is the caller's: it
is not created, not dropped and not cleaned, because a helper that dropped a
schema on cleanup is one nobody can point at a real database.

Widening the facade also found a hole in the gate that audits the published surface
(D86): the names inventory walked `core/` while the package list has always
declared the facade published too, so `gobit.App` and its methods were promises
nothing audited — `InProcess` arrived and the gate stayed green. The inventory
reads the same package list now, one directory per entry: a first version that
recursed descended into `contrib/` and `examples/`, separate modules whose names
this one does not promise.

Three mutations bit, and the one worth naming is the harness handing back the
router WITHOUT the assembly: the schema endpoint still answers, the module routes
still answer, and the storefront stops refusing a request with no publishable key.
Every authorization test an embedder wrote against that harness would pass for the
wrong reason, which is why the proof asks for a 401 rather than a 200.

## Rejected

- **A `core/gobittest` package.** A published package may not import `internal/`
  (`TestNoPublishedPackageImportsAnInternalOne`) and the assembly is internal by
  the decision that rule protects. The facade is the one package exempt, because
  naming what it assembles is its job.
- **A fixture builder — customers, products, a cart.** Every such helper is a
  second way to create a record, and the first one that drifts from the service it
  imitates teaches an embedder a shape gobit does not have.
- **Start the jobs behind a flag.** A flag that changes what a harness runs makes
  two harnesses; an embedder that wants a job's work calls the job.
- **Return the container so a test can resolve a service.** It would publish the
  container's names as a contract, which ADR 0001 keeps free to change.
