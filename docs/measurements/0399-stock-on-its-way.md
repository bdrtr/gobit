# Stock on its way — measured 2026-10-05

Evidence for [ADR 0399](../adr/0399-stock-on-its-way-has-a-date-the-storefront-shows.md)
and gaps D257 and D258. Measured on a tree at 8339896c with Docker's
`postgres:16-alpine`, every Go command run as
`GOTOOLCHAIN=go1.26.6 GOMAXPROCS=2 nice -n 19`.

## Nothing knew what was coming, at 8339896c

```
$ git grep -l -i -e expected_at -e incoming -e supplier 8339896c -- internal/modules/inventory/migrations/
(no output, exit 1)
```

The six movement reasons were `stock_count`, `adjustment`, `sale`,
`return_restock`, `replacement` and `cancellation`; none is a supplier
delivery, and `adjustment` is documented as "breakage, a correction, a
transfer recorded by hand". `internal/arch/movement_ledger_test.go` already
named "a supplier receipt" as the next flow its choke points would have to hold.

## D257, the stale ledger description

```
$ git grep -l -e "Two of the four reasons" -e "Two of the reasons come" \
    -e "is the one reason that" -e "The only reason that carries a unique" \
    -e "which of four" -e "reason is the same shape as status — four" 8339896c -- internal/
8339896c:internal/modules/inventory/api/movements.go
8339896c:internal/modules/inventory/erasure_test.go
8339896c:internal/modules/inventory/migrations/000004_the_ledger_explains_the_count.up.sql
8339896c:internal/modules/inventory/models/movement.go
```

After the change the same probe answers only
`internal/modules/inventory/migrations/000004_the_ledger_explains_the_count.up.sql`,
whose comment was true
when that migration was written (four reasons) and which, as a migration, is
history and is not edited. The published OpenAPI description, the movement
DTO's `reservation_id` and `from_admin_request` godocs, the model's reason,
`CarriesAReference`, `FromAdminRequest`, `Reference` and
`ErrMovementAlreadyRecorded` godocs and the erasure test's comment now count
seven reasons and name each reference, a cancellation's included: the order's
cancellation or the canceled parcel of a written-off line, or the reservation of
a recalled replacement (`service/replacement_recall.go`, ADR 0239). The schema had held six reasons since
inventory migration 000006, a replacement had named its reservation since
000005, and 000007 had dropped the cancellation's unique index.

## D258, the warehouse breakdown on every storefront product

The reading chain at 8339896c:

1. `product/service/store.go` `enrichVariants` expands `product_variant_inventory`
   as `inventory_item` with no `Fields`.
2. `core/query/resolve.go` `fieldsWithID` passes an empty list on as nil.
3. `inventory/service/provider.go` `records` answered nil with every getter,
   `available_by_location` included, and computed the breakdown for it.
4. The record is published whole as `StoreVariant.InventoryItem`, which ADR 0093
   says never carries the breakdown.

`TestTheWarehouseTopologyNeverReachesTheResponse` stayed green because its fake
graph never put the field into the record. The end-to-end reproduction,
`TestTheStorefrontPublishesNoWarehouseBreakdown`, run with the whole
`internal/e2e` package at 8339896c (every other test green, 53 s):

```
--- FAIL: TestTheStorefrontPublishesNoWarehouseBreakdown (0.01s)
    Error: map[string]interface {}{"available_by_location":map[string]interface {}{"sloc_…":4},
           "available_quantity":4, …} should not contain "available_by_location"
    Messages: REST: a shop's warehouse topology is not a shopper's business (ADR 0093)
    (the same failure for GraphQL)
```

With the change the package is green (75 s), the test included.

## The lanes

| Lane | Result |
|---|---|
| `go vet` and `go vet -tags integration` on inventory, product, `internal/e2e` | clean |
| `bin/golangci-lint run` on inventory, product, `internal/arch`, `internal/e2e` | 0 issues |
| `go test -count=1` on inventory and product packages | ok |
| `go test -count=1 ./internal/arch/` | ok |
| `go test -tags integration -count=1 ./internal/modules/inventory/`, whole | ok, 18.1 s |
| `go test -tags integration -count=1 ./internal/e2e/`, whole | ok, 373 s on a loaded machine (75 s alone) |
| `go vet -tags integration ./internal/modules/product/` | clean; the package's integration run was not in this run's lanes |

## Mutants

Each mutant was applied alone after a green base run of its killing test,
run with `-count=1`, and the file restored byte for byte. Integration mutants
ran their killing test in a container of their own.

| Mutant | Killed by | Result |
|---|---|---|
| receipt written as `adjustment` | `TestAReceiptIsTheCountedUnitsThroughTheLedger` | killed |
| expected quantity written instead of the count | same | killed |
| receipt left expected | same | killed |
| level opened as a `stock_count` | `TestAReceiptOpensTheLevelAtANewWarehouse` | killed |
| receipt routed through `adjust` | same | killed |
| status guard removed, both checks | `TestAReceiptRetriedFinishes` | killed |
| a received repeat answers 409 | same | killed |
| count comparison dropped | same | killed |
| location locked before the status check | same | killed |
| a canceled repeat answers 409 | same | killed |
| another item's receipt accepted | `TestAnotherItemsReceiptIsNotFound` | killed |
| close count dropped | `TestAnExpectedReceiptKeepsItsWarehouseOpen` | killed |
| delete count dropped | `TestAnItemExpectingUnitsIsNotDeleted` | killed |
| open location not required to record | `TestAReceiptIsRefusedWhatItCannotBe` | killed |
| record quantity `> 0` weakened to `>= 0` | same | killed |
| receive quantity `> 0` weakened | same | killed |
| blank reference accepted | same | killed |
| missing moment accepted | same | killed |
| `Valid` misses `supplier_receipt` | `TestEveryReasonSaysWhoAndWhatItNames` | killed |
| `CarriesAReference` misses `supplier_receipt` | same | killed |
| `CarriesAReference` misses `cancellation` | same | killed |
| `FromAdminRequest` misses `supplier_receipt` | same | killed |
| `LeavesAgainstAPromise` misses `replacement` | same | killed |
| forecast fit predicate removed | `TestTheForecastIsTheFillReadForward` | killed |
| forecast fit predicate strict (`>=`) | same | killed |
| forecast location match dropped | same | killed |
| forecast dates a warehouse selling now | same | killed |
| receipts not ordered by moment | same | killed |
| ties not ordered by id | same | killed |
| claims not in queue order | same | killed |
| D258 fix reverted (default = every field) | `TestThePerWarehouseFieldsAreNotInTheDefaultSet` | killed |
| forecast computed per record | same | killed |
| served warehouses ignored | `TestARestockDateIsTheEarliestAtTheServedWarehouses` | killed |
| latest date instead of earliest | same | killed |
| a variant with something to sell asks | `TestOnlyAVariantWithNothingToSellAsks` | killed |
| a variant with no inventory record asks | `TestListStoreProductsMakesOneEnrichmentCall` | killed |
| a page in stock still calls | `TestAPageInStockPaysNothing` | killed |
| an unasked variant is dated | `TestABundleShowsNoRestock` | killed |
| the bundle clause dropped | `TestABundleWithARecordOfItsOwnShowsNoRestock` | killed |
| the counted clause dropped | `TestAnUncountedVariantAsksNothing` | killed |
| the badge read dated | `TestTheBadgeReadAsksNoDate` | killed |
| the scan dates every chunk it reads | `TestAFilteredListingDatesOnlyThePageItReturns` | killed |
| the scan dates nothing | same | killed |
| a product read by id undated | `TestAProductReadByIDIsDated` | killed |
| a published path undated | `TestARestockDateIsTheEarliestAtTheServedWarehouses` | killed |
| the record's item lock dropped | `TestADeletedItemIsOwedNothing`, and `TestADeletedItemIsOwedNothingOnRealSQL` | killed in both |
| movement `reference` not published | `TestAMovementPublishesWhatItWasFor` | killed |
| receive without a count reaches the service | `TestAReceiveAnswersTheReceiptAndTheLevel` | killed |
| each of the seven receipt CHECKs dropped | `TestTheSchemaRefusesAReceiptThatCannotBeTrue` | 7 killed |
| the two `received_*` CHECKs merged into one equivalence | same | killed |
| `supplier_receipt_adds`, `names_its_receipt`, `once_idx` dropped | same | 3 killed |
| unlocked read in place of the receipt lock | `TestAReceiveWaitsForACancelHoldingTheReceipt` | killed (ends `inventory_inconsistent_state`) |
| shared item lock on receive | `TestTwoReceiptsOpenOneLevel` | killed |
| listing `ORDER BY` dropped | `TestTheReceiptsAreListedByWhenTheyAreExpectedOnRealSQL` | killed |
| listing status filter dropped | same | killed |
| location lock dropped on record | `TestARecordingRacingACloseFindsItClosed` | killed |
| down migration missing its `UPDATE` | `TestTheReverseMigrationKeepsTheArithmetic` | killed |
| down migration keeps the reference | same | killed |
| close count reads every status | `TestTheCountsReadWhatIsExpectedWhereTheyAsk` | killed |
| delete count reads every status | same | killed |
| close count reads every warehouse | same | killed |
| received moment from `clock_timestamp()` | `TestAReceiptCopiesItsMovement` | killed |
| forecast fit predicate removed | `TestTheForecastIsWhatTheReceiptsDo` (P1) | killed |
| forecast ignores the claims' warehouses | same | killed |
| a receive repeated while the first holds the receipt answers 409 | `TestARepeatWaitingOnTheFirstReceiveFinishes` | killed |
| that repeat answers no level | same | killed |
| a cancel repeated while the first holds the receipt answers 409 | `TestARepeatWaitingOnTheFirstCancelFinishes` | killed |
| forecast read keeps an overdue receipt (`expected_at > now()` dropped) | `TestTheForecastReadsWhatIsStillExpectedAndStillWaiting` | killed |
| forecast read keeps a closed receipt (`status = 'expected'` dropped) | same | killed |
| forecast read keeps a filled claim (`status = 'waiting'` dropped) | same | killed |

The restock read's bundle clause was once removed as equivalent, on the ground
that a bundle is linked to no item (ADR 0234). It was not equivalent: ADR 0234
admits that a link and a composition written in the same instant can both land,
and only the fixture gave no bundle a record of its own. The clause is back, and
`TestABundleWithARecordOfItsOwnShowsNoRestock` gives the bundle a record at zero
with a forecast.

## Bytes of the calibration documents

`restockExpectedAt` joins the GraphQL documents that select every field. The
request column of `docs/api-surfaces.md` and `graph/limits.go` is the JSON
body `{"query": …}`, and the response column the measurement fixture of
`graph/handler_test.go`, each measured with and without the field:

| document | request | response |
|---|---:|---:|
| product page (PDP, everything included) | 823 → 841 B | 7,108 → 7,183 B (6.9 → 7.0 KiB) |
| product page with its three related lists | 1,162 → 1,180 B | 13,557 → 13,632 B (13.2 → 13.3 KiB) |
| ALL fields on the default page | 832 → 850 B | 141,928 → 143,428 B (138.6 → 140.1 KiB) |
| ALL fields with `limit=100` | 844 → 862 B | 709,769 → 717,269 B (693.1 → 700.5 KiB) |

The field adds 25 bytes per variant (`"restockExpectedAt":null`).

## Not measured

The storefront listing's p50/p95 on the rig of
[measurement 0191](0191-a-list-shown-in-one-read.md), with 0% and 20% of
variants out of stock and expecting receipts, was not taken: the run was held
to the per-package lanes above. A page where every variant has something to sell
makes no second graph call (`TestAPageInStockPaysNothing`); a page with any
variant out of stock makes one more, carrying three unlocked reads for all of
them. A filtered listing dates only the page it returns, once
(`TestAFilteredListingDatesOnlyThePageItReturns`), and the stock alert's badge
read dates nothing (`TestTheBadgeReadAsksNoDate`).
