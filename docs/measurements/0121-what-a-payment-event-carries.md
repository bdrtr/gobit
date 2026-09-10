# What a payment event carries — measured 2026-09-10

Serves [ADR 0121](../adr/0121-the-payment-module-says-when-money-moved.md).

ADR 0022 refused a subscriber on payment events for one reason — there were none
— and left the question that had to be answered before there could be: what does
a payment event carry. This file answers it, and records what the measuring
found that the question did not ask for.

## The population of moments is two, and it was counted

A collection's money lives in three columns and exactly one function writes
them. That function has six production callers:

```
grep -rn 'writeCollectionTotals(' --include='*.go' internal/modules/payment/ | grep -v _test.go
```

Four of them move the AUTHORIZED figure alone — opening a session, authorizing,
cancelling, and the reconciliation's hold arithmetic — and an order's summary
has no column for a hold. Two move the pair an order keeps: the capture and the
refund.

So the topic count is two, and it is derived rather than chosen. A third for the
authorization is refused by a record that already exists: ADR 0054 fixed the
money-moment vocabulary at two, because no money moves at a hold.

## The payload: an identifier, and the three reasons for no amount

**A refund is not idempotent, on purpose.** Its own godoc says two calls for ten
units are a real refund of twenty. An amount in the payload would therefore be
an INCREMENT of an unknown base, and the bus delivers at least once — a
redelivered increment reports a total that never existed.

**The consumer's write is a merge that only works on cumulative figures.** The
order's summary keeps the LARGER of what it holds and what it is told, which is
idempotent and order-independent for a lifetime total and silently wrong for an
increment: a late delivery reporting less is ignored with one debug line. A
payload without an amount makes the wrong shape unwritable rather than merely
discouraged.

**Every published topic is forwarded out of the installation.** The
outgoing-webhook plugin's topic list is not a preference — a gate fails in both
directions, so a topic published and not forwarded is a build failure. Whatever
a payment event carries goes to whichever endpoints an operator registered. The
plugin redacts exactly one field today, and adding money to the payload would
have meant adding a redaction rule for it; an identifier needs none.

What is left is the collection's id and the moment. The order module's own event
discipline is the same sentence — carry an identifier, read the record — and it
was written for the same reason: a durable stream is a bad place for anything a
reader can look up.

## The event id is derived from the ROW, not the collection

The outbox writes with `ON CONFLICT (id) DO NOTHING`, which makes a duplicate id
silent rather than loud. A collection moves money many times, so an id keyed on
it would make the second refund look like a redelivery of the first and drop it
without a word. The id is keyed on the capture or the refund row instead.

Being derived rather than random is what makes the outbox row and the direct
publish ONE event rather than two: both carry it, and a subscriber idempotent on
the event id — which the bus's contract already requires — cannot tell the
deliveries apart.

## The subscriber cannot use the collection's reference

The payment module does not know which cart or order a collection belongs to,
and says so in its own package documentation. What the collection carries is a
`reference` the caller wrote and this module never validates — and the checkout
writes the CART's identifier there.

```
grep -n 'CreateCollection' internal/workflows/checkout/authorize_payment.go
```

The published OpenAPI description of that query parameter said the reference is
"an order id in practice". It is not, it never was, and a reader who filtered on
it would find carts and miss orders. Corrected in the same commit.

The order is reached the only way it can be: BACKWARDS over `order_payment`,
which is the query layer's own contract — with the root entity on the To end of
a link the expansion goes the other way, and a one-to-one link puts exactly one
record on the other side. That path needed one thing the payment module did not
offer: its query provider accepted a filter on `reference` and on `status` and
refused everything else, including the collection's own id. Routed to the fetch
the joining already uses, so the two paths cannot answer differently.

## What the measuring found that the question did not ask for

**The refund route was not the only silent one.** Gap D55 named
`POST /admin/v1/payments/{id}/refunds`. The capture route is its sibling — a
live admin route that raises the captured total with no flow on the path — and
it was missed because the row was written from the route somebody was looking at
rather than from the population of routes that can produce the defect. It is
gap D57 and the same subscriber closes it, because the subscriber reads the
collection rather than believing the event.

**ADR 0119's own prose half was not closed either.** That record said it had
corrected the sentence claiming a payment-event subscriber feeds the order's
summary. It corrected one of three: the same claim stood two paragraphs below,
in the indicative, and again on the input type. Both were the stated REASON for
the merge semantics, so the passage read as an explanation of a live mechanism
rather than of a shape kept for a caller nobody had written. Corrected before
this record was built on top of it.

## The gate that decides where a publisher may live

The topic census resolves a published topic from `Publish` CALL SITES and skips
the relay, by its own admission. So a module that wrote only to the outbox and
never published directly would be seen as publishing nothing, and its
subscriber would fail the gate that requires every subscribed topic to have a
publisher. The order module survives that today only because it does both.

This is why the payment module does both as well, and the shape is not a
preference: the outbox row is the guarantee and the direct publish is the speed,
and either alone fails something real.

## What the tests found after the decision was made

**The first end-to-end test passed under the mutation that breaks it.** The
backward expansion needs a filter on the collection's own id, and the test that
was written to prove the whole chain stayed green with that filter removed. The
reason is that the collection listing falls back to `ORDER BY created_at DESC,
id DESC` with no filter at all, and a database holding ONE collection answers
the unfiltered question correctly by accident.

The test now creates a NEWER collection bound to no order before the refund —
the shape an exchange's difference has, and a cart that never became one. An
unfiltered read is handed that row, finds no order behind it and stays silent,
so the mutation fails. Removing the subscription fails it too. Both were run
with `-count=1`.

This is worth writing down for what it says about the test rather than the code:
a chain test on a shared database can be discriminating in the full suite and
blind on its own, and the direction it fails in is the dangerous one.

**The payment module's integration harness had no outbox table.** Writing to
`event_outbox` inside the module's transaction made eleven of its integration
tests fail with `relation "event_outbox" does not exist`. The table is a CORE
schema owned by `core/eventbus/outbox` and the module's own migrations must not
create it, so the harness applies it the way the composition root does — which
is what the order module's harness already did, for the same reason.

**The bus requirement is proved at the module boundary, not the service's.**
Both refuse, but only the module's error names `core.eventbus`. The test asserts
the name, because a startup failure that does not say which service is missing
leaves an operator with a healthy-looking installation and no next step.
