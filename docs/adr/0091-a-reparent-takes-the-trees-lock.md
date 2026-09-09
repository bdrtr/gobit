# ADR 0091 — A reparent takes the tree's lock, because a guard is not a lock

**Summary:** Moving a category under another serializes on an advisory lock; the
statement's cycle guard refuses the second mover once it can see the first.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

ADR 0085 put the cycle rule inside the UPDATE and said that this is what stops
two concurrent reparents from closing a ring between them. It does not, and the
test that "proved" it was passing by luck: one round, and the unprotected tree
loses that round about once in three.

CI ran the unlucky third the day after (D46). The reason is plain once looked
at: the two statements touch DIFFERENT rows, so they take no lock from one
another, and under READ COMMITTED each recursive walk reads a snapshot taken
before the other committed. "Move A under B" and "move B under A" therefore both
see a tree with no ring, both write, and the ring is theirs together.

## Decision

A reparent takes `pg_advisory_xact_lock` on ONE key for the whole tree before
the statement runs, in the same transaction. The two halves are one decision:
the lock makes the second mover wait, and the statement's guard is what refuses
it, because after the wait its walk sees what the first one committed.

An update that cannot close a ring — a rename, a rank, a flag, or clearing a
parent — takes no lock.

## Consequences

Two reparents never run at the same time. That is the price and it is small: a
reparent is an operator action made by hand, and the lock is held for one
statement.

The key is class 2 in the convention the order module's spending lock
introduced, because the advisory key space is one across the whole database and
two unrelated locks that picked the same number would hold each other up with
nothing to notice it.

The race test now runs twenty rounds instead of one. Without the lock an
unprotected tree closes a ring in better than 99.9% of runs, so the test fails
the way a test is supposed to: every time, rather than twice out of three.

ADR 0085's concurrency claim is corrected rather than deleted, and the shape of
the mistake is worth keeping: the guard was real, the reasoning was one step
short, and a single-round race test agreed with the reasoning often enough to
look like proof.

Measurement: none beyond the reproduction — one round in three before the lock,
twenty of twenty after, and the mutation that removes only the lock turns it red
again.

## Rejected

**Locking the two rows the move names.** A cycle is a property of a PATH, so a
third move elsewhere on it can still close the ring; the tree is the thing that
has to be serialized.

**SERIALIZABLE for the statement.** It refuses the loser with a serialization
error and hands the retry to the caller. Nothing in this repository retries a
category write, so the failure would reach an operator as a word they did not
ask about; the lock waits and then answers.

**Doing the walk in the service under the lock.** The read would then be a
second statement and the guard would move out of the write it protects — the
shape ADR 0085 rejected for its own reasons, which still hold.

**Leaving it and making the test tolerant.** The tree really can hold a ring,
and nothing in this module walks a tree, so the ring would be found by whatever
first tried to.
