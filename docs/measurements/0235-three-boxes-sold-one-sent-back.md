# Three boxes sold, one sent back — measured 2026-09-29

The evidence behind [ADR 0235](../adr/0235-a-bundle-sells-from-its-parts.md).

## 1. What there was

| Place | After ADR 0234 |
|---|---|
| the badge (`variantInStock`) | a bundle is counted with no item, so it read out of stock |
| checkout (`inventoryItems`) | the same line refused with `checkout_workflow_variant_not_stocked` |
| the order line | a variant, a quantity and prices; nothing said what a unit held |
| the write-off (`HandleLineCanceled`) | the event's variant, through its current link; a variant with no item put nothing back |
| the parcel act (`putBack`) | the same, per line of `DispatchableLinesJSON` |
| the receipt (`restock`) | the return line's variant; a variant with no item was a warning |
| the replacement (`holdStock`) | a variant with no item refused with `returns_workflow_no_inventory_item` |

## 2. Where each rule is held

| Rule | Where |
|---|---|
| a bundle's badge is its parts' | `variantBadge` → `bundleInStock`, units per part through `unitsAvailable` (the variant rule uses the same) |
| the parts' stock and flags | the page's `enrichVariants` call with the part ids added; `bundleParts`, one `ListVariantsByIDs` |
| the checkout reads the composition | `bundle_components` on the variant record (`recordsWithBundles`), read by `readBundleParts` in `variantTitles` |
| parts the cart does not hold | the second round of `variantTitles`; a part that reads as a bundle is refused |
| a part's item | `stockVariantIDs` puts the parts in the one `ListMany` in the bundle's place |
| a part is reserved as a line is | `reservationUnits`: the line with the part's variant, item, flags and `quantity × units` |
| the record accounts for every unit | `reservationRef.variant_id`, `reserveOutput.unreserved_components`, `Restore` counting units |
| the plan pairs each part | `planLine.validateComponents` |
| the order keeps the composition | `order_line_items.components` (000033, `jsonb`, `[]` for none, CHECK array); `validateLineComponents` |
| the put-back acts read it | `DispatchableLinesJSON` and `ReturnDetailJSON` carry `components`; `lineStock` reads the order's lines for the write-off |
| each part to its multiple | `putBack` brings each part up to `target × units`; `restockPart` puts back `quantity × units` |

The bounds of a composition — 20 parts, 1 to 100 units — are written in the
product module, the checkout and the order; `TestTheBundleNamesAgree` holds the
three to one another and the field names to the product module's.

## 3. Round trips

| Path | Added |
|---|---|
| a storefront page with a bundle | no graph call; one query for the parts' flags |
| a storefront page without one | nothing |
| a checkout with a bundle whose parts are not in the cart | one variant call; the product call and the link call cover the parts |
| a write-off | one order read; per part the link read, the ledger read and the write the line already made |

## 4. The record

A plan written before this decision has no `components`, so its reservation
units are its lines and `Restore`'s identity is the one it was. A bundle line
of two parts, one counted and one not, finishes the step as:

```json
{"reservations":[{"line_item_id":"li_a","reservation_id":"res_…","location_id":"sloc_1","variant_id":"var_towel"}],
 "unreserved_components":[{"line_item_id":"li_a","variant_id":"var_soap"}]}
```

## 5. End to end

`TestAGiftBoxSellsFromItsParts`, a box of one towel and two soaps, both
stocked at ten in one warehouse:

| Step | Towels | Soaps |
|---|---:|---:|
| stocked | 10 | 10 |
| three boxes sold | 7 | 4 |
| one box written off | 8 | 6 |
| one box received back | 9 | 8 |

The admin order read carries the line's `components`; the receipt answers one
line restocked, three units, and no warning. `TestAGiftBoxNamesWhatItIsMadeOf`
now reads the box in stock over REST and GraphQL.

A replacement of a bundle names a variant with no item, the shape
`TestAVariantWithNoInventoryItemIsRefused` holds.

## 6. Mutations

| # | Mutation | Killed by |
|---|---|---|
| P1 | a part read as a yes rather than as units | `TestABundlesBadgeIsReadFromItsParts` |
| P2 | a part nobody counts limits the box | the same |
| P3 | a bundle badged by its own row | the same; the channel test; the end-to-end test |
| P4 | the parts left off the page's graph call | `TestABundlesPartsRideOnThePagesGraphCall`; `TestAGiftBoxNamesWhatItIsMadeOf` |
| P5 | a part counted over every warehouse | `TestABundlesBadgeCountsOnlyTheChannelsWarehouses` |
| P6 | the variant record carries no composition | `TestTheVariantRecordNamesWhatABundleIsMadeOf`; the end-to-end test |
| O1 | the column written empty | `TestABundleLineKeepsWhatItWasMadeOf`; the end-to-end test |
| O2 | the dispatchable lines carry no parts | the same two |
| O3 | the return detail carries no parts | `TestAGiftBoxSellsFromItsParts` |
| O4 | the snapshot's parts dropped on the way in | the same |
| O5 | the service keeps no parts | `TestABundleLineIsWrittenWithItsComponents`; the integration and end-to-end tests |
| O6 | the order read carries no parts | `TestAGiftBoxSellsFromItsParts` |
| O7 | a part held past the bound | `TestABundleLinesComponentsAreHeldToAShape` |
| O8 | a part named twice | the same |
| O9 | more parts than a bundle has | the same |
| C1 | the composition not asked for | `TestTheStockFlagsRideOnTheTitleQuery`; the end-to-end test |
| C2 | no round for the parts | `TestABundlesComponentsAreReadInARoundOfTheirOwn`; `TestAnUncountedComponentIsNotReserved` |
| C3 | a bundle inside a bundle expanded | `TestABundleInsideABundleIsRefused` |
| C4 | a part reserved once per line | `TestABundleLineReservesEachComponent`; the end-to-end test |
| C5 | a part reserved against the box's item | the same; `TestAnUncountedComponentIsNotReserved` |
| C6 | recovery counts lines | `TestTheReserveRecordCountsReservationUnits`; `TestAnUncountedComponentIsNotReserved` |
| C7 | a skipped part named as its line | `TestAnUncountedComponentIsNotReserved` |
| C8 | a reservation that does not name its part | the same |
| C9 | a part that lost its item passes the plan | `TestAPlanWhoseComponentLostItsItemIsRefused` |
| C10 | the snapshot carries no parts | `TestABundleLineReservesEachComponent`; the end-to-end test |
| C11 | a part named twice reads | `TestACompositionThatDoesNotReadIsRefused` |
| C12 | a fraction reads | the same |
| C13 | the bundle reserved as its own stock | `TestABundleLineReservesEachComponent` and the other bundle tests; the end-to-end test |
| C14 | a part held past the bound passes the plan | `TestAPlanWhoseComponentLostItsItemIsRefused` |
| X1 | the write-off puts back the box | `TestAWrittenOffBundlePutsBackItsParts` and the other bundle tests; the end-to-end test |
| X2 | a part brought up to the line's units | the same |
| X3 | the write-off reads the event, not the order | the same |
| X4 | a part that tracks nothing stops the act | `TestABundlesPartThatTracksNoStockIsSkipped` |
| R1 | the receipt restocks the box | `TestAReturnedBundlePutsBackItsParts`; the end-to-end test |
| R2 | a part restocked once per box | the same |
| R3 | a half-restocked line counted | `TestABundleWithAPartThatHasNoItemIsNotWhollyRestocked` |

Thirty-six mutants, all killed. P2 and P6 first died by a build failure and
were written again so they compile. P4 first died only in the end-to-end
test: the unit test read a listing on which the parts' own products stood, so
their ids were in the call either way; it now reads the box alone. X4 needed
the part that tracks nothing to come first, or a stop at it would have been
the last step anyway.
