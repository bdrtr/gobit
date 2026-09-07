# ADR 0037 — The audit log gains a reader, and reading it is recorded

- **Status:** Accepted
- **Date:** 2026-09-07
- **Phase:** after the roadmap

## Context

`core/audit` writes one row per admin write: who called it, what they called,
what came back. It has done so since the middleware was built, and **nothing in
this repository has ever read a row.** `Store` has one method and it is `Write`.

That is not a gap somebody forgot to mention. It is the defect this repository
names most often, quoted back at itself in four separate places:

| where | what it says |
|---|---|
| `core/eventbus/outbox` | "the mistake this repository has already made once, in `audit_log`; a dead letter nothing reads" |
| `plugins/webhookout` (migration) | "the mistake this repository has already made once, in `audit_log`" |
| `plugins/webhookout` (module) | "once, in `audit_log`. The failure does not clear itself" |
| `internal/jobs/outboxrelay` | "the write-only ledger this repository has already built once, in `audit_log`" |

Every one of those is a place where the lesson was applied — the outbox has a
dead-letter listing, the webhook plugin has an operator surface with exits, the
relay fails its run while the pile is non-empty. The lesson was learned
everywhere except where it was learned.

**The table was built to be read.** Its migration carries two indexes and names
the two questions in its own comment: *"what did this person do"* and *"what
happened to this endpoint"*, both newest-first, both with the id as a tiebreaker
— which is the shape of a keyset page. The design anticipated a reader that was
never written, and until one existed the indexes were pure write cost.

## Decision

**`audit.Store` gains `List`, and one admin endpoint reads it:
`GET /admin/v1/audit-log`, behind a new `audit:read` scope.**

**Paging is keyset, not offset.** An audit log is append-only and read
newest-first, which is the shape offset is worst at. Rows keep arriving while a
reader walks, and under offset each arrival shifts every later page by one — so
somebody following an incident silently misses a row or sees it twice. The
position is `(created_at, id)`, written as a ROW comparison so it becomes an
index condition rather than a filter over rows the index already returned.

**The store speaks in a moment and an id; the endpoint speaks in an opaque
cursor.** Encoding a position into a string is an HTTP concern and the code for
it is in an internal package a published one may not import (ADR 0026's rule,
enforced by `internal/arch`). The split turned out to be the right one anyway:
the store's business is a keyset position, and what a caller wraps it in is the
caller's.

**A third index is added.** The two the migration shipped both lead with another
column, so neither can satisfy an ordering by `created_at`. The reader brought a
third question — *what happened most recently*, the one an operator asks before
they know whose or which — and it had no index at all. It is affordable here for
a reason specific to this table: `audit_log` is append-only and `created_at` is
monotonic, so every insert lands in the rightmost leaf and no page splits.

**Reading the audit log IS audited.** This is the decision with a cost, and it is
the reason this is an ADR rather than a commit.

## Why one read is recorded when reads are not

The middleware records the four methods that change something, and its stated
reason for excluding reads is that "knowing somebody listed the orders answers no
question, and recording every read would bury the writes in volume". Both halves
are true and neither survives contact with this one path.

Who read the record of who did what is the question an incident starts with. It
is also the one read an intruder makes: they cannot alter the log — there is no
endpoint that deletes a row and no plan to add one — but they can learn from it
what is known about them, and that they looked is a fact worth keeping.

**The exception is a list of exact paths, not a rule.** A predicate would let an
installation audit reads by shape, and the cost of this decision is exactly its
breadth: every audited read is a row, and a log that records reads in volume
buries the writes it exists for. An exact-path list cannot grow by accident, and
the composition root passes exactly one path.

The comparison ignores the query string. A listing's filters would otherwise
multiply one endpoint into an unbounded set of entries — an operator paging
through an incident writing a row per page. What the row answers is that the log
was read, by whom, and with what outcome.

## Rejected alternatives

**Leave it write-only and read the table with psql.** This is what the last year
of this repository did, and it is what the four quotations above call a mistake
when any other component does it. It also means the only people who can answer
"who changed this" are the ones with database credentials, which is a strictly
larger power than reading an audit log.

**Offset paging.** Simpler, and wrong for this table specifically — see above.
The failure it produces is a reader silently missing the row they are looking
for, during the one activity this endpoint exists for.

**Put the listing under an existing resource.** There is no resource it belongs
to: a row records a request to any admin endpoint, and hanging the log off one of
them would say it belonged there.

**Audit reads generally, or audit no reads at all.** The first buries the writes;
the second leaves the one read that matters unrecorded. The middle is narrower
than either and its narrowness is enforced by an exact-match list.

**Make the endpoint exist even when no audit store is configured.** It would
answer with an empty page, and a client cannot tell that from a log that recorded
nothing. During an incident those are opposite facts, so the route is not bound
at all.

## Consequences

**Positive**

- **The indexes stop being write cost with no reader.** Two of them have been
  maintained on every audited request and answered nothing.
- **"Who changed this" is answerable without database credentials**, which is a
  smaller power than the alternative required.
- **Reading the log is itself in the log**, which is what makes the record
  useful against somebody who has an admin token.

**Negative, and accepted**

- **A third index on a write-heavy table.** One more B-tree write per audited
  request. It is the cheapest kind (monotonic key, no page splits) and it is
  still a cost.
- **`core/http.Audit` gains a variadic option and `GuardOptions` a field.** Both
  are additive and both are published surface. The alternative — a literal path
  inside `core/http` — would put an application's route in a package that must
  not know it.
- **The endpoint returns personal-ish data.** `actor_id` names a person, and the
  paths can carry customer and order ids. That is what an audit log is; the
  scope is what limits who sees it, and `personaldata`'s declaration audit does
  not cover `core/audit` because the table records ACTORS rather than subjects.
  Whether an actor id is personal data in a given deployment is the embedder's
  call under ADR 0029, and this ADR does not decide it for them.

## What this deliberately does NOT do

- **No retention, no deletion.** There is no endpoint that removes a row and no
  window after which rows disappear. A log that can be pruned through the API is
  a log an intruder can prune; a retention window is a legal choice ADR 0029
  leaves to the embedder, who can express it with a scheduled statement.
- **No diff.** What changed is still read from the record itself, which carries
  its own `updated_at`. The reasoning is in the table's own comment and this
  decision does not reopen it.
- **No storefront rows.** That surface is unauthenticated by decision (ADR 0008),
  so a row there would say "somebody".

## Reopening the decision

Reopen the retention half the first time an installation reports the table's
size as a problem — that is the signal, not a schedule — and the answer is most
likely a partition or a scheduled `DELETE` an operator runs, not an endpoint.

Reopen the "reads are not audited" rule if a second path ever earns the
exception. Two paths is still a list; the day it wants to be a pattern is the day
the cost argument above has to be made again with numbers.

## Related

- [ADR 0026](0026-the-published-surface-is-fourteen-packages.md) — why the store
  may not import the internal package that encodes cursors.
- [ADR 0029](0029-the-embedder-is-the-data-controller.md) — who decides whether
  an actor id is personal data, and who owns a retention window.
- [ADR 0008](0008-musteri-kimligi-guven-siniri.md) — why the storefront is not
  audited.
