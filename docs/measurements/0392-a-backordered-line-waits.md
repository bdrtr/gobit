# A backordered line waiting — measured 2026-10-05

Evidence for [ADR 0392](../adr/0392-a-backordered-line-waits-for-its-units.md)
and gap D242. Measured on a tree at 4cd5ec7f with Docker's `postgres:16-alpine`,
every Go command run as `GOTOOLCHAIN=go1.26.6 GOMAXPROCS=2 nice -n 19`.

## D242, reproduced at 4cd5ec7f

A probe in `internal/e2e` ran with the whole package (every other test green):
one variant with `allow_backorder`, one unit on the shared shelf, a storefront
cart of two engravings of it — "Ada" for one unit, "Bo" for five. The checkout
reserved and confirmed Ada's unit and let Bo's five through unreserved
(ADR 0048). `POST /admin/v1/orders/{id}/line-cancellations` then wrote off all
five of Bo's.

| Tree | Shelf after the checkout | Shelf after the write-off |
|---|---|---|
| 4cd5ec7f | 0 | **5** |
| with ADR 0392 (`TestAWrittenOffBackorderPutsNothingBack`) | 0 | 0, held for 2 s |

Five units that never left became stock. The cancellation flow's target was
`min(canceled, bought − committed)` = 5, and the shelf was found by item and
order through `SaleLocations`, which answered Ada's sale.

## Why

- `targetOnShelf` counted every unit bought as deducted; a backordered line's
  units were never deducted.
- `shelf()` found the location by item and order, and since ADR 0223 one
  variant with other properties is a second line of the same item.
- The order line answer (`DispatchableLinesJSON`) said "stock was deducted for
  all of them", and the line act and the parcel act both read it.

## The claim on a real PostgreSQL

`internal/modules/inventory/backorder_integration_test.go`, the package run
whole:

| Test | Outcome |
|---|---|
| I1 `TestAnArrivalFillsAWaitingClaim` | a count of 5 for a claim of 2 answers 3; the ledger is `stock_count +5`, then `sale −2` naming the claim's reservation and the order |
| I2 `TestRacingArrivalsFillAClaimOnce` | six concurrent `+1` adjustments for a claim of 3: one fill, one sale, 3 left |
| I2b `TestArrivalsAtTwoWarehousesFillAClaimOnce` | a claim of 2 naming two warehouses, its row held by the test while `+5` lands at each; released, both writes succeed, one sale, the other warehouse keeps 5 |
| I3 `TestAWithdrawnClaimIsNotFilled` | `SettleBackorder(line, 4, 4)` withdraws the claim and answers 4 undeducted; a restock of 4 stays on sale |
| I4 `TestTheSchemaRefusesAClaimThatCannotBeTrue` | each of the eight CHECKs and both unique indexes (line, reservation) refuse their row by name |
| I5 `TestSaleLocationsAnswerTheCheckoutsSale` | a claim filled at one warehouse before the confirm at another: `SaleLocations` answers the confirm's |
| `TestTheQueueReadLocksOnlyTheClaimsThatFit` | with 3 sellable, the read locks the claim of 2 here and not the 5, nor the 1 at another warehouse |
| `TestTheCountHoldsUnderAnySequence` | the model now claims and settles; first-fit queue, checked after every step |
| I6 `TestAClaimRacingADeletionFindsTheItemDeleted` | the test holds the item `FOR UPDATE`, a claim starts and waits, the test soft-deletes and commits: the claim answers NotFound and no row is written |

I4's first draft wrote a fill naming no reservation but a shelf; the row broke
two constraints and the server reported the shelf's. Its "claim of nothing"
(quantity 0, status waiting) also broke two, `quantity_positive` and
`withdrawn_is_whole`, and passed only because the server reported the first by
name; it is now status withdrawn, which breaks the first alone. Each row now
breaks one rule.

I2 cannot see the claims' own lock: its six arrivals share one level lock. I2b
races two warehouses; a count would not do, because a count takes the item's
lock exclusively and orders the two writes by itself, which the first draft of
I2b did (the mutant without `FOR UPDATE` survived it).

## On the production wiring

`internal/e2e/backorder_test.go`, the package run whole and green:

| Test | Outcome |
|---|---|
| E1 `TestABackorderedLineIsFilledByTheNextArrival` | two units ordered with none on the shelf wait on the order's line; `POST …/levels` with 5 answers 3, the claim is filled at the shared shelf, and the ledger reads `sale −2` over `stock_count +5`; a second order of 4 waits |
| E2 `TestAWrittenOffBackorderPutsNothingBack` | the D242 order above: the claim is withdrawn and the shelf stays at 0 for 2 s; a later count of 5 stays on sale |
| E3 `TestAFilledBackorderGoesBackWhereItWasFilled` | the first line sold at one warehouse, the claim filled by a count of 5 at another; two written off go back to the second, and the first stays at 0 |

## Mutants

Each was applied alone, the package run whole with `-count=1` after a green
base run, and restored; `mutate.py` asserted its anchor matched once.

| # | Mutant | Package | Killed by |
|---|---|---|---|
| M1 | a fill takes the newest claim first | inventory/service | `TestAnArrivalGoesToTheOldestClaimItCompletes` |
| M2 | the fill stops at the first claim that does not fit | inventory/service | same, third case |
| M3 | a claim is filled in part | inventory/service | same, third case |
| M4 | no fill when a count opens a level | inventory/service | seven tests |
| M5 | the fill only in `adjust` | inventory/service | `TestEveryWriteThatRaisesTheSellableQuantityFills` |
| M6 | `>` becomes `>=` | inventory/service | `TestAWriteThatRaisesNothingDoesNotReadTheQueue` and five more |
| M7 | the claim is not marked filled | inventory/service | ten tests |
| M8 | the fill's sale names no order | inventory/service | ten tests |
| M9 | a write answers the level before the fill | inventory/service | `TestALevelWriteAnswersTheCountAfterTheFill` |
| M9b | an opened level answers before the fill | inventory/service | four tests |
| M10 | a fill takes the claim's quantity, not what is owed | inventory/service | `TestAFillTakesWhatIsStillOwed` and two more |
| M11 | the withdrawal is assigned, not raised | inventory/service | `TestSettleWithdrawsUpToTheWindow` |
| M12 | a bundle's part ignores its units per line unit | inventory/service | `TestSettleWithdrawsUpToTheWindow` |
| M13 | no drain after a withdrawal | inventory/service | `TestAPartialWithdrawalFillsFromStockOnHand`, `TestTheDrainTakesStockInRankOrder` |
| M14 | a second claim of another quantity accepted | inventory/service | `TestAClaimIsIdempotentPerLine` |
| M15 | a second claim answers no claim | inventory/service | `TestAClaimIsIdempotentPerLine` |
| M16 | the drain sorts the warehouses | inventory/service | `TestTheDrainTakesStockInRankOrder` |
| M17 | deletion ignores waiting claims | inventory/service | `TestAnItemOwingOrdersIsNotDeleted` |
| M18 | the queue read is not told what is sellable | inventory/service | `TestAQueueReadLocksOnlyClaimsThatFit` |
| M20 | the queue route on the write router | inventory/api | `TestReadEndpointWorksWithNarrowScope` |
| M21 | the status filter not passed on | inventory/api | both queue tests |
| M22 | no redirect parameter after a fill | adminui | `TestACountThatFilledWaitingOrdersSaysSo` |
| M23 | the notice not read | adminui | `TestACountThatFilledWaitingOrdersSaysSo` |
| M24 | `SaleLocationsForReference` without `NOT EXISTS` | inventory (integration) | I5 |
| M25 | the queue read without `ANY (location_ids)` | inventory (integration) | `TestTheQueueReadLocksOnlyTheClaimsThatFit` |
| M26 | the queue read without the fit predicate | inventory (integration) | `TestTheQueueReadLocksOnlyTheClaimsThatFit` |
| M27a | no drain | inventory (integration) | `TestTheCountHoldsUnderAnySequence` |
| M27b | `>` becomes `>=` | inventory (integration) | `TestTheCountHoldsUnderAnySequence`, I1 |
| M27c | strict FIFO | inventory (integration) | `TestTheCountHoldsUnderAnySequence` |
| M28 | the order line one position off | checkout | four tests |
| M29 | an uncounted line claimed | checkout | `TestAnUncountedLineIsNotClaimedEvenWithAnItem` |
| M30 | a line with no item claimed | checkout | `TestALineNoWarehouseHoldsIsNotClaimedWhenItIsNotCounted` |
| M31 | a short order answer unguarded | checkout | `TestAnOrderThatDoesNotFollowTheCartRecordsNoClaim` (panic) |
| M32 | the position checked on the variant only | checkout | same |
| M33 | a declared warehouse ranked anyway | checkout | `TestADeclaredLocationIsTheClaimsOnlyWarehouse` |
| M34 | the write-offs read once, before the claim | checkout | `TestAClaimSeesAWriteOffMadeBeforeIt` |
| M35 | the claims after the confirms | checkout | `TestClaimsComeBeforeTheConfirm` |
| M36 | a bundle's two parts of one item not summed | checkout | `TestABundleOwesOneClaimPerItem` |
| M37 | the channel's warehouses ignored | checkout | `TestARecoveryNarrowsTheWarehousesToTheChannel` |
| M38 | the line act withdraws what was canceled | ordercancel | `TestAWriteOffUnderALiveParcelWithdrawsOnlyWhatWillNotLeave` |
| M39 | the line act does not settle | ordercancel | five tests, the property among them |
| M40 | the shelf's target ignores the undeducted units | ordercancel | three tests, the property among them |
| M41 | the filled shelf ignored | ordercancel | `TestAFilledClaimGoesBackWhereItWasFilled`, the property |
| M42 | the line act's early return removed | ordercancel | `TestAWrittenOffLineUnderAParcelKeepsItsClaim` |
| M43 | the parcel act withdraws what was canceled | ordercancel | `TestACanceledParcelBesideALiveOneWithdrawsOnlyWhatWillNotLeave` |
| M44 | a failed settlement read as "no claim" | ordercancel | `TestASettlementThatFailsPutsNothingBack` |
| M45 | the checkout settles without the live parcels | checkout | `TestAClaimIsSettledAgainstTheOrdersLiveParcels` |
| M46 | the checkout's window not clamped at zero | checkout | same, third case |
| M47 | a parcel read that fails taken as no parcel | checkout | `TestAClaimWhoseParcelsCannotBeReadIsNotSettled` |
| M48 | the order line checked at the cart index | checkout | `TestAnAddOnAheadOfABackorderedLineIsNotItsOrderLine` |
| M49 | the order line taken at the cart index | checkout | same |
| M50 | both acts settle before they read the sales | ordercancel | `TestTheShelfIsReadBeforeTheClaim` |
| M51 | the line act drops the fill's error | ordercancel | `TestAFillThatFailsAfterTheWithdrawalStillPutsBack` |
| M52 | the parcel act drops the fill's error | ordercancel | same |
| M53 | any settlement error read as "no answer" | ordercancel | same |
| M54 | the shelf's target not clamped at zero | ordercancel | three tests, the property among them |
| M55 | the settlement drops the drain's error | inventory/service | `TestAFillThatFailsAfterTheWithdrawalAnswersAndSaysSo` |
| M56 | the drain stops at the first warehouse it could lock | inventory/service | `TestTheDrainPassesOverAWarehouseThatCannotFillIt` |
| M57 | a closed warehouse fails the drain | inventory/service | same |
| M58 | the panel's count answers what was typed | inventory/service | `TestACountAnswersWhatIsLeftAfterTheWaitingOrders` |
| M59 | the queue read without `FOR UPDATE` | inventory (integration) | I2b |
| M60 | a claim without the item's shared lock | inventory (integration) | I6 |
| M61 | the reservation index not unique | inventory (integration) | I4 |

Five survived the first pass and each named a test that was not there: M2 and
M3 (the fit predicate hid them from a claim that never fitted; a claim that fits
until an earlier fill takes the units now kills both), M29 (the plan leaves an
uncounted line's item empty, so the test drives the last step over a record
that carries one), and M38 and M43 (the property's one parcel makes the window
equal what was canceled whenever the parcel act runs; two unit tests hold a
parcel live beside the write-off). M13 and M37 failed to build in the first pass
and were rewritten.

M45–M61 came from the review: each survived the suite as it then stood and is
killed by the test named. M54 survived because the cancellation flow's fake took
a target of zero or less as "already back", where the module refuses it; the
fake now refuses it too. M50 is the order D242 recurred through for a line whose
claim was recorded: a settlement that ran before the checkout's claim answered
"no claim", and a sale of another line confirmed before the shelf read was
credited with the whole window.

## The cost on the release path

Not measured. The 52 000-product rig was not run under this change's machine
load limit, and no plan of the read was taken. Every write that raises a
sellable quantity — a release among them — adds one `LockWaitingBackordersAt`;
`inventory_backorders_queue_idx`, partial on `status = 'waiting'` and led by the
item, is the index written for it. A write that raises nothing (a reservation, a
confirm) does not read it (M6).
