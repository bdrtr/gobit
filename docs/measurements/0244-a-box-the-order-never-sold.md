# A box the order never sold — measured 2026-09-30

The evidence behind [ADR 0244](../adr/0244-a-bundle-variant-is-replaced-from-its-catalog-parts.md).

## 1. What there was

| Part | State |
|---|---|
| an item that names a line | the line's `components` copied as its parts (ADR 0238) |
| an item that names a variant | no parts; the dispatch looks the box up, finds no inventory item and answers `returns_workflow_no_inventory_item` |
| the order module's catalog | `Options.Catalog`, the query layer's `Graph`, optional; used for payments and the timeline |
| the product module | publishes `bundle_components` on the `variant` entity: a list of `variant_id` and `quantity` |

## 2. The shape

| Piece | Now |
|---|---|
| `variantBundleParts` | one `Graph` over `variant` with the request's variant ids and `bundle_components`, before the transaction; a variant with no composition is absent |
| reading a composition | records in process, maps and floats once they crossed JSON; a part with no variant or a count that is not a whole 1–100 is `order_catalog_read_failed` |
| `CreateReplacement` | a variant item takes the bundle's parts; a line item takes the line's |
| the names | `CatalogEntityVariant`, `CatalogFieldBundleComponents`, `…VariantID`, `…Quantity`, `CatalogFilterIDs`, bound to the product module's (and the checkout's `ids`) by `TestTheBundleNamesAgree` |
| no catalog | nothing read, no parts, the old refusal |

## 3. The tests

| Test | Holds |
|---|---|
| `TestAReplacementOfABundleVariantKeepsWhatTheCatalogMakesIt` | the box's parts, the plain variant none, one read for both variants |
| `TestABundleVariantsPartsAreHeldOneByOne` | a variant item's parts take their promises and the record waits for both |
| `TestACatalogThatCannotBeReadRecordsNothing` | a failed read and a part in halves record nothing |
| `TestAnExchangeSendsAGiftBoxFromItsParts` (end to end) | a shirt exchanged for a box; the replacement read shows `parts`; the catalog's box is remade of three soaps before the dispatch; `sent_units` 3, towels 10 → 9, soaps 10 → 8 |

The end-to-end test is also the proof that the production wiring gives the
order module its catalog: without it the box would have been refused.

## 4. Mutations

| # | Mutation | Killed by |
|---|---|---|
| V1 | a variant item takes no parts | the service tests |
| V2 | a failed catalog read ignored | `TestACatalogThatCannotBeReadRecordsNothing` |
| V3 | a fractional count taken | the same |
| V4 | only the first variant read | `TestAReplacementOfABundleVariantKeepsWhatTheCatalogMakesIt` |
| V5 | the parts read under another field name | `TestTheBundleNamesAgree` |
| G1 | 0238's row back to `current` | `TestTheADRIndexNamesEveryAmendment` |

Six mutants, all killed on the first run.
