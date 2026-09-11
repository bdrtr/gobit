# ADR 0140 — A parcel records which order it is for

**Summary:** The fulfillment module writes the `order_fulfillment` binding itself,
on every parcel it opens. It costs a link write on a path that did not have one,
and it makes three earlier decisions true that were describing a subtraction
which never subtracted.

- **Status:** Accepted
- **Date:** 2026-09-11

## Context

A parcel can be opened two ways. The fulfilling flow opens one for a whole order
and wrote the binding afterwards; the module's own admin endpoint opens one with
an ITEM BREAKDOWN and wrote nothing. Those are the only two, measured from the
call sites of `CreateFulfillment`.

The item breakdown is what matters. `CommittedQuantities` sums a parcel's ITEMS,
and the flow-opened parcel has none — the surface that opens it does not take
them. So one kind of parcel carried units and no binding, the other a binding and
no units, and every question asked through the link answered zero either way.

That zero is the middle term of three records. ADR 0134 puts back
`min(canceled, bought − committed)`, so a write-off returned units sitting in a
box. ADR 0135 bounds a parcel by `bought − canceled − committed`, so a second
parcel could hold units the first already held. ADR 0139 releases what a canceled
parcel held, and found no order for it at all.

A probe against a real database settled it: a parcel opened with three units of a
line reports `{oli: 3}` to `CommittedQuantities` and an EMPTY list to
`order_fulfillment`. Gap D76.

Measurement: [measurements/0140](../measurements/0140-which-parcels-were-bound.md)

## Decision

`Service.CreateFulfillment` writes the `order_fulfillment` binding from the
reference it was given, after its transaction commits, on every call including one
that returned an existing parcel. The fulfilling flow no longer writes it.

## Consequences

The module owns the definition, and the rule that put it there is that a link
belongs to the side holding the record it carries. The owner declared it and
somebody else wrote it, which is how one of the two writers came to be missing;
the rule and the write are now in one place.

Writing it from `reference` treats that field as an order identifier, and this
module's record has always said it never validates it. ADR 0135 already took that
leap, handing the same field to the flow as an order id; what changes is that the
leap is now visible in a table.

The write is after the transaction: the link service owns its own pool and keeps
its transaction under a key this module cannot see. So a parcel can commit and its
binding fail, and that failure FAILS the request with the parcel's identifier in
the message — an operator who reads only "it failed" presses again with a fresh
key and opens a second one. A retry writes the binding again and repairs the
half-finished case; the link service is idempotent, so always writing costs one
statement.

The flow's "already open" answer is computed from what was bound BEFORE the call,
so it now depends on the module having bound it. The flow's own fake did not, and
its test caught the move in exactly that shape: a fake disagreeing with its
producer about the one fact the caller reads back.

Twelve mutations across this record and ADR 0139 all bit, the two that matter here
being: binding only a NEW parcel, and swallowing the binding error.

## Rejected

- **Have the flow open the item-carrying parcels too.** The admin endpoint is
  published (ADR 0026) and integrators call it; moving it would break them for a
  reason that has nothing to do with them.
- **Keep both writers.** The same rule in two places, and the second write a
  no-op — which is how the first one came to be forgotten on one path.
- **Write the binding inside the transaction.** The link service cannot see this
  module's transaction, and giving it a reaching hand would put a second module's
  writes inside this module's locks.
- **Derive the order from the reference at read time.** Every reader would then be
  reading a field the module never validates.
