# ADR 0069 — The job reporting channel is published and the scheduler is not

**Summary:** `core/jobreport` publishes the channel a scheduled run leaves its
one operator line on; the runner stays internal and the core's own jobs report
through the published package, so there is one mechanism and not two.

- **Status:** Accepted
- **Date:** 2026-09-08

## Context

D21's residue was the last live row in the defect ledger: a run that succeeded
could say nothing, fixed for the core's jobs and not for a plugin's. Two plugins
register a periodic job and both drop a number they compute — `paymentpaytr`'s
watch counts payments PayTR never called about, `webhookout`'s pass counts what
it sent and gave up on. Both SUCCEED doing it, so `gobit jobs` showed a blank
DETAIL whether three payments were stuck or none were.

The obstacle was a wall, not an omission: the call lived in `internal/core/job`,
and `core/plugin`, where `Job` lives, may not import an internal package —
`TestNoPublishedPackageImportsAnInternalOne` refuses it.

## Decision

**The CHANNEL moves to `core/jobreport`. The scheduler does not move.** Three
FUNCTIONS and no type — `Report`, `WithReporter`, `Detail`. The runner, the
store, the lock class and the occurrence election stay in `internal/core/job`,
which now depends on the published package rather than the other way round —
ADR 0014's shape: the contract in the core, the implementation at the edge.

**There is ONE mechanism.** The core's own jobs report through the published
package too and `internal/core/job` exports no reporting call of its own — two
ways to say one thing would drift in the column read during an incident.

**The handle stays unexported; the read-back is a FUNCTION.** A caller must be
able to install a reporter and read the line back, since `Report` is a no-op
without one. Two functions do it — the shape core/http already publishes three
times over — where a struct would promise its identity, method set and zero
value until 1.0.0 for nothing extra.

**The consumers ship with it, both of them.** `paymentpaytr` reports its count
every pass, filled cap included; `webhookout` reports its pass, including the
filled batch its ladder already called "a bound the operator can see".

## Consequences

- **The published surface gains one package and three names** — the part that
  cannot be taken back before 1.0.0. `core/` goes from sixteen packages to
  seventeen, named in `internal/arch`'s list so it is an edit, not a file move.
- **What is NOT promised is the scheduler.** ADR 0026's amendment measured that
  a plugin needs the four values of a job definition and not the machine. This
  adds a fifth thing that is not a field, published on the same rule: what an
  outside program must NAME to compile.
- **The channel is still hidden.** Nothing in a job's signature says it may
  report. `plugin.Job`'s Run field now names it, five jobs call it, and a call
  outside a run stays a silent no-op — so the cost of not knowing is a missing
  line, never a dead process.
- **`paymentpaytr`'s pass takes its rows through a narrow interface** so its own
  test can run it without a database. It had no test of its reading path before.
- **A false sentence in `webhookout` is corrected**: its pass said a detail
  reaches the listing only alongside an error. The pile still fails the run,
  because the OUTCOME column is where an alarm belongs.

## Rejected

- **Widen `plugin.Job.Run` to return a string.** A breaking change to a
  published struct, paid by the downstream author whose plugin stops compiling,
  and it breaks `TestEveryJobDefinitionFieldReachesAPluginJob` — Go converts
  neither direction between func types whose results differ.
- **Publish `internal/core/job` whole.** Freezing the runner, the store and the
  lock arithmetic to hand out a one-line report; ADR 0026 refused that price
  once already, for the definition's four fields.
- **Put the channel in `core/plugin`.** The scheduler would import the plugin
  host — chi, the container, the event bus — to hold a context key, and
  `sagawatch`, which never sees a plugin, would import "plugin" to count sagas.
- **An interface, the reporter left internal.** Smaller name; it moves the cost
  onto every plugin that wants to test its job.
- **A success value carried in a non-nil error.** Nil implements nothing, so the
  runner, the failure column and the "FAILED:" prefix would each have to learn
  which errors are not failures.
