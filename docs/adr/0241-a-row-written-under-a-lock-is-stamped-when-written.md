# ADR 0241 — A row written under a lock is stamped when it is written

**Summary:** A moment the database writes under a lock, where the order of
such rows decides what they say, is the moment of the write,
`clock_timestamp()`, not the start of the transaction, `now()`.

- **Status:** Accepted
- **Date:** 2026-09-29
- **Amends:** [0053](0053-the-two-clocks-stay-and-every-moment-names-its-own.md), whose database clock was the transaction's start

Measurement: [measurements/0241](../measurements/0241-the-change-that-waited.md)

## Context

`now()` is the moment a transaction began. A write that takes a lock first and
waited for it is written after the write that held the lock, and stamped
before it (D164). Four readers decide by that order: the last delivery change
is the delivery a parcel is opened with, an address correction that waited was
superseded before its row had been written and its CHECK answered a 500, an
exchange completed after its funding read as funded in the order's history,
and the newest stock movement no longer carried what its level counts.

## Decision

The delivery changes, the address rows and their supersede moment, and an
exchange's transition moments are stamped with `clock_timestamp()`. A level's
write is stamped the same way, and the movement that explains it carries that
same moment, passed in rather than defaulted.

## Consequences

- The lock puts the moments in the order the rows were written, so the four
  readers see the later write as the later one. The rows already stored keep
  their stamps.
- A movement and its level still carry one moment (ADR 0053); it is now the
  level write's. The repository refuses a movement with no moment.
- Rows of one transaction no longer share a moment where these stamps are
  taken; a keyset over them still breaks ties by id.
- The rest of the audited stamps move by a lock wait only in a list, a timeline
  or a journal window, and keep `now()`; the measurement names them. The price
  histories and invoices read the process clock before their lock, which is
  D165's.
- Order migration 000037 changes two column defaults; the other changes are
  queries.

## Rejected

- **Every `now()` in the schema.** Rows written together in one transaction
  would stop sharing a moment where nothing needs them to, and the readers that
  sort by the stamp and then by id or seq were measured correct without it.
- **Ordering the delivery changes by an identity column.** The rows already
  there are numbered in table order, and the stamp a person reads would still
  say the wrong thing.
- **Taking the order's lock before the transaction's first statement.** The
  lock is the transaction's first statement; the wait is inside it, whichever
  statement comes first.
