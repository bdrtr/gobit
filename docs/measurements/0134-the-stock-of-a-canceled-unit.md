# The stock of a canceled unit — measured 2026-09-11

Serves [ADR 0134](../adr/0134-canceled-units-go-back-on-the-shelf.md).

## The defect, measured before it was fixed

The feature list called this "a canceled unit leaves no reservation". It is worse
than that sentence and in a different place, and the difference decided the whole
design.

| Question | Answer, read off the tree |
|---|---|
| what does the checkout do with the reservation? | `clear_cart.go` calls `ConfirmReservation`, which DEDUCTS the stock and consumes the reservation |
| so what does an order hold? | deducted stock and no reservation — there is nothing to release |
| does `CancelOrder` touch stock? | no, and the order module cannot (Principle 2.1/2.4) |
| does the partial cancellation? | no |
| is there an `order.canceled` EVENT? | no. `order.canceled` is a TIMELINE kind, so there was nothing to subscribe to |
| who calls `Restock`? | the returns flow, once, on physical receipt — and its godoc says two calls mean two arrivals |

So the gap is not a reservation left behind. It is that every unit written off stays
deducted forever, and the whole-order cancellation has the same hole as the partial
one the list named. Gap D70.

## What decided the shape

**The endpoints could not move.** ADR 0113 published two admin routes. Moving them
into a flow breaks a promise (ADR 0026) for a reason integrators had no part in, and
a second route beside them leaves a shop with two ways to cancel, one of which
quietly loses stock. So the order publishes and something else listens — which the
user chose when the fork was put to them.

**`RegisterGuestCustomer`'s twin problem.** The read a listener needs — how many of
this line already shipped — did not exist. The fulfillment module records it in
`fulfillment_items(line_item_id, quantity)`, the query layer does not expose it, and
the shipment provider's field map is shipment-level. So one new cross-module read,
with the rule about WHICH parcels count inside the module that owns them.

**The location could not be derived.** Units have to go back to the shelf they left.
The reservation knew it and reservations are keyed to the CART's line item — stated
in `inventory/migrations/000001` — and an order carries no cart line. Three hops
through three modules would have answered it; one column on the sale movement
answers it in one read, and that column is also what makes the return idempotent.

## The arithmetic, and why it needs no memory of past restocks

	window = bought − committed
	return = min(canceledBefore + now, window) − min(canceledBefore, window)

`committed` is the units a live parcel holds. The total ever returned is therefore
`min(canceledTotal, window)` however the write-offs were split, so a second
cancellation cannot re-return what the first did and nothing has to record what was
restocked. A line whose units all shipped gives a window of zero: those goods are
with the customer and writing them off is a money act.

The table the flow's test drives:

| bought | in a parcel | canceled before | canceling now | goes back |
|---|---|---|---|---|
| 5 | 0 | 0 | 2 | 2 |
| 5 | 5 | 0 | 2 | 0 |
| 5 | 3 | 0 | 2 | 2 |
| 5 | 3 | 0 | 3 | 2 |
| 5 | 0 | 2 | 2 | 2 |
| 5 | 3 | 2 | 2 | 0 |
| 2 | 3 | 0 | 1 | 0 |

The last row is a parcel holding more than the line sold, which should not happen and
costs nothing to answer: a negative window returns nothing rather than subtracting.

## Idempotence, and the two halves that cannot drift apart

Adding stock is deliberately not idempotent everywhere else in the inventory module —
`Restock`'s own record says two calls mean two arrivals. A bus that delivers at least
once therefore needed the opposite guarantee here, and it is the DATABASE's: the
cancellation's id is the movement's reference and a partial unique index holds it.

Measured while mutation-proving: dropping the unique index does not quietly lose
idempotence, it breaks EVERY movement write with "there is no unique or exclusion
constraint matching the ON CONFLICT specification". The index and the conflict clause
cannot drift apart, because the statement refuses to run without the index. That is
worth more than a comment tying them together.

## The new topic IS forwarded, and the decision was not mine to make

The first draft of this file said the opposite, with reasoning: `plugins/webhookout`
keeps a curated list, its visible gate is one-directional — a receiver may not
register for a topic gobit does not publish — and forwarding a new topic changes what
an operator's third-party endpoints receive, which is an outward-surface decision
rather than a stock one.

Every clause of that is true and the conclusion was wrong.
`TestTheForwardedTopicsAreEveryPublishedTopic` exists and fails in BOTH directions: a
published topic missing from the list, and a forwarded topic nobody publishes. The
lane found it a minute after the sentence was written.

The gate is right and the paragraph it replaced was reasoning where it should have
looked. A receiver cannot even REGISTER for a name the list omits — `validateTopics`
refuses it as unsupported — so a published topic left out is not a conservative
choice, it is a topic no integrator can ever subscribe to with nothing anywhere
saying why.

Two things made the wrong conclusion easy, and both are about how the search was
done rather than about the plugin. The grep was for `Test.*[Ff]orward` inside
`plugins/webhookout/*_test.go`, the gate lives in `topics_test.go`, and its name does
begin with the word the pattern was looking for — it was matched and the output was
read for the four other hits it showed. And the plugin's list has a comment
explaining why it is written out rather than ranged over, which reads like a curated
list defending itself.

The payload carries no money and no personal data, so no new redaction rule was
needed. The subscription is written out beside the others rather than looped, because
the reverse gate resolves a subscription's name statically and a loop variable is a
name it cannot resolve.

## The mutation table

| Mutation | Bitten by |
|---|---|
| the flow returns the canceled quantity, ignoring the window | `TestUnshippedUnitsGoBackAndShippedOnesDoNot` |
| a second cancellation starts from zero | `TestUnshippedUnitsGoBackAndShippedOnesDoNot` |
| a redelivery is treated as a failure | the out-of-tree example lanes |
| a variant that tracks no stock is an error | `TestAVariantThatTracksNoStockIsNotAFailure` |
| an order with no sale movement restocks anyway | `TestAnOrderWithNoSaleMovementIsNotAFailure` |
| a negative count in the payload is accepted | `TestAnEventThatCannotBeActedOnIsRefused` |
| the event's id comes from the order, not the row | `TestAWriteOffSAYSSoOnTheBus` |
| the outbox row is not written | `TestTheCANCELLATIONAndItsPromiseCommitTogether` |
| `canceled_before` is always zero | `TestASecondWriteOffSaysWhereItSitsInTheLINE` |
| the cancellation movement's reference is wrong | `TestCanceledUnitsGoBackAsTheirOwnLedgerReason` |
| the sale movement does not remember its order | `TestTheLocationLockComesFirst` |
| the reference/reason pairing is unchecked | the language ledger gate — see below |
| the unique index is not unique | every movement-writing test, loudly |
| a cancellation may deduct | `TestTheCHECKsRefuseACancellationTheStoreWouldNeverWrite` |
| a cancellation may carry no reference | the same test's second case |
| `ON CONFLICT` is dropped from the insert | `TestOneCancellationPutsItsUnitsBackOnceAgainstTheDatabase` |

Sixteen, each with `-count=1`, each restored from a scratchpad copy rather than with
`git checkout`.

## Three things the work itself found

**The pairing check caught a test of mine.** A reference belongs to the two reasons
that point at something, and a blanket rewrite of the confirmation call sites gave an
ORDER to a replacement — goods leaving against a claim. The service refused it, which
is the check earning its place on the day it was written.

**The ledger's gate named a function, and the refactor renamed it.**
`TestEveryPhysicalStockWriteGoesThroughTheLedger` allows exactly one caller of
`AppendMovement` and names it. Splitting `recordMovement` into a pair broke that, and
the fix was not to edit the gate: the two functions were collapsed back into one with
an extra argument, so the rule stayed literally true. A gate that does not need
editing is a gate that keeps meaning what it said.

**A new integration test passed alone and failed in the suite.** `SaleLocations` asks
by order, and the package's shared `testSaleOrderID` is used by every other
confirmation in it — so the assertion saw another test's movements. It has its own
order id now. The lesson is already in this repository's notes and it still cost a
run: running a new integration test with `-run` is not running it.
