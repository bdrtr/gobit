# ADR 0262 — The local reaper waits out the gap between packages

**Summary:** Every Makefile recipe that starts containers keeps the shared
testcontainers reaper alive for five minutes after its last client, so a
package that starts seconds after the previous one ended finds it running
instead of waiting a minute on one that is stopping.

- **Status:** Accepted
- **Date:** 2026-09-30
- **Amends:** [0138](0138-the-reaper-is-off-where-the-machine-is-thrown-away.md), for its local consequence

## Context

ADR 0138 found the race: one reaper serves every package process of a
`go test` session, it stops ten seconds after its last client leaves, and a
process that looks it up as it stops waits sixty seconds for a port that never
opens. It switched the reaper off in CI and kept the race on a developer's
machine as a rare cost. On 2026-09-30 it failed the local integration lane in
four runs, three of them in the b2b package.

The race was reproduced with two packages in one `go test`, the second
starting its container after a set delay: at 9.40 seconds it reused the
reaper, at 9.45 it failed with the lane's signature after sixty seconds, and
from 9.5 it made a new one.

Measurement: [measurements/0262](../measurements/0262-the-reaper-a-package-found-stopping.md)

## Decision

The Makefile sets `TESTCONTAINERS_RYUK_RECONNECTION_TIMEOUT=5m` once and every
recipe line running `go test` under the integration or smoke tag carries it,
which `internal/arch` holds line by line together with the value being
minutes. CI keeps the reaper off (ADR 0138).

## Consequences

- The window moves rather than closes: it now sits at a gap of five minutes
  between two packages, and the lane's longest gap is seconds. The same
  reproduction reused the reaper at 9.45 seconds, and at 30.
- A lane's leaked containers are removed five minutes after its last package
  instead of ten seconds, and the reaper waits that long before it exits.
- A `go test` started by hand, outside the Makefile, keeps the library's ten
  seconds and the race with it.

## Rejected

- Giving each package process its own reaper: the session id is derived from
  the `go test` process by the library and no setting replaces it.
- Running the integration packages one `go test` at a time: the lane's single
  coverage profile is written by one invocation.
- Retrying the lane on failure, for ADR 0138's reason: it hides a mechanism
  that is now reproducible.
