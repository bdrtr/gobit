# A gift box of three — measured 2026-09-29

The evidence behind [ADR 0234](../adr/0234-a-variant-names-what-it-is-made-of.md).

## 1. What there was

| Part | State |
|---|---|
| a variant's stock | `manage_inventory`, `allow_backorder`, and one inventory item through the `product_variant_inventory` link |
| the storefront badge (ADR 0040) | in stock when not counted, OR sold past zero, OR the linked item's available quantity is above zero; a counted variant with no item is out of stock |
| checkout (`inventoryItems` in `internal/workflows/checkout/plan.go`) | a counted variant with no item and no backorder is refused with `checkout_workflow_variant_not_stocked` |
| what a variant is made of | nothing: a box was a variant with an item of its own |
| the product deletion paths | `DeleteVariant` and `DeleteProduct` are the only callers of the variant soft deletes |
| `is_giftcard` | written at creation only; no update path changes it |

## 2. Where each rule is held

| Rule | Where |
|---|---|
| at most 20 components, 1 to 100 units, none twice | `bundleComponentIDs`; the table's `quantity` check, primary key and rank |
| live, another product's, not a gift card | `requireBundleShape`, over the rows `LockLiveVariantsForBundle` holds |
| no bundle inside a bundle, either way round | `requireBundleShape`, reading the compositions under the same locks |
| a bundle is counted and not sold past zero | `requireBundleShape`; `UpdateVariant` asks `requireBundleCounted` with the flags it would write |
| no inventory item on a bundle | `requireNoInventoryItem` before the composition; `requireNotBundle` before the link |
| a component is not deleted | `requireNotComponent` in `DeleteVariant` and `DeleteProduct`, locking before it reads |
| the composition goes with its bundle | `SoftDeleteVariant` and `SoftDeleteProductChildren` |
| the composition is a revision | `SetVariantBundle` writes inside `revise`; `product_bundle_component` is in the revision view's table list in `internal/arch` |

The bundle's own variant is its product's, so "a bundle is not its own
component" is the own-product rule; the table's `not_itself` check backs it.

## 3. The races

Each is an integration test that holds a row in a raw transaction, starts the
service call, sees it wait 500 ms, commits, and asserts how the wait ended.

| Test | Held | Waiting | Ends |
|---|---|---|---|
| `TestABundleWriteWaitsForAComponentsDeletion` | the component's row, soft deleted | `SetVariantBundle` | 422, nothing written |
| `TestAComponentsDeletionWaitsForABundleWrite` | both rows, a composition inserted | `DeleteVariant` of the component | 409, the component live |
| `TestAProductsDeletionCoversTheVariantItWaitedFor` | the product's row, a new variant a bundle holds | `DeleteProduct` | 409, the new variant live |

In the second, the deletion's own UPDATE waits on the same row, so the wait is
seen without the lock too; what the lock changes is the answer, and that is the
assertion.

## 4. The storefront and checkout, end to end

`TestAGiftBoxNamesWhatItIsMadeOf`: a priced box of a stocked towel and two
stocked soaps. The composition round trips over the admin surface; the REST
product read carries it with `"in_stock": false`, GraphQL answers the same; a
stocked variant is refused as a bundle with 409; deleting a soap answers 409
naming the box; the box goes into a cart at its own price and the completion
answers 422 with `checkout_workflow_variant_not_stocked`.

## 5. The calibration and its copies (D158)

ADR 0219 moved four rows of the pinned table in `graph/limits_test.go`; the
table in the `DefaultMaxComplexity` godoc and the one in `docs/api-surfaces.md`
kept the numbers from before it.

| Row | the copies | pinned before this record | pinned now |
|---|---:|---:|---:|
| product page (PDP) | 2,390 | 2,640 | 2,840 |
| with its three related lists | 6,440 | 6,690 | 6,890 |
| ALL fields, default page | 28,880 | 33,880 | 37,880 |
| ALL fields, `limit=100` | 140,400 | 165,400 | 185,400 |

The byte columns were taken again with the fixture in `graph/handler_test.go`,
the ceilings raised so every document runs:

| Row | request | response |
|---|---:|---:|
| product page (PDP) | 823 B | 6.9 KiB |
| with its three related lists | 1,162 B | 13.2 KiB |
| ALL fields, default page | 832 B | 138.6 KiB |
| ALL fields, `limit=100` | 844 B | 693.1 KiB |

`TestTheComplexityTableCopiesMatchThePinnedOne` maps every pinned document to
its row in each copy and compares the complexity column; against the copies as
they were it failed on the four rows of each.

## 6. Mutations

| # | Mutation | Killed by |
|---|---|---|
| B1 | a component named twice passes | `TestABundleIsRefusedRatherThanShortened` |
| B2 | no unit ceiling | the same |
| B3 | zero units pass | the same |
| B4 | no component ceiling | the same |
| B5 | the product's own variant passes | the same |
| B6 | a gift card component passes | the same |
| B7 | a gift card bundle passes | the same |
| B8 | a bundle not counted passes | `TestABundleIsRefusedByTheCatalogsState` |
| B9 | a bundle sold past zero passes | the same |
| B10 | a bundle inside a bundle passes | the same |
| B11 | a component made a bundle passes | the same |
| B12 | a missing component passes | `TestABundleIsRefusedRatherThanShortened` |
| B13 | a bundle with its own item passes | the state test; the end-to-end test |
| B14 | a bundle takes an item | `TestABundleStaysCountedAndUnlinked` |
| B15 | a bundle updated uncounted | the same |
| B16 | a component deleted | `TestABundlesComponentIsNotDeleted`; the end-to-end test |
| B17 | a component's product deleted | `TestABundlesComponentIsNotDeleted` |
| B18 | the deletion asks before it locks | `TestAComponentsDeletionWaitsForABundleWrite` |
| B19 | the bundle write reads without `FOR NO KEY UPDATE` | both row races |
| B20 | the product deletion reads its variants before the product's lock | `TestAProductsDeletionCoversTheVariantItWaitedFor` |
| B21 | the product read carries no composition | the order and revision tests; the round trip; the end-to-end test |
| B22 | the variant read carries no composition | `TestABundleKeepsTheOperatorsOrder` |
| B23 | a bundle's deletion leaves its rows | `TestABundleRoundTripsOnTheRealSchema` |
| B24 | a bundle's product leaves its rows | the same |
| B25 | the write in a transaction that is not a revision | `TestABundleWriteIsARevision`; `TestEveryWriteToAProductsViewIsARevision` |
| B26 | the rows read against their rank | `TestABundleRoundTripsOnTheRealSchema` |
| B27 | the rows written against the order given | the same |
| B28 | the route outside the If-Match group | `TestEveryRevisingWriteTakesTheVersion` |
| B29 | the field priced as a scalar | `TestEveryListFieldIsPriced`; the calibration |
| B30 | a number in the document's copy moved back | `TestTheComplexityTableCopiesMatchThePinnedOne` |
| B31 | a number in the godoc's copy moved back | the same |

Thirty-one mutants, all killed. B25 first died by a build failure, the bundle
variant left unused; written again so it compiles, it dies by both gates.
