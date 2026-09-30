# The reaper a package found stopping — measured 2026-09-30

The evidence behind [ADR 0262](../adr/0262-the-local-reaper-waits-out-the-gap.md).

## 1. What failed

The local integration lane (`make test-integration`, `GOFLAGS=-p=1` or `-p=2`)
failed on 2026-09-30 in four runs with one signature, three times in
`internal/modules/b2b` and once in `internal/app`:

```
⏳ Waiting for Reaper "c8ed4cb7" to be ready
<the package's own sentence>: run postgres: generic container: create container: reaper: from container "c8ed4cb7": wait for reaper c8ed4cb7: context deadline exceeded
FAIL	github.com/bdrtr/gobit/internal/modules/b2b	60.844s
```

The wait began at 16:02:08 and ended at 16:03:08. The package passed each time
it was run again on its own.

## 2. The library's path

testcontainers-go v0.44.0, the latest version on the proxy that day:

- Its bootstrap package derives the session id from the parent process id and
  its start time, so every package process of one `go test` shares it, and no
  setting replaces it.
- Its reaper spawner looks the reaper up by that session and, when it finds a
  container, waits for the container's "Started" log line and its exposed
  port. A container that is stopping publishes no port, and the wait runs
  until its deadline.
- Its configuration's ryuk.reconnection.timeout, ten seconds by default and
  read from `TESTCONTAINERS_RYUK_RECONNECTION_TIMEOUT`, is how long the reaper
  lives after its last client leaves.

## 3. The reproduction

Two throwaway integration packages, removed afterwards. The first starts and
terminates one `postgres:16-alpine`. The second sleeps `ZZ_DELAY`, then starts
one. Both ran in one invocation, `-p=1`, so they share the session:

```
ZZ_DELAY=<d> go test -p=1 -tags integration -count=1 -v <first> <second>
```

With the library's ten seconds:

| delay | what the second did | container start |
|---|---|---|
| 5s, 9s, 9.1s … 9.4s (every 0.05s) | reused the reaper | 7.4–8.5 s |
| **9.45s** | **waited for the stopping reaper, failed** | **1m0.594s, `wait for reaper e1cbd3db: context deadline exceeded`** |
| 9.5s, 10s, 10.5s, 11s, 12s, 15s | created a new reaper | 8.2–8.3 s |

With `TESTCONTAINERS_RYUK_RECONNECTION_TIMEOUT=5m`:

| delay | what the second did | container start |
|---|---|---|
| 9.45s | reused the reaper | 7.53 s |
| 9.5s | reused the reaper | 7.58 s |
| 10.5s | reused the reaper | 7.49 s |
| 30s | reused the reaper | 7.53 s |

The delay is not the whole gap: process start and the test binary's own
startup come before it, which is why the window sits near 9.45 seconds of
delay rather than at ten.

## 4. The lane under the setting

The integration lane was run as five `go test -race -tags=integration`
invocations covering all 189 packages of `./...` (`internal/app`,
`internal/e2e`, the modules in two halves, and the rest), plus
`make test-modules-integration`'s two, all with
`TESTCONTAINERS_RYUK_RECONNECTION_TIMEOUT=5m`, while `docker events` recorded
every container. Every invocation passed.

- Eight reapers were created for eight invocations, one each; no package
  had to create another.
- 117 test containers started. The longest stretch with none running inside
  one invocation was 28.7 seconds, and the next longest 20.0, 17.5 and 16.1:
  gaps longer than the library's ten seconds, and far shorter than five
  minutes.
- The reapers of the two multi-package module invocations exited about five
  minutes after their invocation ended.
