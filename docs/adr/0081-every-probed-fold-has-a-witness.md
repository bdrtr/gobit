# ADR 0081 — Every fold the startup probe measures has a test that fails on the wrong cluster

**Summary:** The suite passed on a cluster that breaks ADR 0015's one hard
requirement; three tests whose subject is the CLUSTER now fail on it.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

ADR 0015's cluster contract has one hard requirement about text: a ctype that
folds more than ASCII. Collation is FREE and says so. gobit checks the
requirement at startup and WARNS rather than refusing, and that stands — an
ASCII catalog works perfectly on a C-locale cluster.

**Measured on 2026-09-09: the three packages that depend on that fold were run
against a `--locale=C` cluster and ALL THREE PASSED.** `internal/modules/product`
(the storefront's `title ILIKE`), `plugins/searchpg` (`to_tsvector`) and
`internal/modules/auth/repository` (the `email = lower(email)` guard) were green
on a cluster where none of those three expressions folds. The warning was the
only thing that changed.

The cause is not carelessness. Every one of those files keeps its fixture in
ASCII **and says why**, correctly: a module test with a non-ASCII fixture would
pass or fail on how the container was initialized rather than on the code it
covers. `core/db/casefold.go` predicted the consequence in its own godoc — "the
failure is invisible to a test suite as well, because test fixtures are usually
ASCII" — and nobody had measured it.

A second thing was measured and changed nothing: the suite's containers pass no
initdb arguments and get `en_US.utf8` from the image, while
`deploy/docker-compose.yml` pins `C.UTF-8`. Both fold all three paths; the names
differ in the COLLATION column, which the contract frees on purpose.

## Decision

**Each path the startup probe measures gets one test whose declared subject is
the CLUSTER.** Such a test is ALLOWED to depend on how the container was
created, because that dependence is what it asserts — the very property that
disqualifies a non-ASCII fixture from an ordinary module test.

**`internal/arch/cluster_contract_test.go` reads the probe's own SQL** for the
paths it measures and requires a named witness for each. The population comes
from the probe rather than from a list, so a fourth probed path arrives as a
failure.

**The suite's containers are NOT pinned to production's initdb arguments.** The
contract frees collation and the image default satisfies the ctype requirement;
pinning would assert more than the contract promises, at 36 call sites.

## Consequences

- **A contract-violating cluster now turns the suite red in three places**, each
  with a message that names the cluster rather than the code. Proved by running
  the three tests both ways: green on the image default, red on `--locale=C`.
- **The mutation here is the CLUSTER**, which is the only mutation that could
  prove these tests. Nothing about the Go code changes between the two runs.
- **The three ASCII fixtures are untouched.** Their reasoning was right; this
  adds a fourth kind of test beside them rather than rewriting them.
- **The gate does not check that a witness still FAILS on a bad cluster.**
  Nothing in a build can, without a second deliberately broken cluster per path.
  What stands in for it is this record and each witness file's own reproduction.
- **The startup probe still warns rather than refusing**, unchanged; a warning is
  for the operator and a red test is for whoever changes the code.
- **The letters are `\u` escapes**, as the probe writes them: the pair has to be
  a real non-ASCII case pair and ADR 0012's diacritic lane reads the source.

## Rejected

- **Pinning `POSTGRES_INITDB_ARGS` at every container start.** It would test one
  locale where the contract names a property, at 36 sites, for a guarantee the
  witnesses already give.
- **Making the probe refuse to start.** An ASCII-only shop is entitled to a
  C-locale cluster, which is `casefold.go`'s own argument and is not weakened by
  anything measured here.
- **Turning the existing module fixtures non-ASCII.** That would trade a test of
  the read layer for a test of the container, which is the trade their comments
  correctly refuse.
- **Running the whole suite against a second locale in CI.** A container per
  package per locale, for a signal three tests already carry.
