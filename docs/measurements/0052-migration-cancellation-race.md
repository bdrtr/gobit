# Migration cancellation: the GracefulStop race — measured 2026-09-08

Evidence for [ADR 0052](../adr/0052-the-migration-cancellation-drops-its-graceful-layer.md).

## The unsynchronized pair, upstream

golang-migrate v4.19.1, `migrate.go`:

```
	GracefulStop chan bool
	isLockedMu   *sync.Mutex

	isGracefulStop bool     // guarded by nothing
	isLocked       bool     // guarded by isLockedMu
```

`stop()` reads `isGracefulStop` and, on receiving from the channel, writes it.
`Up()` runs `readUp` on a new goroutine and `runMigrations` on the caller's, and
both call `stop()` on every loop iteration. The channel is `make(chan bool, 1)`,
so a non-blocking send lands in the buffer and waits for whichever goroutine
calls `stop()` first; the other one then reads the field it wrote.

There is no happens-before edge between them. `ret` is buffered to
`PrefetchMigrations` (10 by default, and gobit never sets it), so with a
three-migration fixture the buffer never fills and the memory model's
sender-ward edge never fires.

In this repository exactly one line armed the write: the `GracefulStop` send in
`db.session.run`, which was ADR 0003's first layer.

## The reproduction that did NOT fire, and what it taught

The plan was to widen the window on purpose: give the fixture more than ten
migrations so `readUp` blocks on its send, stays alive for the whole run, and
races `runMigrations`' write. Fourteen `pg_sleep(0.2)` migrations, cancelled at
900 ms, under `-race`:

```
ok  github.com/bdrtr/gobit/core/db  12.771s
```

No race. Reading the code again explains it, and the explanation sharpens the
diagnosis rather than weakening it: after the signal is sent, `session.run`
closes the connection immediately. `runMigrations` then dies on the closed
connection and returns WITHOUT reaching the top of its loop, so it never calls
`stop()` again; `readUp` stays blocked on its send because nothing drains the
channel, so it never calls `stop()` again either. Neither goroutine reaches the
field.

The window is therefore narrower than "readUp is alive": the runner has to
finish its current statement SUCCESSFULLY after the signal and before the close
severs it, loop, and call `stop()` while the reader is also there. That is
sub-millisecond in isolation and widens under a loaded lane — which is exactly
the shape of a fault seen once in a full `make test-integration` run and never
again.

## The decision measurement: what the layer bought

The regression test pins the error code, the marker table and the version. It
does not pin the DIRTY flag, which is what a graceful stop would plausibly be
buying, so the probe asked for it directly.

Fixture: two `pg_sleep(0.7)` migrations then a `CREATE TABLE` marker, cancelled
at 1 s, run under `-race`.

| | error code | version | dirty | `TestCancellationActuallyStopsRemainingMigrations` |
|---|---|---|---|---|
| with the send (as shipped) | `db_migration_canceled` | 2 | **true** | passes |
| without the send | `db_migration_canceled` | 2 | **true** | passes |

Identical. The layer changed nothing observable, and a cancelled run has always
left the version dirty — ADR 0003 never claimed otherwise, and nobody had
looked.

## What was ruled out by reading

So the next person does not re-walk it:

- Closing a `database/sql` connection while a query runs on it is documented as
  safe, and the postgres driver's `WithConnection` path leaves its `db` handle
  nil, so closing touches only the connection.
- The driver's own `isLocked` is an `atomic.Bool`.
- A migration's `StartedBuffering` and `FinishedReading` stamps are ordered by
  the `io.Pipe` the driver drains to EOF before running a statement.
- The grace-period abandon path can leave `session.close` running against a live
  worker. That is a resource-ordering hazard, not a data race, and it is not the
  path this test takes.
