# What a parcel may hold — measured 2026-09-11

Serves [ADR 0135](../adr/0135-a-parcel-cannot-hold-more-than-the-order-owes.md).

## What the endpoint checked, read rather than assumed

ADR 0134 named this unclosed as "a dispatch can still ship units that were
canceled". The sentence was narrower than the truth, and the difference is the
whole record. `normalizeItems` is the entire validation of a parcel's contents:

| Refused | Not even asked |
|---|---|
| a blank line identifier | whether the line is on the order |
| a quantity outside `MinQuantity`…`MaxQuantity` | whether the quantity is within what was sold |
| the same line twice in one parcel | whether units of it are already in another parcel |
| | whether they were written off |

So an operator could put any line id and any quantity in a parcel. Cancellations
were one case of a surface that validated nothing against the order.

## Why it costs twice as much since ADR 0134

Before that record, shipping a canceled unit sent goods a customer had been told
were not coming. Since it, those units' stock goes back on the shelf — so shipping
them afterwards takes the same goods out of the door a second time and leaves the
count short by the difference. A fix landing on one side of the same pair makes the
other side worse, which is worth writing down about any invariant that spans two
records.

## The arithmetic, and the two sums that are not the same sum

	dispatchable = bought − canceled − committed

`committed` is what a LIVE parcel holds; the rule about which parcels count lives
in the fulfillment module's own SQL and this flow does not repeat it. Returns are
NOT subtracted: a returned unit shipped, came back and was restocked on receipt, so
whether it ships again is a new decision.

That makes two different sums over the same rows, and the order module already had
the other one. `unitsSpokenFor` is `returned + canceled` and it is right for the
cancellation CEILING, which asks what is spoken for by either act. A dispatch asks
what will not be delivered. Reusing the first for the second would bound a parcel
by goods that already left once — and a mutation doing exactly that survived the
first version of the test that was supposed to catch it (below).

| bought | canceled | in a live parcel | dispatchable |
|---|---|---|---|
| 5 | 0 | 0 | 5 |
| 5 | 2 | 0 | 3 |
| 5 | 0 | 3 | 2 |
| 5 | 2 | 3 | 0 |
| 5 | 5 | 0 | 0 |
| 2 | 0 | 3 | 0 |

The last row is parcels holding more than the line sold — a state this flow did not
create and cannot fix. It answers zero rather than a negative, because "minus two"
would make a caller's `quantity > remaining` comparison pass for a quantity of minus
three.

## Absence is the refusal, and zero could not be

A line the order does not have is left OUT of the answer. It cannot be answered with
zero, because zero is what a fully shipped line answers — and the caller has to be
able to refuse the first while accepting that the second is merely finished. Both
halves are tested on both sides of the boundary: the flow leaves it out, and the
module refuses what is missing.

## Where the check runs, and why not one line later

Before the transaction. Asking another module while holding this one's locks takes a
second connection from the same pool, and enough concurrent parcels would each hold
one and wait for another — measured for a different write in ADR 0130 and not
re-measured here, because the shape is the same and the record already exists.

## The retry, which the first draft would have broken

The check cannot simply run on every request. The first request's units are counted
as `committed` the moment it succeeds, so a retry carrying the same idempotency key
would be re-checked against a bound that now excludes its own parcel and refused —
and a retry is exactly the request that must come back with the existing shipment.

The key is looked up first, and a key that already names a fulfillment asks the bound
nothing. What guards a retry carrying a DIFFERENT item list is the mismatch check the
transaction already makes, which predates this work.

## It fails closed, and the existing tests proved it

A bound that cannot be read is not a bound. An unresolvable flow refuses the parcel —
and this was not a claim to assert: thirty-seven tests in the fulfillment module went
red the moment the guard landed, because their service had no bound. They gained a
generous one; two that build their own service gained it explicitly. A guard that
fails closed is a guard whose absence is loud.

## The mutation table

| Mutation | Bitten by |
|---|---|
| the flow does not subtract cancellations | `TestWhatAParcelMayStillHold/two_canceled` |
| the flow does not subtract what parcels hold | `TestWhatAParcelMayStillHold`, and the out-of-tree starter |
| an unknown line is answered with zero | `TestOnlyTheLinesASKEDAboutAreAnswered` |
| a fault answers an empty map | `TestAFaultInEitherSideIsREPORTED` |
| the guard does not compare quantities | `TestAParcelCannotHoldMoreThanTheLineOwes` |
| the guard accepts a line the order lacks | `TestALineTheOrderDoesNotHaveIsRefused` |
| a missing bound lets the parcel through | `TestAParcelCannotBeOpenedWhenTheBoundCannotBeREAD` |
| a retry is re-checked | `TestARetryAsksTheBoundNOTHING` |
| the guard is never called | `TestAParcelCannotHoldMoreThanTheLineOwes` |
| the order reports no cancellations | `TestTheDispatchableLinesSayWhatWasWrittenOff` |
| the order reports returns as cancellations | `TestTheDispatchableLinesCountNoRETURNS` |

Eleven, each with `-count=1`, each restored from a scratchpad copy rather than with
`git checkout`.

### Two survived, and one of them exposed a false sentence in a test of mine

**The order reporting zero cancellations** survived until the producer had a test of
its own. Every test of the bound used a FAKE order module, so the arithmetic was
covered on both sides of the interop and the surface in the middle was covered by
nothing. That is the gap a fake always leaves and it is why the producer's own test
exists.

**Swapping the surface for the returns-inclusive sum** survived a test written
specifically to refuse it. The test leaned on a shared helper called
`returnedOrder` — which, despite the name, creates no return. So it asserted zero on
an order with neither a return nor a cancellation, and its own comment said "this
order has a RETURN on its line", which was false. The fixture now creates one.

This is the third fixture in this stretch that could not separate the rules it was
written for, and the only one that also made a comment lie. A fixture is not a
detail of a test: it is the part that decides whether the test is about anything.
