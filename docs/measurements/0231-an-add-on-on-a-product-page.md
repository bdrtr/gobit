# An add-on on a product page — measured 2026-09-29

The evidence behind [ADR 0231](../adr/0231-the-graphql-storefront-reads-a-products-add-ons.md).

## 1. The field

`TestAddOnsAskForTheirProductWithTheIdentitysChannels`: `product(handle:
"ring") { addOns { variantId product { handle } } }` asks the storefront once,
for the product's id with the identity's channel, and answers the two add-ons in
the order the service returned them. `TestAddOnsArePricedAsARoundTrip`: one
product's add-ons pass the default ceiling of 50,000; fifty products each asking
for theirs are refused with a complexity error before the listing or any add-on
is read. `TestEveryListFieldIsPriced` reads a product's `addOns` as priced;
`TestEveryObjectTypeIsBound` failed on the first run with `AddOn` missing from
the gates' list, and `TestAllProductFieldsSelectsTheWholeTree` with the field
missing from the calibration, which now leaves it out with its reason.

## 2. On the production wiring

`TestAProductNamesTheAddOnsItsLinesTake`: the GraphQL product answers the one
visible add-on, the engraving with its product, and not the draft wrap, as the
REST read does.

## 3. Mutations

| # | Mutation | Killed by |
|---|---|---|
| G1 | the identity's channels dropped | the field test |
| G2 | the product asked by handle | the field test |
| G3 | the add-ons unpriced | the pricing test, the list pricing gate |
| G4 | the add-ons priced as a list and not a round trip | the pricing test |
| G5 | the add-ons answered empty | the field test, the end-to-end test |

Five mutants, all killed on the first run.
