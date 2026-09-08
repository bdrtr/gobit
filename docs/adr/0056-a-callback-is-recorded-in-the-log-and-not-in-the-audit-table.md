# ADR 0056 — A callback is recorded in the log, and not in `audit_log`

**Summary:** A callback leaves a line in the ring's log rather than a row in
`audit_log`, and every outcome leaves one, refusals included. It costs an
operator a log search where they expected a query.

- **Status:** Accepted
- **Date:** 2026-09-08

## Context

ADR 0028 gave `/paytr/callback` its guards and left it no record; its first open
decision was whether a verified provider becomes an actor.

`audit_log.actor_id` holds an identifier this installation ISSUED, and the index
over it answers "what did this person do". A callback's actor would be
`CallbackRoute.Source`: a literal a plugin compiles in, identical on every row
forever, issued by nobody, and on a refused callback proved by nothing. Six
places in the contract say the table records ADMIN writes, and one already
refuses a surface for exactly this reason.

The deciding fact is not the contract. What is worth recording is WHICH of five
answers went back, and `audit_log` has no column but `status` for it — while
`status` belongs to the provider's protocol: PayTR answers an accepted payment
and a contradicting retry both with 200, so the row meaning "a human has to
look" would be identical to an ordinary one. And a record covering only the
callbacks that PASSED is evidence about the guards, while the entries a reader
opens the log for are all requests a guard stopped.

Measurement: [measurements/0056](../measurements/0056-callback-audit.md).

## Decision

**A callback does not become an `audit_log` row. Its record is the ring's own
log, and every outcome leaves a line there, refusals included.**

The population is drawn where `corehttp.CallbackRegistry` RECOGNIZES a route,
before the quota and before the signature check, so a throttled or forged
callback is in the record. Four outcomes were silent, and all four were the one
class a guard does not produce — the handler RAN, and then succeeded, failed, or
ran with no replay record. Each now writes a line carrying the source, the path
and the status, and `TestNoCallbackOutcomeIsSilent` drives twelve outcomes and
fails on one that says nothing or names no callback.

A durable callback ledger is not built. It is reopened by a reader: a table
nothing reads is the write-only ledger ADR 0037 was written about.

## Consequences

- **A refused callback is now in the record** — the signature failure, the
  throttled flood and the unkeyable payload each leave a line naming the
  provider.
- **A log line is not an audit row.** No retention promise, no query; an
  operator reading `GET /admin/v1/audit-log` finds no callback there.
- **The population is chosen by the caller**, affordable in a log and not in a
  table: the quota defaults to 600 a minute and `audit_log` has no retention.
- **Nothing in the published surface moves.** `audit_log` keeps two actor kinds,
  `Principal.Kind` keeps its pair, and no migration is added.
- **A panicking handler still leaves no line from the ring**; the recoverer and
  the access log carry it.
- **The census is a FLOOR, not a fence.** It holds the twelve known outcomes; a
  branch added later can still end a callback in silence, measured and written
  into the guard's godoc.

## Rejected

- **A third `actor_kind`, with the source as the actor.** It puts an identifier
  nobody issued in the column an operator filters people by.
- **An outcome column on `audit_log`.** One surface would write it; every other
  row would leave it empty and the table would be two tables sharing a name.
- **A row only for VERIFIED callbacks.** The cheap version, and the trap: the
  log becomes evidence about the requests that passed.
- **A `callback_log` table now.** It fits the facts, and it needs a reader, a
  scope and a retention answer first — all the owner's.
- **Nothing, with the access log as the record.** It names no source, and its
  status cannot name the outcome.
- **A callback-outcome hook on `CallbackOptions`, or an event.** It hands the
  reader, the scope and the retention to the embedder, which is what makes it
  attractive — and it widens a published struct (ADR 0026) for a consumer that
  does not exist. It is the candidate to reach for the day one does.
