# ADR 0139 — A canceled parcel gives its units back

**Summary:** The fulfillment module publishes its first event when a parcel is
canceled, and the cancellation flow puts back the stock that parcel had been
holding. It costs one more topic on every operator's webhook endpoints, and it
closes a hole whose only known workaround was the thing that opened it.

- **Status:** Accepted, amended by [0142](0142-both-acts-compute-the-same-target.md)
- **Date:** 2026-09-11

## Context

How many of a line's units belong on the shelf is
`min(canceled, bought − in a live parcel)` (ADR 0134). Both sides of that move. A
write-off grows the left one and the flow hears it as `order.line_canceled`;
nothing told it about the right one, because the fulfillment module published no
events at all.

So a line canceled under an open parcel put back only the units outside the box,
which is right while the box is going out. ADR 0135 looked at that state, declined
to withdraw somebody's shipment on the framework's own authority, and named the
shop's resolution in the same breath: cancel the parcel. Canceling it flipped a
status; the units it held were already counted as gone, nothing recomputed
anything, and they ended up neither sold, nor shipped, nor stock.

The recommended cure lost the goods, and it lost them silently: no error, no log
above debug, and a stock figure that is simply short. Gap D75.

Measurement: [measurements/0139](../measurements/0139-what-a-canceled-parcel-releases.md)

## Decision

The fulfillment module publishes `fulfillment.canceled` — an outbox row inside the
cancelling transaction and a direct publish after it — and the cancellation flow
subscribes to it as its second event. For each line the parcel held, the flow puts
back the difference between what is owed to the shelf now and what was owed while
the parcel still counted.

## Consequences

The amount released is a difference of two STATES rather than of records, so the
flow keeps no memory of what earlier acts returned: `after` is
`min(canceled, bought − committed)` and `before` is the same with this parcel's
units added back to `committed`. Two parcels canceled in either order put back the
same total, and a redelivered event computes the same number and writes it under
the same reference, which the inventory ledger refuses the second time.

The reference is the PARCEL and the LINE, not a cancellation id, because this act
is not a cancellation: several write-offs can share one parcel and one parcel can
release several lines. The pair is what happens once.

A parcel whose line nobody wrote off releases nothing, and the distinction is the
point — its units become dispatchable again, not sellable again. Putting them on
the shelf would sell the same goods twice.

Three costs. Every operator's endpoints now receive one more topic, which was not
this repository's choice: an unforwarded published topic is a build failure in both
directions. The module gained an outbox hand and a bus dependency, so a
cancellation can now fail because a row would not write — deliberately, since that
is the state this record exists to end. And the subscriber needs a read answering
for a CANCELED parcel, which `CommittedQuantities` refuses, so two similar reads
now sit beside each other and their difference has to stay understood.

Two of ten mutations survived the first pass and they were opposite mistakes: a
guard nothing could make fail, deleted because it claimed the arithmetic missed a
case it covers, and a clamp every fixture happened to satisfy, now tested.

## Rejected

- **Withdraw the units from the open parcel when the line is canceled.** ADR 0135
  refused it and the refusal still holds: a pending parcel is one the warehouse is
  picking, and pulling units out of a box is not a database write.
- **Refuse to cancel a line while a live parcel holds it.** It would make the shop
  cancel the whole parcel to write off one unit, losing the picking work on every
  other line.
- **Carry the item quantities in the event.** An event is a flat map of strings; a
  list would travel as JSON inside one value and be parsed against a schema
  nothing checks. Identities cross and the subscriber asks.
- **Recompute from the inventory ledger's history.** It would make the flow read
  another module's movements to learn what it had itself done.
