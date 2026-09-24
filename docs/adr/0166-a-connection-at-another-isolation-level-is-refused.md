# ADR 0166 — A connection at another isolation level is refused

**Summary:** The connection pool refuses every session whose transactions would
start at any level but READ COMMITTED, when the process starts and whenever it
opens a connection later. Every lock in the tree that guards a total is written
for that level, and nothing had held the database to it.

- **Status:** Accepted
- **Date:** 2026-09-24

Measurement: [measurements/0166](../measurements/0166-what-another-isolation-level-breaks.md)

## Context

ADR 0165 found that the balance lock is safe only at READ COMMITTED — the sum
read after the wait is a fresh snapshot there and a stale one at REPEATABLE
READ — and named the level in the payment repository alone (D119). The same
precondition sits under every lock in the tree that guards a total, and every
transaction and every single statement ran at whatever the server, the database
or the role defaults to.

Run with every connection defaulting to REPEATABLE READ, the integration lane
failed forty tests in fifteen packages. Most failed loudly. Five failed with
every call answered with success and the invariant broken: a spending limit
that covers one order let eight through, a three-unit line took sixteen
cancellations, a 6,100 order was credited 10,000, a category ring closed, and
a location was closed while stock was written into it.

## Decision

The pool core/db builds reads each new session's default transaction isolation
and refuses the connection unless it is READ COMMITTED. It does so for every
connection it opens, so a default changed under a running process is refused at
the next connection rather than obeyed.

## Consequences

ADR 0015's cluster contract gains a row, the default transaction isolation,
and by that record's own rule a widened contract is a breaking change for an
operator: an installation whose database or role defaults to REPEATABLE READ or
SERIALIZABLE does not start, and the error names the level it found and the
statement that sets it back; it no longer reads as an unreachable database.
SERIALIZABLE is refused too: it would not corrupt a total, but every lock here
waits and proceeds, and at that level the waiter fails where no caller retries.

A default changed under a running process turns into refused connections —
loud errors on the requests that need a new one — instead of silent second
spends; setting it back recovers without a restart. The check is one `SHOW` per
connection the pool opens and nothing per query.

Transactions that choose their level keep choosing it: the order and cart read
views still begin at REPEATABLE READ by name, and the payment repository still
names READ COMMITTED, which is what holds on a pool the guard did not build.
The guard lives in the published core/db, so a plugin's queries are covered,
and a pool built without core/db is not.

## Rejected

- **Naming the level at every BEGIN.** A single statement outside a transaction
  runs at the default too; the payment failure left under REPEATABLE READ was one.
- **Setting the level as a startup parameter.** It overrides the operator
  silently, and a connection pooler refuses it or, told to ignore it, drops it.
- **`SET` after connecting.** Session state a transaction-mode pooler does not keep.
- **A warning at startup, as the case-folding check gives.** ADR 0015 stops
  startup where continuing is silent, and money spent twice is.
- **Checking once at startup.** The default is read per session, for as long as
  the pool opens them.
