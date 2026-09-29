# An operator fills a box — measured 2026-09-29

The evidence behind [ADR 0236](../adr/0236-the-panel-edits-a-variants-bundle.md).

## 1. What there was

| Part | State |
|---|---|
| a variant's composition | ADR 0234's `PUT /admin/v1/variants/{id}/bundle`, by variant id, under If-Match |
| the read layer | `bundle_components` on the variant record since ADR 0235 |
| the variant page | prices and stock; nothing about a bundle, and a bundle's stock section said it was linked to no item |
| naming a variant in the panel | ADR 0232's `ResolveVariantRefs`, a SKU or an id, and the add-on page's reader of variants and their products |
| a revision from the panel | `UpdateProductBasics` with the version the form was read at, a stale save a `PreconditionFailed` |

## 2. The panel

`TestTheVariantPageListsItsParts`: a box whose parts are a towel without a SKU
and two soaps, answered by the read layer in the other order, lists the towel
first, each part with its units and linked to its own page, and says the box
counts no stock of its own. `TestTheBundleFormShowsThePartsInOrder`: the form
reads `variant_towel 1` and `SOAP-1 2`, and carries version 7.
`TestSavingTheBundleSendsEveryPart`: blank lines and spaces are dropped and a
line with no number holds one. `TestABundleLineThatDoesNotReadIsRefusedBeforeTheModule`:
`two`, `1x` and a third word are refused naming the line, and the module is
not asked. `TestARefusedBundleSaveComesBackWithWhatWasTyped`,
`TestAStaleBundleSaveComesBackAtTheStoredVersion`,
`TestTheBundleCannotBeSavedWithoutTheModule` and
`TestAVariantOfAnotherProductHasNoBundleForm` hold the refusals.

## 3. The admin surface

`TestThePanelNamesABundlesPartsBySKU`: an id and a SKU resolve in the order
typed with their units. Refused, and writing nothing: units past the bound,
none, `2³² + 1` (which the column would have read as one), a SKU nobody
carries, lists that do not pair, and a stale version.

## 4. The assembly

`TestThePanelEditsARealVariantsBundle` builds the panel from a real
installation's container: the form reads the product's version, a save of a
SKU and an id with its units reaches the product module's admin surface and is
stored, the variant page lists what the provider publishes, and a save on
version 1 after the product moved comes back unsaved.

## 5. Mutations

| # | Mutation | Killed by |
|---|---|---|
| A1 | a line with no number holds none | `TestSavingTheBundleSendsEveryPart`; the assembly |
| A2 | units that do not read taken as one | `TestABundleLineThatDoesNotReadIsRefusedBeforeTheModule` |
| A3 | a third word dropped | the same |
| A4 | a blank line refused | the same; the save test |
| A5 | the save sent on no version | the save test; the assembly |
| A6 | a stale save shown as a failure | `TestAStaleBundleSaveComesBackAtTheStoredVersion`; the assembly |
| A7 | the form naming every part by id | `TestTheBundleFormShowsThePartsInOrder`; the assembly |
| A8 | a variant of another product given the form | `TestAVariantOfAnotherProductHasNoBundleForm` |
| A9 | the form unscoped | the panel's privilege test; the application's |
| A10 | the page asking for no composition | `TestTheVariantPageListsItsParts`; the assembly |
| A11 | the parts shown with no units | the page and form tests; the assembly |
| S1 | units narrowed unchecked | `TestThePanelNamesABundlesPartsBySKU` |
| S2 | the version not asked | the same; the assembly |
| S3 | every part given the first part's units | the same two |
| S4 | lists that do not pair taken | `TestThePanelNamesABundlesPartsBySKU` |

Fifteen mutants, all killed. A11 and S3 first died by a build failure and were
written again so they compile. A10 first died only in the assembly: the panel's
fake read layer answered the composition whether it was asked for or not, as
the real one does not; the fake now answers it only when asked. The lint lane
then flagged the narrowing to `int32` (gosec G115) because the bound was
checked in a loop of its own; the check moved beside the conversion, and S1 to
S4 were run again there and died.
