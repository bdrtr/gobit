# ADR 0430 — A write-off that races an open is the operator's to see

**Summary:** An open reads what was written off before it takes its order's lock, and a return parcel reads whether its return awaits goods before its write; both windows stay.
It costs a rare unit both shelved and boxed, a box that ships a written-off unit, or a parcel for goods already back, and keeps one connection per open.

- **Status:** Accepted
- **Date:** 2026-10-07
- **Amends:** [0420](0420-every-parcel-waits-for-its-orders-lock.md), whose two windows were left open

## Context

ADR 0409 and ADR 0420 hold a parcel, under its order's dispatch lock, to what
the order sold less what was written off less what its live parcels hold, and a
return parcel to what its return names. What was written off, and whether a
return still awaits goods, are the order module's answers, read before the
transaction, because asking another module while holding this one's locks
takes a second connection from the same pool (ADR 0130, ADR 0135). A write-off
committing between that read and the lock is seen by neither act, and a return
received or canceled between a return parcel's read and its write is not seen
by it. ADR 0420 recorded both and decided neither (D265).

## Decision

The two windows stay: an open reads its ceiling before its lock, and a return
parcel reads its return's state before its write. The operator sees both acts
on the order's page and corrects the rare collision by canceling the parcel.

## Consequences

- If the write-off's count runs first, a unit ends on the shelf and in the box;
  if the open's lock is granted first, the box ships a written-off unit. ADR
  0139's correction, canceling the box, puts the units right while it has not
  shipped.
- A return parcel opened for a return received or canceled in between carries
  goods already back or no longer asked back; canceling it is the correction.
- Each window is as wide as the open's wait for its order's lock, which another
  open's carrier call bounds; two acts on one order must meet inside it.
- An open keeps one pooled connection, and no read of another module runs
  under a held lock.
- What the order's returns and replacements speak for, read with the ceiling
  since [ADR 0423](0423-a-parcel-that-came-back-holds-only-what-a-return-or-a-replacement-speaks-for.md),
  shares the open's window: one written or withdrawn inside it is not seen
  (D268).
- Reopens when a cross-module read under a held lock has a measured bound on
  the pool, or an incident shows the windows met in practice.

## Rejected

- Reading the ceiling under the lock: a second connection under a held one, the exhaustion ADR 0130 measured.
- Taking the lock on a second connection before the read: two connections per open for the whole carrier call.
- Fulfillment keeping its own copy of what was written off under the lock: the copy arrives by event and narrows the window to the bus's delay instead of closing it.
- A version token from the order module checked under the lock: the same cross-module read.
