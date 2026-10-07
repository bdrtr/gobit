# A parcel that came back — measured 2026-10-07

Evidence for [ADR 0423](../adr/0423-a-parcel-that-came-back-holds-only-what-a-return-or-a-replacement-speaks-for.md)
and gap D268. Read on a tree at 7c143c96, on the first draft of the change
(d76fcc60, never merged) and on the change; every Go command ran with the
machine's `go1.27.1`, the integration and e2e packages against Docker's
Postgres through testcontainers.

## 1. What each act answered

`internal/e2e/parcel_came_back_test.go` places the write-off fixture's order
(three units of one line, ten on the shelf, seven after the sale), opens a
parcel of the three on `POST /admin/v1/orders/{id}/fulfillments`, ships it and
marks it come back on `POST /admin/v1/fulfillments/{id}/returned`. Each act
runs on its own order. "7c143c96" and "d76fcc60" are those trees with only the
test file added.

| Act after the parcel came back | 7c143c96 | d76fcc60 | After |
|---|---|---|---|
| Open a parcel of the three units | 409, "owes 0 more unit(s)" | 201, holds 3 | 201, holds 3 |
| Write the line off, 3 units | the shelf stays at 7 | back at 10 | back at 10 |
| Record a return of the 3, then open a parcel of 1 | 409 | 409 | 409 |
| Receive that return | back at 10 | back at 10 | back at 10 |
| Record a claim's replacement of the 3, then open a parcel of 1 | 409 | **201** | 409 |
| Return of 1, a parcel of 2 sent again, write off 2 | the parcel of 2 answers 409 | the shelf stays at 7 | the shelf stays at 7 |

The first two rows are the defect: on 7c143c96 the open answered
`fulfillment_line_not_dispatchable` and the shelf "never reached 10 (last
read: 7)". The fifth row is the draft's regression: a replacement the operator
recorded for the units did not hold them, and a parcel would have sent them a
third time. The last row is the write-off path with a return; with the
interop's locked count ignoring what is spoken for, the shelf reads 8.

`TestAWrittenOffParcelThatComesBackPutsItsUnitsBack` writes the line off whole
while the parcel is on the way, so nothing goes back, then marks the parcel
come back:

| | 7c143c96 | d76fcc60 | After |
|---|---|---|---|
| The shelf two seconds after the write-off | 7 | 7 | 7 |
| The shelf after the parcel is marked come back | 7 ("never reached 10") | 7 ("never reached 10") | 10 |

Before the change nothing recomputed the shelf, since marking a parcel come
back published nothing; a further write-off, a return and a parcel were all
refused by the line's bounds, and the parcel cannot be canceled once it came
back.

## 2. The rule, per line

`models.HeldUnits.Held` with three live units and two that came back, against
what returns and replacements speak for (`TestWhatALineHolds`):

| Spoken for | -1 | 0 | 1 | 2 | 7 |
|---|---|---|---|---|---|
| Held | 3 | 3 | 4 | 5 | 5 |

On the real schema (`TestEachParcelCountsAsItsStatusSays`), one reference with
a pending parcel of 1, a shipped one of 10, a delivered one of 100, one of 200
that came back, one of 500 that came back and is marked `held_whole`, a
canceled one of 300, a parcel of 5 bringing a return back, and another
reference's parcel of 400:

| Spoken for | none | 150 | 999 |
|---|---|---|---|
| Held under the lock | 611 | 761 | 811 |

`TestAParcelThatCameBackBeforeTheUpgradeIsHeldWhole` marks a parcel of 2 come
back, rolls fulfillment migration 000008 back (the column is gone), applies it
again and marks a parcel of 3 come back: the first reads `held_whole` true,
the second false, and the count under the lock is 2.

## 3. The count by link

The link-based count, which counts a parcel that came back whole, is read by
one flow. `grep -rl --include='*.go' '\.CommittedQuantities(' internal plugins examples contrib cmd`
without the test files:

```
internal/modules/fulfillment/service/interop.go
internal/modules/fulfillment/service/fulfillment.go
internal/workflows/checkout/claim_backorders.go
```

The first two forward it; the backorder claim settles a claim against it, and
its own comment says a recovery may run that step after a parcel of the order
exists.

## 4. Mutants

Each was applied alone after a green baseline, its packages run with
`-count=1`, and the files restored and checked by sha256.

| Mutant | Killed by |
|---|---|
| a unit that came back counted whole | `TestWhatALineHolds`, `TestAParcelThatCameBackHoldsNothingNoReturnNames`, `TestTheLockedCountReadsTheReturns`, `TestThePanelsCountReadsTheReturns` |
| a unit that came back held by nothing spoken for | `TestAReturnKeepsTheUnitsOfAParcelThatCameBack`, `TestTheComeBackRuleIsConservativeOnAMixedLine` |
| the larger of the two figures taken; what is spoken for not clamped at zero | `TestWhatALineHolds` |
| the open's, the panel's or the locked count ignoring what is spoken for | `TestAReturnKeepsTheUnitsOfAParcelThatCameBack`, `TestThePanelsCountReadsTheReturns`, `TestTheLockedCountReadsTheReturns` |
| the interop's locked count passing nothing spoken for | `TestTheInteropPassesWhatIsSpokenForToTheLockedCount`, and the e2e write-off with a return (shelf 8) |
| the order's lines ignoring replacements, ignoring returns, or carrying nothing | `TestTheDispatchableLinesCarryWhatAReturnOrAReplacementSpeaksFor`; replacements also by the e2e replacement subtest |
| the ceiling or the cancellations reading write-offs as spoken for, or passing nothing | `TestWhatTheReturnsAskBackGoesWithTheCeiling`, `TestAWriteOffPassesTheLinesReturnsToTheCount`, `TestAParcelCancelPassesTheLinesReturnsToTheCount` |
| the write-off counting when the order's lines cannot be read, or reading a bundle's parts from no lines | `TestAWriteOffThatCannotReadTheLinesIsTriedAgain`, `TestAWrittenOffBundlePutsBackItsParts` |
| the SQL counting a returned parcel live, a delivered one back, a canceled one live, none back, a held-whole one back or not live | `TestEachParcelCountsAsItsStatusSays` |
| migration 000008 without its backfill; the repository dropping the mark | `TestAParcelThatCameBackBeforeTheUpgradeIsHeldWhole` |
| `fulfillment.returned` not published, not written to the outbox, without its reference, or announced on a second report | `TestAParcelThatComesBackIsAnnounced`, `TestAParcelReportedComeBackTwiceIsAnnouncedOnce` |
| the cancellation flow not subscribed to it | `TestTheFlowHearsAParcelComeBack`, `TestAWrittenOffParcelThatComesBackPutsItsUnitsBack` |
| the recount bringing the line up to the parcel's units instead of the target | `TestAParcelThatComesBackPutsBackWhatTheWriteOffCouldNot` and seven parcel-cancel tests |
| a return parcel's coming back restocking | `TestAReturnParcelThatComesBackPutsNothingBack`, `TestAReturnParcelsCancelPutsNothingBack` |
| the page's sentence under every parcel that came back, under one held whole, whenever anything is owed, or never; the page not asking for the mark or dropping it | `TestAParcelThatCameBackSaysWhatTheOrderOwesAgain` |
| the read layer answering no mark | `TestAShipmentSaysItIsHeldWhole` |

One survived and is equivalent: the SQL's row filter opened to every status
alone. The two `FILTER` clauses name the statuses each figure sums, so a
canceled parcel still reaches neither; the only difference is a line held by
canceled parcels alone answering zero instead of being absent, which every
reader reads as zero.
