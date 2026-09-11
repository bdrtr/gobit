# ADR 0138 — The reaper is off where the machine is thrown away

**Summary:** The integration and smoke jobs switch the testcontainers reaper off,
because a GitHub-hosted runner is destroyed with the job and there is nothing
left to reap. It costs a CI that cleans up differently from a developer's
machine, and closes a race that failed the lane twice in one day.

- **Status:** Accepted
- **Date:** 2026-09-11

## Context

Thirty-eight sites in this repository start a container, spread over roughly
thirty packages, and `go test ./...` runs several package processes at once.
testcontainers-go starts a Ryuk container to remove what a run leaks, and ONE
Ryuk is shared by every process of a test session — `reuseOrCreate` looks it up
by label expressly so that parallel processes find each other's.

That sharing is the race. Ryuk terminates itself ten seconds after its last
client disconnects and its container is created with `AutoRemove`, so between one
process finishing and the next one starting there is a window in which the
container has EXITED but its record still exists. The lookup passes `All: true`,
so it matches that record; the reuse path then waits for a log line and an
exposed port, an exited container publishes no ports, and the wait's default
deadline is sixty seconds. The outer retry cannot save it: its whole budget is
twenty seconds, already spent by the single attempt.

CI fell this way twice on 2026-09-11 — `plugins/paymentpaytr` in the morning and
`internal/workflows/checkout` in the afternoon — in unrelated packages, each after
roughly sixty seconds, each taking the whole lane red. Both were reported as "the
postgres container could not be started", which names the container the test
wanted rather than the one it was waiting for. A red lane that is red for a
reason nobody can act on teaches the next person that red means nothing.

Measurement: [measurements/0138](../measurements/0138-what-the-lane-was-waiting-for.md)

## Decision

The Integration and Smoke jobs set `TESTCONTAINERS_RYUK_DISABLED`, and nothing
else in the repository does. The reaper removes containers on a machine that
outlives the test run; a GitHub-hosted runner does not outlive it, so on that
machine the reaper protects against nothing and can only lose the race above.

## Consequences

CI and a developer's machine now run the same harness differently, and that is
the real cost: a leak the reaper would have swept is invisible in CI, so a test
that forgets to terminate its container is a fault nobody will be shown until
somebody runs the lane locally. The gain is proportionate — the integration job
stops paying for a container it cannot use and stops depending on a
cross-process handshake it does not need.

The premise is the load-bearing part, and it is not visible from the place that
relies on it: "the machine is thrown away" is a property of one word in a YAML
file, and the decision explaining why that matters lives here. So
`internal/arch/reaper_test.go` pins it in both directions — a job may disable the
reaper only while its `runs-on` names a runner this repository knows to be
ephemeral, and no Makefile, script or env file may disable it at all. A runner
label nobody has classified is refused rather than assumed, which makes adopting
one a deliberate edit.

The race remains possible for anyone running the lane locally, deliberately: on
that machine the reaper is what keeps the containers from piling up, and a rare
sixty-second failure is the cheaper of the two.

## Rejected

- **Raise `ryuk.reconnection.timeout` so the shared reaper outlives the gap.** It
  widens the window rather than closing it, and the right value is a guess about
  how long a package takes.
- **Run the packages serially with `-p 1`.** It closes the race by removing the
  parallelism the lane's ten minutes depend on, and the race is not the cost of
  parallelism but of a shared handshake.
- **Retry the whole lane on failure.** It hides a reproducible mechanism behind a
  second run, and it is the habit that makes a red lane mean nothing.
- **Give each package its own session id so no reaper is shared.** It trades one
  Ryuk for thirty, on a machine where none of them is needed.
