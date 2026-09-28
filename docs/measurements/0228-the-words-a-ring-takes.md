# The words a ring takes — measured 2026-09-28

The evidence behind [ADR 0228](../adr/0228-a-product-names-the-add-ons-its-lines-take.md).

## 1. What there was

| Part | State |
|---|---|
| a price modifier | none: ADR 0223 left it out, pricing having no row for it |
| the model chosen | an add-on line — a variant with its own price set, bound to the line it modifies (chosen by the user, 2026-09-28) |
| a product pointing at another product | `product_relation` (ADR 0180): a pointer, deleted with either side, outside the revision view |
| a product pointing at a variant | no table |
| the channel-scoped reads | the listing, the single product, the option vocabulary, the related products, the facet counts, and the search plugin's |
| the gates over them | the document's segment and tag tests named four paths by hand, the end-to-end refusal test three; the facet counts were on neither |

What an add-on line needs of the rest of the system was measured before the
model was chosen: each cart line is priced by its own price set at its own
quantity, discounted by its own product's facts and taxed by its own product's
class, and a variant with `manage_inventory` false needs no stock. The per-line
arithmetic takes an add-on line as it is.

## 2. The list

`TestAnAddOnListKeepsTheOperatorsOrder`: the list reads back in the order
written, a second write replaces it, an empty one takes it off, and a product
with none answers an empty list. `TestAnAddOnListIsRefusedRatherThanShortened`:
a variant named twice, a missing one, the product's own, an empty id and a list
of 21 are each refused with 422, and the stored list stays; a product that does
not exist is 404. `TestADeletedAddOnLeavesTheList`: deleting the variant, then
the other product, takes each entry off.

## 3. The storefront read

`TestTheStorefrontShowsTheAddOnsItMayShow`: of four add-ons, a draft's and
another channel's are skipped and the other two come in the operator's order,
each with its variant and product; the draft published and the other channel
asked, all four come back where the operator put them; a draft product's list
is 404.

## 4. On the production wiring

`TestAProductNamesTheAddOnsItsLinesTake`: over the admin surface, the ring's own
variant is refused with 422 and an engraving and a draft wrap are written; the
storefront shows the engraving with its product and not the wrap; deleting the
engraving through `DELETE /admin/v1/variants/{id}` takes it off the list, and
deleting the wrap's product takes the wrap off.

## 5. The channel-scoped reads (D155)

With the populations read off the document and the router:

| Gate | Paths it reads now | What it found |
|---|---|---|
| the document's segment test | every path under the segment, six | the facet counts described neither the segment nor its 403 |
| the document's tag test | the same six, each with its tag written | — |
| the end-to-end refusal test | every GET under the segment on the router, seven with the search | the related products, the facet counts and the add-ons refuse a foreign channel |

`TestTheUnpagedReadsDescribeOnlyTheirData`: the related products, the facet
counts and the add-ons describe one envelope of `data`; the facet counts were
described with `count`, `offset` and `limit` required, which the read does not
write.

## 6. The channel audit (D156)

`TestEveryChannelScopedRouteNarrowsThroughTheOneHelper` saw five routes: the
listing, the single product, the option vocabulary, the facet counts and the
search. The related products, registered on `pathStoreProduct + "/related"`,
and the add-ons written the same way resolved to nothing and were not among
them; with the add-ons read left unscoped (A12) the audit stayed green and only
the end-to-end refusal failed. Both paths are now literals, the audit sees
seven, and `TestEveryRoutePathIsReadByTheAudit` fails a registration whose path
is a concatenation or a constant it cannot resolve (A13).

## 7. Mutations

| # | Mutation | Killed by |
|---|---|---|
| A1 | the product's own variant accepted | the refusal test, the end-to-end test |
| A2 | a missing variant accepted | the refusal test |
| A3 | a variant named twice accepted | the refusal test |
| A4 | the ceiling one past | the refusal test |
| A5 | a deleted variant left listed | the end-to-end test |
| A6 | a deleted product's entries left | the end-to-end test |
| A7 | the product deletion taking only its own list, regenerated | the end-to-end test |
| A8 | the storefront in the database's order | the storefront read test |
| A9 | a hidden product's list read | the storefront read test |
| A10 | the facet counts described as a page | the unpaged body test |
| A11 | the facet counts' segment undescribed | the segment test |
| A12 | the add-ons read unscoped | the channel audit, the end-to-end refusal |
| A13 | the related path a concatenation again | the readable path gate |
| A14 | the related read unscoped | the channel audit, the end-to-end refusal |

Fourteen mutants, all killed. A1 and A2 first died of a build failure, a
removed branch leaving a variable unused, and were rewritten as conditions the
compiler takes; A12 first survived the audit, which is D156.
