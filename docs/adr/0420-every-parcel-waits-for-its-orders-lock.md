# ADR 0420 — Every parcel waits for its order's lock

**Summary:** A parcel bringing a return back takes its order's dispatch lock and is held to what its return names less what its live parcels hold, counted under it.
The cancellation flows count an order's outgoing parcels by reference under the same lock, without waiting for it; it costs the bus consumer a bounded wait.

- **Status:** Accepted; amended by [0423](0423-a-parcel-that-came-back-holds-only-what-a-return-or-a-replacement-speaks-for.md), whose count holds a parcel that came back only as far as a return or a replacement speaks for it and whose cancellation flow recounts a parcel that comes back
- **Date:** 2026-10-06
- **Amends:** [0409](0409-an-orders-parcel-holds-the-units-it-ships.md), whose lock only outgoing parcels took, [0384](0384-a-return-parcel-brings-back-the-return-it-names.md), whose return's parcels were counted before the transaction, and [0240](0240-a-failing-handler-is-called-again.md) and [0273](0273-the-bus-keeps-the-message-it-gives-up-on.md), whose Redis bus counted a handler failing during its shutdown as processed

## Context

ADR 0409 holds an outgoing parcel to its order under the order's advisory
lock, counting the order's live outgoing parcels by the reference this module
stores. A parcel bringing a return back (ADR 0384) still counted its return's
parcels before its transaction and took no lock, so two of them opened at once
could together carry more than the return named (D265). The cancellation flows
found the order's parcels through the order's link: a write-off read while a
parcel's provider call was in flight, or one of a parcel whose link was not
written, put back units that parcel held (D264, D265).

## Decision

Every parcel takes its order's dispatch lock inside its transaction, and a
return parcel may hold, per line, what its return names less what that return's
live parcels hold, counted under the lock. The cancellation flows count the
order's live outgoing parcels by reference under the same lock, which the count
takes only if it is free, answering a lock in use with a retryable refusal the
flow asks again after, for a bounded time and holding no connection.

## Consequences

- A return parcel's check is a target, like an outgoing one's. What the return
  names and whether it awaits its goods are still read before the transaction;
  only what its parcels hold moved under the lock.
- Only the return's own parcels count; another return of the same order names
  its own units. One order's opens, outgoing and coming back, wait for each
  other.
- The operator's write-off or box cancel returns at once; the bus consumer
  that restocks is what waits. While a parcel of the order is being opened,
  its carrier call included, `HeldForReferenceLocked` answers an unavailable
  fault, `fulfillment_dispatch_busy`, and the flow pauses and asks again. Its
  pauses are derived from the bus's own figures: each of the bus's three calls
  may pause about 14.5 s, so a busy delivery ends within about 45 s, below the
  Redis takeover idle time; ADR 0240's refusal of minutes-long waits in a
  handler holds. On Redis the stream's later messages wait behind it.
- A delivery that still meets a busy lock is logged as failed and not stored.
  Both topics are outbox topics, so the relay delivers the event once more
  within about a minute, and the restock is a target the line's next act
  brings the shelf up to (ADR 0142); units stay off the shelf only when both
  deliveries meet a busy lock and nothing later touches the line.
- The flow is registered in the container after the bus and shut down before
  it, which ends its pauses. On Redis a handler that still fails once the
  bus's shutdown has begun leaves its message pending for the next process,
  for every handler; the in-memory bus has no pending list, and only the
  relay's later delivery recovers such an event.
- A write-off counts a parcel whose link was not written, and an outgoing
  parcel's cancel acts on its reference when the link names no order, so ADR
  0135's write-off-then-cancel path ends with the units on the shelf. The
  cancel event names the return a parcel was bringing back, whose cancel
  puts nothing back.
- An open reads what was written off before it waits for the lock, and the
  wait can last another open's carrier call, a return parcel's included. A
  write-off committing in that window is seen by neither: if the write-off's
  count runs first, a unit ends both on the shelf and in the box; if the
  open's lock is granted first, the box ships units that were written off
  (D265 Open).
- A return received or canceled between the parcel's read of whether it awaits
  goods and its write can still get a parcel.
- A returned parcel still counts as gone to the cancellation flows. The lock is
  ADR 0409's class; no new class.

## Rejected

- A lock per return: a return parcel's reference is already its order, and one key serialises both directions.
- Waiting for the lock in the count: a bus consumer would hold a pooled connection for a carrier's call, and an order of many lines would take the pool.
- Handing a busy count straight to the bus: its three calls end within a second and a quarter, shorter than a carrier call.
- Pausing for minutes: on Redis another process takes a message over that outlasts the idle time, which ADR 0240 refused.
- Reading what the order wrote off, or whether the return awaits goods, inside the transaction: a second connection from the same pool, which ADR 0135 refuses.
- Refusing a write-off of units a live parcel holds: ADR 0139 lets the box be canceled after.
