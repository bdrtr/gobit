# An operator names an engraving — measured 2026-09-29

The evidence behind [ADR 0232](../adr/0232-the-panel-edits-a-products-add-ons.md).

## 1. What there was

| Part | State |
|---|---|
| a product's add-ons | ADR 0228's list, written by variant id over the admin API only |
| the panel's related products | ADR 0181: a section on the product page and a form of handles, resolved by the admin surface's `ResolveProductRefs` |
| a variant by SKU | a `GetVariantBySKU` query nothing called; no batch read |
| the panel's write surface | `ProductWriter`, pinned to the product module's `AdminSurface` at compile time |

## 2. The panel

`TestTheProductPageListsItsAddOns`: a ring whose add-ons are a wrap without a
SKU and a draft engraving, answered by the read layer in the other order, shows
the wrap first and the engraving marked as left out by the storefront, the
variants read in one call by the list's ids and their products in one call.
`TestTheAddOnsFormShowsTheReferencesInOrder`: the form reads `variant_wrap` and
`ENG-1`. `TestSavingTheAddOnsSendsEveryLine`: a typed list with blank lines and
spaces is sent as two references. `TestARefusedAddOnSaveComesBackWithWhatWasTyped`
and `TestTheAddOnsCannotBeSavedWithoutTheModule` hold the refusal and the
missing surface.

## 3. The admin surface

`TestThePanelNamesAnAddOnByItsSKU`: a variant id and a SKU resolve in the order
typed; a SKU nobody carries is refused naming it, and the list stays.

## 4. Mutations

| # | Mutation | Killed by |
|---|---|---|
| P1 | the page in the variants' order | the page test, the form test |
| P2 | a hidden add-on unmarked | the page test |
| P3 | the form naming every add-on by id | the form test |
| P4 | the save sending the text whole | the save test |
| P5 | every reference read as an id | the admin surface test |
| P6 | an unknown SKU dropped | the admin surface test |
| P7 | the form unscoped | the panel's privilege test, the application's |

Seven mutants, all killed on the first run.
