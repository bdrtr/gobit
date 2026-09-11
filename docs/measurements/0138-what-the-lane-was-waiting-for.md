# What the lane was waiting for

Evidence for [ADR 0138](../adr/0138-the-reaper-is-off-where-the-machine-is-thrown-away.md).

Measured 2026-09-11, against `testcontainers-go v0.44.0` as resolved in `go.mod`.

## The two failures

`gh run list --limit 40` over the repository's CI history returns 33 successes,
4 cancellations and **2 failures**. Both failures are the same mechanism, in
packages that share nothing.

| Run | When | Package | Duration before the error |
|---|---|---|---|
| 34569501920 | 2026-09-11 06:28 | `plugins/paymentpaytr` | 61.52 s |
| 34619619192 | 2026-09-11 16:12 | `internal/workflows/checkout` | 60.31 s |

Both report the same error, and the second one cascaded: the package's other
seven recovery tests failed at 0.00 s each, because the container is brought up
once per binary behind a `sync.Once` and every later test is handed the stored
failure.

```
the postgres container could not be started: run postgres: generic container:
create container: reaper: from container "f0de099f": wait for reaper f0de099f:
context deadline exceeded
```

The first clause names the wrong container. The test wanted Postgres; what timed
out was the **reaper**, and the segment `from container "f0de099f"` is the part
that says so — it is emitted only by `reuseOrCreate`'s reuse branch, the path
taken when an existing Ryuk container was FOUND.

## The mechanism, read from the library

Every row below is a symbol in `github.com/testcontainers/testcontainers-go`
at the version `go.mod` resolves. Symbols rather than line numbers, so the
references survive the next release.

| Fact | Where | Value |
|---|---|---|
| One Ryuk is shared across parallel test processes | `reaperSpawner.reuseOrCreate` | its own comment: "created in the same test session but in a different test process execution e.g. when running tests in parallel" |
| The lookup includes non-running containers | `reaperSpawner.lookupContainer` | `ContainerListOptions{All: true}` |
| The reuse path waits for a log line and an exposed port | `reaperSpawner.fromContainer` | `wait.ForLog("Started")`, `wait.ForExposedPort()` |
| That wait sets no deadline of its own, so each strategy takes the default | `wait.ForAll`, `wait.defaultStartupTimeout` | `60 * time.Second` |
| The outer retry's entire budget | `reaperSpawner.backoff` | `MaxElapsedTime: time.Second * 20` |
| Ryuk stops after the last client disconnects | the library's own `RyukReconnectionTimeout` setting | properties tag `ryuk.reconnection.timeout,default=10s` |
| Ryuk's container removes itself when it stops | `reaperSpawner.newReaper` | `hc.AutoRemove = true` |

Reading them together:

1. A process finishes; ten seconds later Ryuk terminates; `AutoRemove` deletes
   the container record shortly after.
2. A process starting inside that window lists containers by label with
   `All: true` and **matches the exited record**.
3. It takes the reuse path and waits sixty seconds for a port an exited
   container will never publish.
4. The retry that would have looked again is already over budget: twenty seconds
   of allowance, spent by one sixty-second attempt. There is no second look.

The window is short, which is why the rate is two in forty rather than constant.

## The probe: does a label filter really match an exited container?

Step 2 is the only claim not stated outright in the source, so it was run
directly against the same Docker daemon the lane uses.

```
$ docker run -d --name reaper-probe --label org.testcontainers.probe=true alpine:3 true
$ docker ps -a --filter 'label=org.testcontainers.probe=true' \
      --format '{{.Names}}  status={{.Status}}  ports=[{{.Ports}}]'
reaper-probe  status=Exited (0) 1 second ago  ports=[]

$ docker ps --filter 'label=org.testcontainers.probe=true' --format '{{.Names}}' | wc -l
0
```

`docker ps -a` is `All: true` and `docker ps` is `All: false`. The exited
container matches the first and not the second, and its port list is **empty** —
so `wait.ForExposedPort()` against it cannot succeed, and the sixty seconds are
spent waiting for something that is not coming.

## How much of the lane is exposed

```
$ grep -rn 'postgres.Run(' --include='*.go' . | wc -l
38
```

Thirty-eight container sites across roughly thirty packages. `go test ./...`
runs `GOMAXPROCS` packages concurrently with no `-p` limit, so over the lane's
ten minutes there are many transitions between "last client disconnected" and
"next process looks up" — which is the window above.

Smoke is not in this population in practice: `make smoke` runs one package, so
one process, so no reuse path. The switch is set there anyway, because the
reason given in ADR 0138 is the runner's lifecycle and not the race.

## The mutations

Both gates in `internal/arch/reaper_test.go` were mutated with `-count=1`, each
mutation reverted from a copy rather than with `git checkout`.

| # | Mutation | Result |
|---|---|---|
| 1 | Integration job's `runs-on` changed to `self-hosted` | **bit** — named the job and the label |
| 2 | Smoke job's `env:` block deleted | **bit** — 1 job set it, 2 expected |
| 3 | `TESTCONTAINERS_RYUK_DISABLED=true` added to the Makefile's `test-integration` | **bit** — named `../../Makefile` |
| 4 | Scanner's `jobKeyIndent` changed 2 → 3 | **bit** — "only 0 job(s) were understood" |
| 5 | Variable hoisted to workflow level AND the lint job moved to `self-hosted` | **bit** — named `lint`, proving the workflow-level value is carried onto every job |

Mutation 4 is the one worth keeping: it is the blindness case. Without the floor
the scanner would have understood nothing, found no job disabling the reaper,
and passed.

Mutation 5's second half is what makes it worth running. The first half alone
passes — with every job on `ubuntu-latest`, hoisting the variable is harmless —
so the assertion is only exercised when a non-ephemeral job is present to carry
it onto.

## What was not measured

The rate. Two failures in forty runs is 5%, but those forty runs are not forty
identical trials: the lane's package count and its duration both grew over the
window, and a rarer or commoner window in the past cannot be recovered from the
run list. The number is here as the reason to act, not as a baseline to compare
against afterwards.

Whether the disable changes the lane's wall-clock was also not measured. It
removes one container startup per process, which is small next to the Postgres
each package starts; the decision does not rest on it.
