# ADR 0019 — Scheduled work is elected by the OCCURRENCE, made live by the LOCK

- **Status:** Accepted
- **Date:** 2026-09-04
- **Phase:** after the roadmap

## Context

gobit had no scheduled work of any kind: no ticker, no cron, no way for a
module or a plugin to ask for anything to happen later. The roadmap that came
out of measuring a real installation against this framework listed four jobs as
the reason to build one — nightly payouts, campaign expiry, abandoned-cart
recovery, and purging rejected applications.

**All four turned out to fail ADR 0009's test, and the measurement is the
decision's foundation rather than a footnote.**

- `payout` appears in **zero** files under `internal/` and `cmd/`. gobit has no
  concept of a payout to settle.
- There is no application to reject: the b2b module's schema is `b2b_company`
  and `b2b_company_employee`, and nothing else.
- Campaign expiry is **already enforced at read time**
  (`promotion/service/compute.go`, `campaignUsable(candidate, in.At)`), so a job
  that flipped a status would change nothing observable.
- Abandoned-cart recovery is not missing. It is **refused**, in writing, in four
  places: ADR 0017, `internal/app/recover.go`, `internal/core/workflow/workflow.go`
  and the README. The refusal is precise — recovery runs COMPENSATIONS, which
  are side effects, and a scheduled job would decide on its own, unwatched, to
  undo work.

So the honest position before writing any code was that the framework had no
consumer for a scheduler at all, and building one for another codebase's jobs
would have been exactly the error class ADR 0009 names.

**What made this decision possible is a distinction inside the existing
refusal.** ADR 0017 forbids ACTING unwatched. ADR 0016, which built
`gobit stuck`, explicitly left the other half unclaimed:

> It is a snapshot, not an alert. Nobody is told a cart is stuck.

And `pgstore/stuck.go` states what that costs, about the class it measured as
unreported:

> that record stays running forever, holds stock, and is mentioned by nothing:
> no log line, no metric, no status.

Held work is reserved inventory. Nobody can buy it and nobody is told. That is
a correctness cost, it has been in the repository since the workflow engine
landed, and reporting it breaks no rule — it is the half ADR 0016 left open.

## Decision

**Scheduled work ships as `internal/core/job`, with exactly ONE job:
`internal/jobs/sagawatch`, which REPORTS abandoned sagas and never touches
them.**

**An occurrence is elected by a row; liveness is answered by a session-scoped
advisory lock. Both, because they answer different questions.**

- The row `job_run (name, due)` answers *"has this occurrence already run?"* —
  frequency and history. The primary key IS the election: the first instance to
  insert wins, every other one gets a conflict. No leader, no coordinator, no
  vote, and it stays correct when instances are partitioned from each other but
  not from the database.
- `pg_try_advisory_lock` answers *"is a process running this job right now?"* —
  concurrency and liveness.

Occurrences are anchored to the **epoch**, not to process start. That anchoring
is the whole election: two instances that booted minutes apart must compute the
same due instant or they never race for the same row, and every replica runs
every job.

## Rejected options

**A. A lock alone (an in-process ticker with no row).** A lock excludes
concurrency but not FREQUENCY. Three replicas ticking on independent phases each
find the lock free at a different moment, so a daily job runs three times a
night — and the fault gets worse as an installation scales out, which is the
opposite of what anyone expects from adding replicas.

**B. A lease (the mechanism the workflow engine is said to have).** Two reasons,
and the first is that gobit does not actually have one: the execution schema is
`id, workflow, idempotency_key, status, input, output, failure, created_at,
updated_at` — no lease column, no owner, no heartbeat, and no sweeper anywhere.
`WithLease` is a caller-side predicate over `updated_at`.

The second reason is that a lease inverts hung and dead. A wedged-but-alive
process keeps renewing and is never taken over, while it prints the healthiest
line in the listing. A lease also forces a bargain nobody can win: its duration
is simultaneously "long enough for the longest run" and "how long a dead run
stays invisible". The repository already carries that scar —
`gobit stuck -stale-after` defaults to checkout's ten minutes, so any longer
flow listed at the default is named abandoned while healthy.

A session lock has no duration at all. The backend exits, PostgreSQL reaps the
lock, the next tick proceeds.

**C. A calendar expression (cron syntax).** A cron expression carries a time
zone, and a time zone carries daylight saving — which means an hour that happens
twice and an hour that does not happen at all. `Every 24h` has neither. A job
that genuinely must run at 02:00 local time belongs behind the operator's own
cron calling `gobit job run`, where the calendar is owned by something that
already understands calendars.

**D. Making a scheduled job a workflow execution.** The engine's shape is a saga
with compensation, and a schedule is not that. It would also have put periodic
work inside the machinery ADR 0017 refuses to run unwatched, which is the one
place this decision must not go.

**E. `Host.RegisterJob` for plugins.** Deferred, not refused. An extension point
with zero things to extend it is ADR 0009's error class in its purest form. It
arrives with the first plugin that brings a job.

**F. A `SCHEDULER_ENABLED` switch.** A capability that can be turned off is a
capability an installation can be silently missing. The runner starts with the
server and says in one line what it is running; an installation that wants no
jobs registers none.

## Consequences

**The lock's key class is 2, and the class is not decoration.** The key space of
`pg_advisory_lock` is a single one across the whole database. Class 0 is
occupied in its entirety by golang-migrate (`crc32(name) * salt`, a uint32), and
golang-migrate waits on its lock with `pg_advisory_lock` on
`context.Background()` — a wait that is unbounded AND uncancellable. A bare hash
of a job name would land inside that range and could block a boot migration on a
wait nobody can interrupt. Class 1 is the order module's per-customer spending
lock; `0x6C696E6B` is the link module's.

**A job must be safe to run twice, and that is a contract rather than a hope.**
The occurrence row is written BEFORE the work, not after, so a process that dies
mid-run leaves the occurrence taken and unfinished. Election makes a second
concurrent run unlikely, not impossible — a process can be partitioned from the
database after taking the lock.

**A job never fails the process and never disappears.** A panic becomes a
recorded failure and a reported error, the same rule the event bus applies to a
subscriber. A failure is recorded as a run that HAPPENED: hiding it would make
the listing claim the job has not run since its last success, which reads as
"it stopped" and sends whoever looks to the wrong question.

**`gobit jobs` is a subcommand, not an endpoint.** It answers "did it run last
night" from a terminal during an incident, when the admin API may be the thing
that is broken — the same reasoning `gobit stuck` was built on.

**The listing cannot tell running from died, and says so.** An unfinished row is
what both leave behind. The lock is what distinguishes them, so the column reads
"unfinished (running now, or the process died)" rather than guessing.

**One consumer is named and NOT built: payment reconciliation.**
`internal/workflows/checkout/doc.go` records it as the only correct way to close
a known hole — *"a periodic comparison against the provider's own ledger"* — and
marks it Phase 7+ work while Phase 7 is marked done. It is the repository's one
unkept periodic promise, and it is about money. It is not built here because
this decision is about the mechanism; building it is a payment-module change.

## Reopening

The extension point reopens with the first plugin that brings a job, and nothing
else needs to change for it: a registration method on the plugin host would add
to the same registry the composition root already uses (see rejected option E).

The calendar decision reopens if a job appears whose correctness depends on
local wall-clock time — a legal reporting window, say. The answer then is not to
put a time zone into `Every` but to decide, in a new ADR, who owns the calendar.

## Related

- ADR 0007 — behaviour on failure.
- ADR 0009 — a capability with no consumer is an error class. This decision's
  first section is that test, applied and mostly failed.
- ADR 0016 — the operator's read surface for half-done sagas, and the alerting
  half it left unclaimed. That gap is this decision's only consumer.
- ADR 0017 — recovery is replayed from the record, and a scheduled sweeper is
  refused. Nothing here acts.
- ADR 0018 — the previous decision that turned on measuring a contract against a
  real consumer rather than assuming one.
- ADR 0020 — payment reconciliation, the consumer this decision named and left
  unbuilt. It is the second job, and it reports without acting for the same
  reason the first one does.
