# ADR 0062 — A callback's ledger is the receiving module's table

**Summary:** A callback gets no ledger table of its own; its durable record is
the table the receiving module already owns, and the ring's log holds the
refusals no module can see.

- **Status:** Accepted
- **Date:** 2026-09-08

## Context

ADR 0056 parked a `callback_log` table on three things it said this repository
could not derive: a reader, a scope beside `audit:read`, and a retention answer
for a population an unauthenticated caller chooses.

All three already exist, in the module that receives the callback. A provider
that reports back instead of being asked cannot answer "is the money held?"
without durable state, so it brings a table whatever this repository decides —
`paytr_payment` carries the moment the provider called, what it said and what it
paid, has a listing behind a module scope, and holds no statement that deletes a
row. A shared table would hold a second copy of that, keyed worse.

What no module's table can hold is a callback whose handler never ran. Those are
in this ring's log, and in an installation with a reporter the ERROR ones leave
the process: `core/errorreport` is wired into the logger by the composition root
and fingerprints a record by the code of the error it carries.

Retention cannot be taken here. gobit holds no retention window anywhere outside
the two idempotency stores, `audit_log` has none by decision, and under ADR 0029
the window is the embedder's — which for a log is the pipeline they already run.

Measurement: [measurements/0062](../measurements/0062-callback-ledger.md).

## Decision

**A callback gets no ledger of its own. Its durable record is the table the
receiving module owns, and the ring's log is the record of what the ring
refused.**

The reader, the scope and the retention are answered where the row is:
`GET /admin/v1/paytr/pending`, behind `paytr:read`, over rows nothing deletes —
which is ADR 0054's "a money record is kept" arriving from the plugin side.

**The contradiction now carries an error value.** It is the one outcome whose own
message says a person has to act, and a line with no error is reported as
`unclassified` — sharing one bucket of three reports a minute with every
genuinely unclassified failure. It reports as `callback_contradiction`.

**The trigger is a SECOND callback PROVIDER in the tree**, and
`TestASecondCallbackRouteReopensTheCallbackLedger` fails the day one appears.
With one provider the cross-provider question has one answer; with two it is a
question no module's table holds and no module's listing should, the reader has
a subject, and the retention must be answered rather than inherited.

## Consequences

- **An operator answers "did the provider call" with a query and "was anyone
  refused" with a log search.** The split is now stated rather than discovered.
- **A refusal has no retention promise from gobit**, and gobit does not pretend
  to one. What keeps it is the embedder's log pipeline.
- **Nothing in the published surface moves.** No migration, no scope, no route,
  and the contradiction's code is unexported because it is returned to nobody.
- **A callback-bringing plugin owes a durable record of what it was told.** The
  one that exists does; nothing enforces it, and the census is where a second
  one is met — it fails on a legitimate route, on purpose, and says so.

## Rejected

- **Build `callback_log` now, with a reader and a `callback:read` scope.** The
  reader would list one provider, and the first consumer would be invented.
- **Build the table and leave it write-only until a reader is asked for.** That
  is the mistake this repository names in four places, in `audit_log`.
- **Take retention as "none, the operator prunes", the way `audit_log` does.**
  That table's population is admin writes by identities this installation
  issued; a callback's is chosen by whoever posts to the path.
- **Leave the row OPEN.** The three requirements are answered; what was missing
  was the reading of the tree, not a decision the owner had to make.
- **A retention window on the ring, dropping lines by age.** It would be gobit
  taking the one number ADR 0029 assigns to the embedder, inside a log the
  embedder's pipeline already governs.
