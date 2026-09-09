# ADR 0090 — A replacement is sent, and the ledger says nobody paid for it

**Summary:** The goods a claim promised leave the warehouse in a parcel, and the
movement that takes them out carries a reason of its own.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

ADR 0089 gave a claim a way to say WHAT to send. Nothing sent it: the settle
verb refused a claim of that kind, and that refusal was the only thing in the
framework that mentioned a replacement at all.

Sending reaches three modules — units come out of inventory's count, a parcel is
the fulfillment module's, and the record is the order module's — so it is a
flow (ADR 0006). What the flow needed and did not have was a way to say that
units left WITHOUT being sold. The ledger's reasons had four words and none of
them was true of these goods: nobody paid for them.

## Decision

`internal/workflows/returns.DispatchReplacement` sets the units aside, opens a
parcel on the order, takes the units out of the count and records all three;
the claim is settled by the goods rather than by money.

A reservation now carries a PURPOSE, and the confirm writes the movement's
reason from it. Stock set aside for a replacement leaves as a `replacement`, and
the ledger keeps it apart from a sale.

## Consequences

Recovery goes FORWARD. Each step leaves a record the next attempt reads — the
promise sits on the replacement's line, the parcel's idempotency key is derived
from the record's id, and the confirm is idempotent — so a dispatch that dies is
finished by running it again rather than by undoing what it did. The confirm is
last because it is the irreversible half: between units held for goods that
never shipped and units gone with nothing carrying them, the first is the one a
person can fix.

The replacement gains a third status, `dispatched`, written by the code path
that now exists. A dispatched record cannot be withdrawn, and the goods it sent
still count against what was bought on the line.

The composition root now has an ORDER: the fulfilling flow is provided before
the return flow, because the second resolves the first by name. A flow that
depends on another flow is new here, and this is the dependency edge.

`inventory_reservations` gains a column with a default, and the equivalence
"units that leave against a promise name the promise" widens from one reason to
two. Every reservation written before the column was a checkout's, so the
default is what those rows really were rather than a convenience.

Measurement: none. What decided the shape was the failure a step leaves behind,
not a number.

## Rejected

**A saga with compensations.** The last step has none: a confirmed reservation
cannot be released, by the inventory module's own rule. A chain whose final
compensation does not exist is a chain that cannot roll back, and pretending
otherwise would have put the pretence in the engine.

**Deducting the stock with no reservation.** Simpler by one call and not
retryable: a second attempt would take the same units out again, because nothing
would record that the first had.

**A reason passed to the confirm.** The confirm is reached from a saga, from a
retry and from the recovery path. On the third the caller does not know what the
units were for, and a reason it guesses is the one the ledger keeps.

**Recording it as a sale.** The arithmetic is identical and the fact is not. An
operator reconciling a month of stock against a month of revenue would find a
gap with no name.

**Opening the parcel through the fulfillment module.** The order-to-shipment
binding is the flow's fact, and going around it would also skip the refusal a
canceled parcel earns (ADR 0088).
