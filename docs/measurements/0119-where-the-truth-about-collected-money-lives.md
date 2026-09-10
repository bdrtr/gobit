# Where the truth about collected money lives — measured 2026-09-10

Serves [ADR 0119](../adr/0119-an-order-does-not-keep-a-second-copy-of-the-money.md).

An exchange's difference has been designed three times, by nine independent
agents, and six candidate designs were destroyed by six independent readings.
The candidates disagreed about almost everything and died of the same thing.
This file measures what that thing is.

Every population below is given with the command that sized it.

## What the six designs had in common

Each of them put a number about MONEY on the exchange's row — a settlement
stamp, a held balance, a collected total — and then held a rule against it with
a row-local CHECK, which is the shape migration 000017 argued for when it
bounded the completion: the rule "needs only this row, which is the whole reason
it can be one".

For a difference, that reason does not hold. The fact is not in the row. It is
in another module, and this one cannot see it change.

## The three facts that make a copy false

**A published route changes the number.** `POST /admin/v1/payments/{id}/refunds`
is bound in the payment module's admin surface. Its handler calls the refund and
returns; it tells nobody else.

**Nothing carries the change across.** The payment module emits no events at
all:

```
grep -rn 'Publish(' --include='*.go' internal/modules/payment/ | grep -v _test.go
grep -rn 'Topic' --include='*.go' internal/ core/ | grep -i payment
```

Both are empty. There is no payment topic, no subscriber on one, and no
reconciliation job that writes. ADR 0022 recorded the same absence when it
refused a subscriber on payment events for being unbuildable.

**A CHECK cannot reach.** A constraint on `order_exchanges` compares columns of
`order_exchanges`. A refund writes `payment_collections` and `payments`. The two
statements never meet, so the constraint cannot be falsified by the refund — it
simply never sees it, and goes on guarding a number that has stopped being true.

Put together: a mirrored amount on an order-side row is a claim that a published
route can silently invalidate, and the strongest guard the schema offers cannot
notice.

## The mirror that already exists, and what it costs today

This is not only a fact about a feature that has not been built.

`order_summaries` carries `paid_total` and `refunded_total`, which are the
order's copy of what the payment module collected and gave back.

```
grep -rn 'SetOrderSummaryTotals' --include='*.go' internal/ | grep -v _test.go
```

Two production callers, both in flows: the checkout's cart-clearing step and the
returns flow's refund. Neither is reached by the published refund route. So an
operator who refunds through that route moves real money and the order's copy
never learns: `refunded_total` stays low, the outstanding figure reads as more
settled than the order is, and the erasure sweep reads the same pair.

The surface that would learn about it explains its own design with a caller that
cannot exist. Its godoc says the side that knows the result is the checkout
workflow "or a subscriber listening to the payment events" — and the whole merge
semantics beneath it, at-least-once and order-independent, are justified by that
subscriber's delivery guarantees. There are no payment events. The mechanism is
sound; the reason given for it describes a feed that was never built.

The prose is corrected in this round. The staleness is not: closing it needs
either the payment module to publish, which is ADR 0022's own named trigger, or
a reconciliation that reads across, and both are decisions of their own. Gap
D55.

## What survived all three rounds

Worth recording, because whoever builds the difference starts from it rather
than re-deriving it.
Every one of these was reached independently by candidates working from
different premises, and confirmed by the readings that destroyed those
candidates on other grounds.

- **The link is `order_exchange_payment`, one to one.** One exchange, one
  collection. The database refuses the second binding through the link table's
  own unique index, which is the structural bar a lock would otherwise have to
  provide.
- **The predicate is `difference_due > 0`, not `<> 0`.** Migration 000017
  already refuses to complete an exchange whose difference is not zero, so any
  wording that opens a completion has to keep refusing the negative side. One
  character decides whether "a negative difference does not settle here" is a
  sentence or a mechanism.
- **The measure is an equality, not a floor.** A `>=` comparison is true for
  every negative difference against zero collected.
- **The ceiling is structural rather than arithmetic.** A collection opened for
  exactly the difference, bound one to one, cannot take more than the difference
  — the payment module already refuses a capture above the collection's amount,
  and that amount is written once.
- **The down migration rewrites rather than refuses.** A raise inside a down
  marks the migration ledger dirty, and the server migrates on every startup.
  Migration 000017 set the test: this state is reachable by code, so finding one
  is not evidence of a hand-written row.
- **A signature is not widened; a method is added.** Go's structural typing
  means a new method on the payment interop breaks no consumer, while changing
  `Collection`'s return list breaks every one that declares it.
- **ADR 0118's capacity rule does not answer a withdrawal.** Capacity asks what
  is left to take and a refund does not raise it; a withdrawal asks whether the
  record still holds the customer's money, and a refund does zero that. Reading
  the first where the second belongs is what produced a permanent lock in an
  earlier round: a record that could never be withdrawn and an order that could
  never be forgotten.

## The knot this record does not untie

The withdrawal guard. The rule wants to be "refuse while this exchange still
holds the customer's money", and that is a question only the payment module can
answer. The order module may not ask it — the two modules do not know each other
— and the withdrawal's published route is on the order module.

Three shapes were visible and none was measured well enough to choose here:
move the route to a flow, which loses today's exit on an installation that binds
no flow; guard on the recorded moment and add a verb that clears it, which is
the reopen shape ADR 0055 rejected; or let the flow refuse before the module is
reached, which leaves the module's own route unguarded.

Choosing needs its own measurement, and it belongs to the record that builds
the difference.
