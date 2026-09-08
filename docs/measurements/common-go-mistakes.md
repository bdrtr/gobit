# Common Go mistakes — measured 2026-09-05

Moved out of `docs/gaps.md` on 2026-09-08 unchanged. A measurement is
evidence, not a decision: it is kept in full and read when a number is
questioned, not on the way to understanding the repository.

The checklist: goroutine leaks (an exit path for every `go`, use errgroup),
transaction boundaries in the service and not the repository, no global state,
and repository tests against a real Postgres rather than mocks.

**This list is mostly already satisfied**, and the value of measuring it is the
three places where the answer is "yes, but".

### Goroutines: all fourteen have an exit path

There are fourteen `go` statements in production code and every one of them
terminates: eleven are tracked by a `sync.WaitGroup`, two exit when a channel is
closed (`for event := range r.queue`), and the Redis consumer loop returns on
`b.ctx.Err()`.

**errgroup is not used and is not in go.mod.** That is not the gap it looks
like: errgroup buys first-error propagation and cancellation, and the places
that fan out here want the OPPOSITE — the parallel saga branch runner collects
every branch's result including the failures, because a compensation has to know
which branches ran. Where a first error should stop the rest, the code cancels a
context instead.

One nuance worth having: the event bus's shutdown starts a goroutine that waits
on the handler WaitGroup and closes a channel, and the caller selects between
that channel and the shutdown budget. If a handler never returns, the SHUTDOWN
returns on the budget — correctly, because the process must be able to stop —
and that one waiter goroutine stays for the life of the process. It is bounded
(one per Shutdown, and Shutdown runs once) and it is the right trade, but it is
the one goroutine in the repository whose exit depends on somebody else's code.

`migrate.go` shows the detail that makes this class safe: `done` is buffered to
1, so when the context runs out the migration goroutine still completes its send
and exits rather than blocking forever on an unread channel.

### Transactions: the boundary is in the service, with a real nuance

Six of sixteen modules carry an open transaction through the CONTEXT — cart,
fulfillment, inventory, invoice, order, payment — and in all six the boundary is
decided by the service, which passes a closure to `repo.WithTx`. The repository
supplies the mechanism; the service says what is inside it.

The other ten have no multi-method transaction and therefore no plumbing, which
is the correct amount of machinery for what they do. Two of them (tax, region)
open a transaction INSIDE a single repository method to make that one method
atomic — a different thing from deciding a business boundary, and defensible.

~~The nuance that will matter one day: those two methods cannot be composed into
a larger transaction. Called from inside a service transaction they would open a
SECOND, independent one, and a rollback of the outer would not undo them. Today
no caller does that. The day one does, the fix is the ambient-transaction
plumbing the other six already have, not a bigger method.~~

**Corrected 2026-09-06, and the wrong half was "today no caller does that".** A
caller did — the tax service's own create paths read a rule and then wrote
against it in two separate transactions — and a godoc in the repository was
already describing the composed behaviour as if it existed. TAX now carries the
transaction in the context like the other six, and its first shared lock exists;
the full record is D6. REGION still has the uncomposable shape, and so, unnamed
by the original sentence, do `pricing`, `promotion`, `auth` and `customer`. The
prescription in the struck paragraph was right and is what was done; what it got
wrong was the deadline.

### Global state: there is none

Zero `init()` functions. Zero package-level mutexes, maps or singletons. Every
package-level `var` in the repository is effectively immutable — an embedded
filesystem, a compiled Redis script, or a SQL string assembled from constants.
Dependencies are struct fields, resolved from the container BY NAME, and the
composition root is the only place that knows what is wired to what.

### Repository tests: real Postgres, and no mocking library at all

There is no gomock, no mockery, no testify/mock in go.mod. ~~Twenty-eight test
files bring up a real PostgreSQL or Redis with testcontainers~~ **Corrected
2026-09-06: the figure was never twenty-eight.** Counted by the testcontainers
IMPORT rather than by the word, thirty-two files brought one up on the date
above and thirty-five do today; the word finds exactly one file the import
does not — `internal/smoke/process_test.go`, which mentions testcontainers in
prose without importing it — and one file the import DOES find is excluded on a
different criterion: `plugins/files3/s3_integration_test.go` imports it and
brings up MinIO rather than PostgreSQL or Redis. **Fifteen of sixteen modules construct their REAL
repository over a real pool** in their integration tests.

### The one real gap on this list

**A repository behaviour that no service path reaches is untested, and the
service-level fakes make that invisible.** The eleven hand-written
`fakes_test.go` files stand in for the repository so the service's DECISIONS can
be tested without a database — which is right — but it means a field dropped
between the model and the INSERT compiles, passes every unit test, and is caught
only if an integration test happens to assert that field.

That is not hypothetical. It happened twice in one change this session: the
order line's `tax_rate_bps` was left out of BOTH the INSERT parameters and the
row conversion, the build was clean, every service test passed against the fake
store, and zero is a legitimate tax rate — so nothing downstream looked wrong.
The first sign would have been an invoice printing 0% VAT on a taxed line. An
integration test writing two different non-default rates and reading them back
is what caught it, and a mutation of each half now fails that test.

The general shape: for every column a module writes, something has to read it
back FROM THE DATABASE. Fifteen modules have the harness to do it; what is
missing is the habit of adding the assertion when the column is added.

---
