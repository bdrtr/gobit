# The guard moves to the binding — measured 2026-09-10

Serves [ADR 0120](../adr/0120-an-exchange-can-take-its-difference.md).

An exchange's difference was designed three times before this record and six
candidate designs were destroyed. This file measures the question the last of
them left standing — where a withdrawal guard goes — and records what the
earlier rounds settled, so the next reader does not derive it again.

## The question, and why every answer at the withdrawal fails

The rule wants to be "refuse a withdrawal while this exchange still holds the
customer's money". Only the payment module can answer that. The order module may
not ask it, and the withdrawal's published route is the order module's.

Five shapes were measured, one of them by an agent that had not been shown the
other four.

**Move the route to a flow.** The objection everyone reached for — an
installation that binds no flow loses today's exit — turned out to be FALSE, and
that is worth recording because it was believed twice: the framework forbids
removing a module, the composition root is closed to embedders, and the workflow
registration is unconditional. What kills this shape is different and was not
obvious: with one collection per order, `captured − refunded > 0` is true of
every paid order, so the guard would refuse the withdrawal of every exchange on
a paid order. Nothing distinguishes the sale's money from the difference's until
the exchange names its own collection.

**A stamp and a verb that clears it.** The stamp is a counter and the guard needs
a balance. A refund made through the payment module's own route leaves the stamp
standing, so the record that holds nothing stays locked — the same trap one
column over. Clearing a moment is the reopen ADR 0055 refused, and no business
record in this tree clears an it-happened stamp; the only clearers are
infrastructure retry cursors.

**Guard in the flow, leave the module's route open.** There is exactly one door
today: the admin route into the service. A guard on a second door nobody walks
never fires, which is D17's shape. Worse, the unguarded door does not merely
fail to refuse — a withdrawal silences the erasure branch, and the order becomes
erasable while money is held.

**No guard at all.** Its premise is that the bound collection names the money.
That premise is only true once the record carries the identifier, and its cost
is that a withdrawal makes the order forgettable while the money is still held.

**The fifth shape: guard the BINDING instead.** The moment the exchange takes
the money it leaves `requested`, and the transition table says what that means.
It is the shape the return already uses, where a received return refuses a
cancel: something arrived that has to leave again before the request can be
taken back. Every objection above dissolves — a flow-less installation cannot
reach the state, the exit is forward rather than a cleared moment, and the money
question is asked once, by the side that may ask it.

## Why the record keeps an identifier and not an amount

ADR 0119 forbids the amount. What made a column the right carrier for the
identifier is a precedent with its reason already written: migration 000012
says of the replacement's parcel and its items' reservations that "neither
carries a foreign key (Principle 2.2), and both are written by the flow that
holds both sides".

The consequence decides the shape. A CHECK can read a column and cannot read a
binding recorded through the link layer, so only a column keeps the completion's
bound where migration 000017 put it — which was the fifth shape's stated
condition. This is why ADR 0120 supersedes ADR 0117's second sentence: that
record named a link when a link was the shape in view, and the measurement found
the tree's own shape for this kind of identifier.

## The hole the new status would have opened

The erasure sweep reads `status = 'requested'` to decide that an exchange still
holds the order open. A funded exchange is not `requested`, so introducing the
status without widening that read would have made an order with money held
FORGETTABLE — a hole the three-word vocabulary did not have. The branch now
reads both.

This is the shape of defect this repository keeps producing: a capability moves,
and a rule written against the old vocabulary does not follow it. D52 and D56
are the same shape in the same module, one round apart.

## What the earlier rounds settled

Reached independently by candidates working from different premises, and
confirmed by the readings that destroyed those candidates on other grounds.

- **The predicate is `difference_due > 0`, never `<> 0`.** Migration 000017
  already refuses to complete an exchange whose difference is not zero, so any
  wording that opens a completion has to keep refusing the negative side. One
  character decides whether "a negative difference does not settle here" is a
  sentence or a mechanism.
- **A floor is true for every negative difference against zero collected.** It
  is the fault that killed three designs, in each of which the record's own cost
  section claimed the opposite. In THIS design the sign is refused before any
  comparison runs, so the lesson is why the sign check exists rather than a
  property of the comparison — and the comparison being an equality is measured
  NOT to be load-bearing here: with the collection's amount pinned to the
  difference and the payment module capping a capture at that amount, what is
  held can never exceed it, so a floor and an equality are the same predicate.
  Turning it into a floor breaks no test, and the honest reading is that the two
  checks answer different questions: the amount is the CEILING on what can ever
  be taken, and the held total is whether the money is there NOW.
- **What is held is `captured − refunded`, not `captured`.** A collection
  captured and refunded in full holds nothing, and a rule reading the capture
  alone calls that funded.
- **The ceiling is structural.** A collection opened for exactly the difference
  and bound one to one cannot take more: the payment module refuses a capture
  above the collection's amount, and that amount is written once.
- **The down migration rewrites rather than refuses.** A raise inside a down
  marks the migration ledger dirty and the server migrates on every startup;
  migration 000017 set the test — this state is reachable by code, so finding
  one is not evidence of a hand-written row.
- **A method is added; a signature is not widened.** Go's structural typing
  means a new method on the payment interop breaks no consumer, while changing
  `Collection`'s return list breaks every one that declares it, the checkout
  saga included.
- **ADR 0118's capacity rule does not answer a withdrawal.** Capacity asks what
  is left to take and a refund does not raise it; a withdrawal asks whether the
  record still holds the customer's money, and a refund does zero that.

## What this record does not build

Nothing opens the collection for the operator. The payment module already
publishes every verb needed to open one, put a session on it, authorize and
capture, and this framework adds no wrapper around them: the money is moved by
the operator through those endpoints, and what is recorded here is which
collection answered.
