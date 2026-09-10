# A counter in place of a balance — measured 2026-09-10

Serves [ADR 0118](../adr/0118-a-collections-gate-reads-what-is-left-to-take.md).

ADR 0117 deferred an exchange's difference and named what stood in the way: a
payment collection cannot be abandoned once it has taken anything. This file
measures that claim. The mechanism is real and two of the conclusions drawn
from it were not — including one written into ADR 0117 itself.

Every population below is given with the command that sized it.

## The rule, and the arithmetic that was not under it

Opening a session refused any collection whose captured total was above zero.
Beneath that flag, the remainder calculation subtracted only what live sessions
reserved:

```
reserved := LiveSessionAmount(collection)
if reserved >= collection.Amount { return 0 }
return collection.Amount - reserved
```

The captured total appeared nowhere in it. So the flag was not a shortcut for
the sum — it was the ONLY thing standing between a second session and a second
charge, and being the only thing is why it had to be blunt.

Two consequences follow, and the second is what makes the flag wrong rather
than merely coarse.

**The rule sat on session OPENING, not on money.** `AuthorizePayment` and
`CapturePayment` never read the captured total. A session opened before the
first capture could still be authorized and captured afterwards. So a
collection was never "closed"; its payability depended on when its sessions
were opened.

**A partial capture could never be finished.** The remainder was unreachable by
any route: the flag refused a new session, no query raises `amount`, and the
module has no delete.

## The stuck path is reachable from a published endpoint

```
grep -rn 'decodeOptionalAmount' --include='*.go' internal/modules/payment/
```

One handler uses it, and it is the capture. The admin capture endpoint takes an
optional amount; the service defaults it to the authorized total when absent and
rejects it only when it is ABOVE the hold. There is no lower bound, so taking
part of a hold is an ordinary call rather than an abuse.

The same state arrives with nobody choosing it. The provider contract permits a
partial authorization, and the derived status vocabulary has carried a name for
the result — a partially captured collection — since it was written. The
module's integration suite already produces one against a real provider.

And the module contradicted its own written promise. Its session test states
that a canceled session must not lock a collection for ever, because the
customer has to be able to try another payment method. That promise held only
while nothing had been captured.

ADR 0042 recorded the blockage three days before this record, in the same words
this file measures: it says the settlement session would be refused today, and
names the remainder calculation as the thing that would have to learn the sum.

## Why the cheapest patch is the dangerous one

Changing the flag to read the NET capture — captured less refunded — looks like
the one-line fix. It breaks three ways, and none of them is visible on the line
being changed.

**It takes money before it fails.** A fully refunded collection would pass a net
flag, and the remainder calculation — which does not know about captures —
would report the whole amount as available. A session opens, the provider is
called, the money moves, and only then does the write of `captured + captured`
meet the constraint that keeps the captured total under the amount. The
transaction rolls back. The money does not.

**It makes the derived status lie.** The status is computed from the money
before it is computed from the session counts, so a reopened collection reads as
refunded while a live session waits under it.

**It does not open the path that is actually stuck.** A partial capture has
captured above zero and refunded at zero, so a net flag refuses it exactly as
the old one did.

The reading that works is neither the counter nor the net: it is the remaining
CAPACITY — the amount, less the captured total, less what live sessions reserve.

## Two claims this measurement refuted

**The erasure sweep is not this record's consumer.** A partially captured order
would be held open for ever by the outstanding-balance branch, and that is true
in the abstract. It is not reachable: the checkout saga refuses a capture short
of its plan and compensates instead, the order's total is never rewritten, the
service method that would adjust the paid total has no route, and a collection
opened for an order is captured in full by the saga. Nothing bound produces an
order that owes money and is neither pending nor canceled.

**A fully refunded collection has no reader waiting.** ADR 0117's named trigger
was a collection that can be reused "once it owes nothing".

```
grep -rn 'RefundCollection(' --include='*.go' internal/ plugins/ | grep -v _test.go
```

Two production callers, both in the returns flow — a refund and a claim
settlement. Both only send money back; neither collects again afterwards. So
the capability ADR 0117 asked for cannot be published today, and this record
deliberately does not publish it: a refund leaves the captured total alone, so
the remaining capacity stays zero and the collection stays closed.

## A correction to ADR 0117

ADR 0117 says the blocker for an exchange difference is not the link but the
collection, "it cannot be abandoned once it has taken anything". The first half
is right and the second names the wrong property.

Measured: even a reusable collection could not take the difference. Its `amount`
is written once — no query in the module updates it — so the difference has no
room inside it, and a capture beyond the amount fails on the constraint after
the provider has moved the money. What a difference needs is a SECOND
collection, under a link name of its own, which is the thing ADR 0117 decided
and deliberately did not name.

The correction is recorded here rather than by editing that record, which is the
rule for a decision that has already been accepted.

## A reason written for a behavior that never shipped

The collection refund's godoc said captures are drawn oldest first, called that
deliberate rather than arbitrary, and built a justification on it: the remainder
would stay concentrated on the most recent captures.

```
grep -n 'ORDER BY' internal/modules/payment/queries/payments.sql
```

`ListPaymentsByCollection` has ordered `created_at DESC, id DESC` since it was
written, and the planner walks that list as it comes — newest first. The prose
is corrected to the shipped behavior rather than the behavior to the prose,
because which order is better is a real question with no measurement behind it:
a provider that only refunds within a window of the capture would prefer the
oldest drawn first, so the remainder does not age out. Changing the order is a
behavior change nobody has asked for, and its trigger is the first provider
that reports a closed refund window. Gap D54.

## What the mutation proof needed

Both new gates were proved by mutation with `-count=1`, and the interesting part
is what the EXISTING tests could not see.

The suite already had a test for a captured collection refusing a new session.
It captures the FULL amount, so it stays green under the new rule as well —
correctly, because a fully captured collection has no remaining capacity. That
is why it could not distinguish the counter from the balance, and why the new
tests capture a QUARTER.

Two mutations, two different failures. Removing the captured total from the
capacity sum turns three tests red, including the one asserting that a fully
refunded collection stays closed. Putting the old flag back turns only the
partial-remainder tests red. A gate that survives both is not a gate.
