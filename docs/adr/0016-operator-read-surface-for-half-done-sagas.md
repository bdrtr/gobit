# ADR 0016 — The operator's read surface over half-done sagas lives in the binary

- **Status:** Accepted
- **Date:** 2026-09-03
- **Phase:** after the admin panel round

## Context

A checkout saga that is interrupted mid-flight leaves reserved stock behind.
Since the lease was added, that is no longer silent: an execution whose lease
has expired is closed, becomes `compensation_failed` if work had been done,
keeps its idempotency key and logs at ERROR. What it still does not do is give
the human anything to look at. README's known limits say it plainly: dropping
the hanging reservation is an operator's job today and there is no management
surface that LISTS them.

The rows are in `workflow_executions` and `workflow_execution_steps`
(`internal/core/workflow/pgstore`). Today the operator opens psql.

### What the operator has to type today — measured

The situation was built in an integration test (an execution in
`compensation_failed` whose step outputs record what was reserved) and the psql
an operator would need was run against it. Two statements, and five things
they have to know that are written down nowhere in SQL:

1. `SELECT id, workflow, status, failure, updated_at FROM workflow_executions
   WHERE status = 'compensation_failed'` — the status string is a Go constant
   (`workflow.StatusCompensationFailed`), not a documented value.
2. A join to `workflow_execution_steps` on `execution_id`, ordered by
   `step_index` — the column is not called `index`, because that is a reserved
   word.
3. Which step statuses mean the side effect is still in the world. `invoked`
   and `compensation_failed` are held; `compensated` and `failed` are not. The
   rule lived in an unexported engine predicate.
4. That the reservation ids are inside the JSONB `output` of a step, in a shape
   defined by a struct in `internal/workflows/checkout`.
5. That the CART is not a column at all — it is inside the execution's `input`.

### The larger half of the problem, also measured

The status-only query answers a smaller question than it looks like. Reaching
`compensation_failed` requires **something to happen**, and the two things that
can are both in-process: the engine writes it live in `unwind()` when a step
fails and its compensation fails too, and it writes it on the replay path when a
caller arrives with the key of a record whose lease has expired. Neither fires
for the shape that matters most here — a process that DIED mid-saga while the
shopper never came back.

Such an execution stays `running` forever, holds its stock, and is mentioned by
nothing — no log line, no metric, no status. Measured on the fixture: six
executions covering every terminal state, of which two need a human, and the
one-line status query finds **one of the two**. The one it misses is the one
nothing else reports.

So the surface has to cover two classes, and they are not the same condition:

- `compensation_failed` — already closed by the engine, already logged.
- `running`, past the workflow's lease, with at least one held step — noticed by
  nothing.

## Options considered

**A. An `/admin/v1` endpoint.** Admin API routes are published by a module's
`api` package and mounted through `module.Registry`; the rules that keep that
honest (`TestHerModulBilesimKokundeKayitli`,
`TestHTTPYuzeyleriYalnizcaApiPaketlerinde`) are written for the module tree.
The saga engine is CORE and no module owns it. Publishing these rows would mean
either inventing a "workflow module" — claiming a commerce module's identity,
migrations ownership and interop name for a core component — or letting core
grow an HTTP surface it has never had.

**B. A screen in the admin panel (`internal/adminui`).** Mechanically possible:
the panel already resolves narrow surfaces from the container by name. It was
rejected on the panel's own ADR. [ADR 0011](0011-yonetim-paneli-dorduncu-agac.md)
Decision 6 puts the panel's reads on the framework's read paths — the Query
layer of [ADR 0004](0004-query-veri-erisimi.md) — and workflow executions are
not there and cannot be: Query is the cross-MODULE read layer built on link
definitions, and the engine is core with no module identity and no links. More
decisive is that ADR's own reopening clause, which names as trigger #1 *"the
first moment a panel screen can do something the framework's API does not
offer — at that moment the panel stops being a reference consumer and becomes a
privileged second path."* A screen over core's execution tables is exactly that
moment. Taking it would mean reopening Decision 6, not applying it.

**C. A subcommand on the binary.** The shape this repository has already written
down as the remedy for a gap of the same class: README's known limits and
`docs/mimari.md`'s limits table both name a *"`cmd/server` migrate subcommand"*
as the exit for `db.MigrateDown` being callable from Go and from nowhere else.
`make migrate-down` exists only to say the surface is missing.

## Decision

**Option C.** The listing is `gobit stuck`, a read-only subcommand of
`cmd/server`, over a `Reader` type in `internal/core/workflow/pgstore`.

### 1. The query lives with the schema, not with the caller

`pgstore.Reader` is a separate type and NOT a method on `workflow.Store`. The
Store contract is what the ENGINE consumes and the engine never lists; adding a
listing method there would force the in-memory store to grow an implementation
nothing exercises. It is also what makes the read-only promise checkable by
reading one small file.

### 2. The held-step rule has ONE definition

`invoked` and `compensation_failed` mean the side effect is still in the world.
That rule already existed, unexported, inside the engine's abandonment
decision. It is now `workflow.StepStatus.Held`, and the SQL filter receives
`workflow.HeldStepStatuses()` as a query PARAMETER rather than embedding the
strings — the same refusal `updateStatusSQL` already makes for
`StatusFailed`. A test parses the status constants out of the source and
requires the list and the predicate to agree, so a fifth status cannot be added
without someone deciding what held means for it.

### 3. The command READS and will never write

No statement it issues is anything but a SELECT, and this is not a matter of
scope. Releasing a reservation while its saga is still running reserves the
stock a second time — the failure `workflow.WithLease` spends its whole godoc
avoiding. A listing is safe at any moment; a release is not, and this surface
never has to decide which moment it is. The promise is checked, not asserted: an
integration test hashes both tables around a call that returns rows.

The same reasoning bounds what it prints. A step whose status says it was
compensated does NOT get its output printed, even though the output still names
the reservation ids: printed under a "here is what is stuck" heading, that list
reads as a list of things to go undo.

### 4. Both bounds are the caller's, and both are printed

`StuckFilter` rejects a zero `StaleAfter` and a zero `Limit` instead of
defaulting them. The staleness cutoff must be at least the lease the workflow
declares — set shorter, the listing names sagas that are still running — and the
safe value is a property of the workflow, which the store does not know. The
command supplies `checkout.ExecutionLease` as its default and prints the value
it used, next to the cutoff instant the database computed.

`Limit` is a page for a human, not a cost limit: measured against 52,000
executions and 52,000 steps, the listing takes 13.8 ms (best of five, local
Postgres) and the plan is a sequential scan of `workflow_executions`. No index
was added; the write cost on the checkout path would be paid by every order to
speed up a command run by hand.

Whether the cap was hit is on the last line. A listing that quietly stopped at
fifty would report a smaller incident than the real one, and the operator would
release what they were shown and believe they were finished.

### 5. No argument still means SERVE

Every deployment, Dockerfile and `make run` invokes the binary with no argument.
An unknown verb FAILS instead of falling through: a typo that started a listener
would leave the operator waiting for output that never comes, and in a
production container it would raise a second server against the live database.

## Consequences

**Positive**

- No new identity surface. The command runs with the server's own environment
  and reaches the database with the same credential psql already needs, so the
  execution inputs — which carry cart contents — become readable to nobody who
  could not already read the table.
- The framework's HTTP surfaces do not change. The admin API stays
  header-only, the panel stays a reference consumer of the read paths, and
  ADR 0011's Decision 6 stays closed.
- The class nothing reported is now reportable at all.

**Negative — accepted costs**

- **The binary grows a verb, and a dispatcher.** Every future subcommand now has
  a place to go, which is convenient and is also how a composition root turns
  into a toolbox. The wiring is guarded (`TestMainDispatchesRatherThanServingDirectly`)
  but the growth is not.
- **It requires a shell on the host.** An operator with a browser and no
  `kubectl exec` cannot run it. That is a real gap and it is the reason
  Option B is not dead, only postponed.
- **It is a snapshot, not an alert.** Nobody is told a cart is stuck; somebody
  has to go and ask. A metric over the same two classes would change that and is
  not in this decision.
- **The output is text and unstable.** It is written to be read by a human
  during an incident, so it is not a machine interface and there is no promise
  that its lines keep their shape.

## Rejected

**Putting the listing on `workflow.Store`.** The engine would carry a method it
never calls and the in-memory store would grow an untested implementation, for
the convenience of one caller.

**Defaulting `StaleAfter` inside the store.** A default would have to be a
number the store invented, and the only safe number is the workflow's own lease.
Getting it wrong is not a cosmetic error: a cutoff shorter than the lease puts
live sagas on an operator's "release this" list.

**Adding an index for the listing.** Rejected on the measurement: 13.8 ms at
52,000 executions, run by hand. The cost would land on every order write.

**Letting the command release a reservation.** This is the whole point of the
separation and it is written here so that adding it later is a decision and not
a convenience.

## Reopening

Two facts reopen this:

1. **An operator without shell access needs the answer.** That is the argument
   for the panel screen, and taking it means reopening ADR 0011's Decision 6
   deliberately rather than sliding into it.
2. **The engine learns to replay a compensation chain.** Today it cannot: it
   stores step outputs but not `StepContext.Shared`. If it ever can, the safe
   home for a "compensate this execution" action is a decision this ADR does not
   make.

## Related

- [ADR 0004](0004-query-veri-erisimi.md) — the read path the panel uses, and the
  one these rows are not on.
- [ADR 0006](0006-workflow-modul-erisimi.md) — why the workflow tree knows no
  module; the reason the engine has no module to publish it.
- [ADR 0011](0011-yonetim-paneli-dorduncu-agac.md) — the panel's placement and
  its Decision 6, whose reopening clause rules out Option B today.
