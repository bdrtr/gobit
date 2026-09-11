# What a canceled parcel releases

Evidence for [ADR 0139](../adr/0139-a-canceled-parcel-gives-its-units-back.md).

Measured 2026-09-11.

## The state before, read from the code

Three facts, each checked rather than assumed, because together they are the
defect and separately none of them looks like one.

| Claim | How it was checked | Answer |
|---|---|---|
| A pending parcel's units count as GONE | the `CommittedQuantitiesForFulfillments` query and its comment | yes, and deliberately: "a pending parcel is one the warehouse is already picking" |
| Canceling a parcel touches no stock | read `Service.CancelFulfillment` end to end | it locks the row, calls the provider, flips the status, returns |
| Canceling a parcel tells nobody | `grep -rn 'Publish\|eventbus' internal/modules/fulfillment/` | no match outside tests — the module published nothing at all |

So the chain is:

1. Line L sold 5 units; the checkout deducted all 5.
2. Parcel P is opened holding 3 of them; `committed = 3`.
3. All 5 units are written off. `returnableUnits(bought=5, committed=3, before=0,
   now=5)` gives `min(5, 2) − min(0, 2) = 2`. Two units go back; **three do not**,
   correctly, because they are in a box that is going out.
4. The shop does what ADR 0135's own text prescribes and cancels P.
5. `CancelFulfillment` flips the status. Nothing recomputes step 3.

Those three units are now in no parcel, owed to no customer, and on no shelf.
Nothing failed, and the only trace is a debug line saying a canceled line had no
units to put back — written at step 3, when it was still true.

## The arithmetic

`releasedUnits` is the difference between the same window evaluated twice:

```
after  = min(canceledTotal, bought − committedAfter)
before = min(canceledTotal, bought − committedAfter − heldByThisParcel)
release = max(0, after − before)
```

Worked through the cases that decide the shape:

| bought | canceled | committed after | this parcel held | before | after | released | why |
|---|---|---|---|---|---|---|---|
| 5 | 5 | 0 | 3 | 2 | 5 | **3** | the defect above, closed |
| 5 | 0 | 0 | 3 | 0 | 0 | 0 | nobody wrote these off — dispatchable again, not sellable again |
| 5 | 2 | 0 | 3 | 2 | 2 | 0 | the write-off already reached both units while the parcel was live |
| 5 | 4 | 0 | 3 | 2 | 4 | **2** | the write-off is the ceiling, not the parcel's contents |
| 5 | 5 | 3 | 2 | 0 | 2 | **2** | first of two parcels |
| 5 | 5 | 0 | 3 | 2 | 5 | **3** | second of the same two — total 5, in either order |
| 5 | 5 | 6 | 3 | 0 | 0 | 0 | parcels holding more than the order sold: a broken record, not stock |

The fifth and sixth rows are the same two parcels canceled in sequence, and the
table is the reason the formula is a difference of STATES: neither row needs to
know what the other returned.

## The two mutations that survived, and they were opposite mistakes

Ten mutations, `-count=1`, each reverted from a copy rather than with
`git checkout`.

| # | Mutation | First pass |
|---|---|---|
| 1 | `before` replaced by a constant zero | bit (6 tests) |
| 2 | the `min(canceledTotal, …)` ceiling removed | bit (2 tests) |
| 3 | the ledger reference reduced to the parcel alone | bit (1 test) |
| 4 | the `canceledTotal <= 0 \|\| held <= 0` guard deleted | **survived** |
| 5 | the order read off the event's `reference` instead of the link | bit (1 test) |
| 6 | the negative-window clamp removed | **survived** |
| 7 | the outbox row moved outside the transaction | bit (3 tests) |
| 8 | an already-canceled parcel publishes too | bit (1 test) |
| 9 | the event id made constant instead of per-parcel | bit (1 test) |
| 10 | `QuantitiesOfFulfillment` reads the LIVE answer | bit (1 test) |

**Mutation 4 survived because the guard was dead.** With `canceledTotal = 0` both
windows evaluate to zero and the difference is already zero; with `held = 0` the
two windows are equal. The guard could not be made to fail because it was not
deciding anything. It was DELETED rather than tested: a guard nothing can break
tells the next reader that the arithmetic does not cover a case it covers.

**Mutation 6 survived because no fixture had a broken record.** Removing the clamp
changes the last row of the table above from 0 to 3 — three units added to a shelf
they never left, on the strength of a negative window. Every test happened to have
`committed ≤ bought`, so nothing separated the two behaviors. The fixture with
six units committed against five sold now exists, and the mutation bites.

The two are worth keeping side by side because they are opposite errors of the
same family: one a check that decided nothing, the other a check that decided
something no test had asked about.

## A third finding, in a test rather than in the code

One test was written expecting two units from `bought=5, canceled=2, held=3` and
failed. The code was right: while the parcel counted, the window was `5 − 3 = 2`
and the write-off had already put both units back, so the parcel released nothing.
The test had assumed the line cancellation returned nothing, which its own numbers
contradict.

It became two — [TestTheWriteOffIsTheCeilingAndNotTheParcel] for a write-off that
outruns the window (`canceled = 4`, two units released) and
[TestAParcelReleasesNothingOnceTheWriteOffIsFullyBack] for one the window already
covered (`canceled = 2`, nothing released) — because a single fixture that cannot
separate two rules proves whichever one fires first.

## What was not measured

Whether any installation has lost units this way. The state leaves no trace: the
stock figure is simply short, the movement that would have corrected it was never
written, and nothing logs above debug. A shop would find it at a stock count and
have no way to attribute it.

The cost of the new topic on an operator's endpoints was also not measured. It is
one more delivery per canceled parcel, and a parcel is canceled far less often
than one is opened.
