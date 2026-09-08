# ADR 0003 — Migration cancellation: we own the connection

**Summary:** Migration cancellation works because gobit owns the connection and
can close it; the layers above cooperate rather than relying on `context`
alone. A cancelled run leaves the version table honest about what did and did
not apply.

- **Status:** Accepted
- **Date:** 2026-08-23
- **Phase:** 1

## Context

Under the `Module.Migrations() fs.FS` contract, migrations are applied with
`golang-migrate`. But the library's PostgreSQL driver **never uses the caller's
context anywhere after setup**:

- `Run`, `Version`, `SetVersion`, `Drop` — all `context.Background()`
- `Lock` — on `SELECT pg_advisory_lock($1)`, which by the source code's own
  comment *"will wait indefinitely until the lock can be acquired"*

This leads to two concrete faults:

1. **Indefinite hang.** A `Migrate` call against a database behind a firewall
   that drops packets blocks until the operating system's TCP timeout (minutes),
   despite the caller's 30-second budget. If two replicas come up at the same
   time, the second waits indefinitely on the advisory lock.
2. **A silent half-schema.** If cancellation only stops the *waiting* and leaves
   the work running in the background, the caller gets a "cut off midway" error
   while the abandoned goroutine goes on applying the remaining migrations. The
   schema silently completes after a call that returned an error.

## Alternatives considered

**A. `GracefulStop` alone** — golang-migrate stops *between* migrations. It
cannot stop a single long migration in flight, nor the `Lock()` wait.

**B. Timeout parameters in the DSN** (`connect_timeout`, `x-statement-timeout`)
— these bound a single *statement*. They do not stop a migration sequence made
of short statements running back to back; measured, it did not stop it.

**C. Abandon the goroutine and return at the ctx boundary** — the caller returns
on time, but fault number 2 remains exactly as it was. Measured: after the
cancellation was reported the remaining migrations were applied and the version
went to 3.

**D. Own the connection** — we open the `*sql.Conn` ourselves and hand it to the
driver with `postgres.WithConnection(ctx, conn, cfg)`. On cancellation we close
the connection: the in-flight statement is severed, and every subsequent
statement fails.

## Decision

**D**, together with A and in layers.

The `session` type in `core/db` owns the connection. On cancellation, in order:

1. `GracefulStop` — the next migration is prevented from *starting*,
2. `conn.Close()` — the *in-flight* statement is severed,
3. the work is waited on to actually finish (`cancelGracePeriod`); the goroutine
   is not abandoned, and if it does not finish that fact is stated explicitly in
   the error message,
4. after the caller has returned, `defer session.close()` releases every
   remaining resource.

A side benefit: the version table is now supplied through
`postgres.Config.MigrationsTable` rather than by writing `x-migrations-table`
into the DSN — no parameter has to be injected into the DSN. So that the DSN
scheme is not hidden by `sql.Open`'s laziness, it is additionally validated
before connecting.

## Consequences

**Positive:** `ctx` really does set a bound; a canceled migration flow does not
advance after the return; against an unreachable server the call returns within
budget.

**Negative:** The `database/sql` + `pgx/stdlib` layer means a second connection
path alongside `pgxpool`. Because a migration runs over a single connection
(`SetMaxOpenConns(1)` — the advisory lock must be taken and released on the same
connection), the cost is negligible.

**Test note:** Because the fix is layered, individual mutations (removing only
`GracefulStop`, or only `conn.Close()`) do not fail the regression test — the
other layer catches it. `TestCancellationActuallyStopsRemainingMigrations`
exercises the property end to end and, under the full mutation (the goroutine
abandoned entirely), fails by going to version 3; this was verified.

## Related

- Plan Section 8 (Migration conventions), Phase 1
- `core/db/migrate.go` — `session.run`
