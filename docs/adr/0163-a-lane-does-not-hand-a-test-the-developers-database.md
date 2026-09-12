# ADR 0163 — A lane does not hand a test the developer's database

**Summary:** The three lanes that run tests set DATABASE_URL and REDIS_URL to an
address where nothing listens, so a test that does not start its own
installation fails on the machine that wrote it rather than on the runner.

- **Status:** Accepted
- **Date:** 2026-09-12

Measurement: [measurements/0163](../measurements/0163-what-the-lanes-were-handing-tests.md)

## Context

An integration test called `config.Load()` without setting `DATABASE_URL` and
reached whatever answered on localhost:5432 — the development database of the
machine it was written on. Nine lanes passed locally and the runner turned red
on the next push (D107).

The rule it broke had already been decided, for one lane and in prose.
`internal/smoke` builds every server process's environment FROM SCRATCH, and the
helper's godoc gives the reason and names the variable: a setting sitting in the
developer's shell would silently pass a scenario written for its absence, and
"the same holds for DATABASE_URL". The lanes themselves inherit the shell they
are started from, and this machine has Postgres and Redis listening on exactly
the addresses the configuration defaults to.

## Decision

`make test`, `make test-integration` and `make smoke` run with `DATABASE_URL`
and `REDIS_URL` pointing at 127.0.0.1:1. A test that needs a database starts one
and hands its DSN to the environment, which every scenario in the tree already
does.

## Consequences

A test whose fixture is the machine fails a lane instead of a push, with the
runner's own message. Measured by putting the defect back: the repaired test,
stripped of the installation it now starts, fails under the lane's environment
with `db_unreachable ... 127.0.0.1:1`, which is what the runner said.

The addresses PARSE, so `config.Load()` still succeeds and only connecting
fails. A test that loads the configuration for an unrelated reason — another
field's default, a validation rule — is untouched, which a deliberately
malformed value would not have left alone.

Nothing in the tree relied on the ambient services. The whole integration lane
and the whole smoke lane were run against dead addresses before the change and
both were green, so this costs no scenario and closes the door behind D107.

The binding is to the make target rather than to the test binary. A developer who
runs `go test -tags=integration ./...` by hand still gets the ambient
environment; the lane is the target, and that is what the verification section
names.

CI is left alone. Its runner has no such service — the condition being
reproduced — and a variable set in the workflow would be a second copy of this
decision, free to drift from the target the developer runs.

## Rejected

**Dropping the `envDefault` from the configuration.** It is why `go run
./cmd/server` works on a fresh checkout. The defect was not that a default
exists but that a lane let a test reach it.

**A source gate refusing a test that calls `config.Load()` with no `t.Setenv`
beside it.** The population is right and the unit is wrong: a helper
legitimately lives in another file — `migrateDSN` does — so the check would be a
proxy for where the text sits rather than for what the test reaches.

**Sharing smoke's `env` helper.** It builds the environment of a process the
test STARTS; what is needed here is the environment of the test binary itself,
and they hold different things for the same reason.
