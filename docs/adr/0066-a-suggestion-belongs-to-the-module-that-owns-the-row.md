# ADR 0066 — A suggestion belongs to the module that owns the row, and none is built until a proposal outlives its computation

**Summary:** gobit builds no suggestion store. When one arrives it belongs to the
module that owns the row it is about, and the trigger is the first proposal a
query cannot reproduce.

- **Status:** Accepted
- **Date:** 2026-09-08

## Context

Gap B16 asks for a store where the system proposes and a human applies. Of the
82 tables the 52 production migrations create none is named for a proposal, and
[nothing writes or reads one](../measurements/0066-suggestion-store.md).

The tree holds the shape twice: `internal/jobs/sagawatch` and
`internal/jobs/paymentrecon` measure, report and never act
([ADR 0017](0017-recovering-abandoned-sagas-from-the-record.md)). Neither stores
a finding, and that is the criterion — both are REPRODUCIBLE, `gobit stuck`
reruns one, so each leaves one counted line per run in `job_run`'s detail
column.

Everything needing more is an AI row, and nothing here computes anything from a
model. The reader end is as empty: the queue a review suggestion would annotate
is `GET /admin/v1/reviews`, and the panel has no page for it. Building ahead of
either end is refused mechanically — a column no INSERT or UPDATE names fails
`TestEveryColumnIsWrittenBySomething`, whose exemption map is empty by policy.

## Decision

**1. No suggestion store is built.** B16 stops being a gap and becomes this.

**2. The trigger is the first proposal a query cannot reproduce.** A production
file computes, for one named row, the value it proposes that row should take,
out of an input the database does not hold: a model call, a paid outside call,
an input nothing keeps. Both proposers above fail that test.

**3. A suggestion is stored by the module owning the row it is about**, and
applied through that module's existing write path. No shared table, no generic
applier.

**4. The shape follows the target.** A proposal to CHANGE a row is columns on
that row — the proposed value, what proposed it, when. A proposal to CREATE one
is a pending row in the owner's table with a state column, and the whole-set
setter learns of it then: the product's category map is a delete-then-insert.

**5. Nothing in gobit applies a suggestion.** The apply is a human calling the
endpoint they already had, with the value the machine proposed; the same write
clears the proposal. That is ADR 0017's refusal, not a new one.

**6. Scope, reader and retention follow from 3.** A bound suggestion takes its
target's scope, is read by the surface listing it, and dies with the row — three
things [ADR 0056](0056-a-callback-is-recorded-in-the-log-and-not-in-the-audit-table.md)
could not derive for a table with no owner.

## Consequences

The first AI producer inherits a shape and pays only for its own half, and the
gate keeps its meaning because nothing was added for it to forgive. The costs
are real: an operator wanting a proposal today gets nothing; a proposal over two
modules — refund this order AND restock its lines — has no home here; six
proposing tables mean six queues; and the first producer's author pays for it.

## Rejected

**One generic `suggestion` table now.** It is what `audit_log` looks like, and
that is the objection: nothing ever APPLIES an audit row. Applying writes
another module's rows, so a generic table needs a generic applier listing every
module's write surface a second time, drifting from the first.

**Take one of the two jobs as its first consumer.** Both do propose to a human.
Killed by reproducibility: the rows would be a cache able to disagree with what
it came from, which is worse than the query it replaced.

**Add the columns to `reviews` now**, the closest measured case. Killed by the
empty exemption map, and by the moderation mirror — a suggestion must be shown
not to be a moderation before it is stored beside one.

**Leave B16 open.** Killed by ADR 0045's precedent: the question gets answered
anyway, by whoever writes the next feature, and the shape is the expensive half.
