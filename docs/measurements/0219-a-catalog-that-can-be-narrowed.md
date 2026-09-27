# A catalog that can be narrowed — measured 2026-09-28

The evidence behind [ADR 0219](../adr/0219-a-product-carries-typed-attributes.md).

## 1. What a product could say before

| Part | State |
|---|---|
| typed fields | `material`, `origin_country`, `weight` and the dimensions on the product; `metadata` untyped on product, variant, type, collection and image |
| the storefront filters | one each of `collection_id`, `category_id`, `tag_id`, `option_value`, `q`, `variant_id`, `in_stock`, a price bracket |
| how a filter is built | an EXISTS clause in `productFilterSQL`'s body, shared by the list and its count and scoped to the path's channel (ADR 0044) |
| a count per choice | none; the search plugin counts its page only |
| the GraphQL `products` query | a gate holds every `StoreListOptions` field to a schema argument, the identity or the selection set |
| advisory lock classes | 1 to 6 taken |

## 2. The service

`TestAnAttributeIsDefinedWithDerivedHandles`: handles derived from the title and
the values, the operator's order. `TestADefinitionOutsideItsKindIsRefused`: eight
definitions refused and none stored, and an option refused on a number
attribute. `TestAProductsValuesAreCheckedAgainstTheirKinds`: ten value sets
refused with the product keeping what it had, and an option named twice chosen
once. `TestTheStorefrontFiltersByAttributes`: options ORed, attributes ANDed, an
open range, a boolean arriving as the text `true`; eight criteria refused.
`TestAFacetIsCountedWithoutItsOwnFilter`: with cotton chosen the material facet
counts cotton 2, linen 0 and wool 1, the width counts the two cotton products
with their range, and the facets take two reads. `TestAListingFiltersOnAtMostTenAttributes`
and `TestTheEnrichedListingKeepsTheAttributeFilter`: eleven distinct attributes
refused and ten taken, and the availability scan keeping the attribute filter.

## 3. The surfaces

`TestTheAttributeParameterIsRead` and `TestAnAttributeParameterOutOfShapeIsRefused`:
the repeated `attribute` parameter as options, open ranges and text, and five
shapes refused before the service. `TestTheFacetsTakeTheListingsFiltersAndChannel`,
`TestAProductsAttributesAreReplacedFromTheBody`, `TestAnAttributeIsDefinedFromTheBody`.
The GraphQL schema gains `attributes` on `products` and `Product`; its argument
gate, its list pricing and its calibration moved with it: the documents selecting
every field cost 250 more per product, 2,640 for a product page and 33,880 for
the default page, which stays under the 50,000 ceiling as limit 100 stays over.

## 4. On a real PostgreSQL

`TestAttributesFilterAndCountOnTheRealSchema`: the same filters in SQL, a product
holding two named options listed once, an inclusive upper bound, the facets
counting the filtered attribute without its filter, and a product's values read
back in the definitions' order. Options came back in rank, then handle order,
which the first draft of the tests had not assumed. `TestTheFacetsCountTheChannelsCatalog`:
a product assigned to another channel is out of the facets.
`TestARemovedOptionAndAttributeNameNothing`: a removed option leaves the product
and a filter naming it is refused; a removed attribute leaves the products and
the facets. `TestTheAttributeSchemaRefuses`: five constraints.
`TestTheAttributeCountWaitsForAWriterThatHasNotCommitted`: a second transaction
holds lock class 7 and defines the hundredth attribute; a definition asked for
meanwhile is refused after its commit. `TestOptionsFollowTheirRankAndValuesAreReplaced`
and `TestASelectHoldsAtMostTwoHundredOptions`: rank before handle, a second
write replacing the first, and the two hundred and first option refused.

## 5. On the production wiring

`TestAStorefrontFiltersAndCountsByAttributes`: two attributes defined over the
admin surface, two products given values, the vocabulary read with the
publishable key, the listing filtered by an option, by a range and by both, the
facets counting wool beside the chosen cotton, the same product found over
GraphQL with its values, and an option the catalog lacks refused 422.

## 6. Mutations

| # | Mutation | Killed by |
|---|---|---|
| T1 | a select filter taking any option | the end-to-end test |
| T2 | a boolean filter taking either value | the module's integration test |
| T3 | the lower bound exclusive | the module's integration test, once written |
| T4 | the upper bound exclusive | the module's integration test |
| T5 | a range on any number attribute | the module's integration test, once written |
| T6 | the filters not applied | the integration and end-to-end tests |
| T7 | the facets not filtered | the integration and end-to-end tests |
| T8 | the facets counting a removed option | the removal test, once written |
| T9 | a product carrying a removed option | the removal test |
| T10 | a product carrying a removed attribute | the removal test |
| T11 | options out of rank | the rank test, once written |
| T12 | one attribute over the cap | the held-lock test |
| T13 | the attribute count not serialized | the held-lock test |
| T14 | one option over the cap | the option cap test, once written |
| T15 | values added to, not replaced | the rank test, once written |
| V1 | a select value of the wrong kind | the value test, once written |
| V2 | a number not finite | the value test |
| V3 | an attribute named twice | the value test |
| V4 | an option chosen twice | the value test |
| V5 | a value option the attribute lacks | the value test |
| V6 | a filter option the attribute lacks | the filter, removal and end-to-end tests |
| V7 | one filter over the most | the filter cap test, once written |
| V8 | an attribute filtered twice | the filter test |
| V9 | a range backwards | the filter test |
| V10 | a range not finite | the filter test |
| V11 | a boolean as text unread | the filter and integration tests |
| V12 | a facet counted within its own filter | the end-to-end test |
| V13 | facets with the availability taken | the facet test |
| V14 | true and false swapped | the facet and integration tests |
| V15 | options on a number attribute | the definition test |
| V16 | an option handle twice | the definition test |
| V17 | an option on a number attribute | the definition test |
| V18 | a product without its values | the integration and end-to-end tests |
| V19 | the listing ignoring the attributes | the filter, integration and end-to-end tests |
| V20 | the enriched scan dropping the attributes | the enriched listing test, once written |
| V21 | the storefront listing dropping the attributes | the filter and end-to-end tests |
| H1 | the listing handler dropping the parameter | the parameter and end-to-end tests |
| H2 | the facets handler dropping the parameter | the facets API and end-to-end tests |
| H3 | the bounds swapped | the parameter test |
| H4 | the options not split | the parameter test |
| H5 | the facet route not mounted | the API and description tests, the end-to-end test |
| H6 | the vocabulary route not mounted | the description tests, the end-to-end test |
| H7 | the values route not mounted | the description tests, the end-to-end test |
| H8 | GraphQL dropping the filter | the end-to-end test |
| D1 | a value of two kinds allowed | the schema test |
| D2 | an infinite number allowed | the schema test |
| D3 | an option chosen twice allowed | the schema test |
| D4 | an unknown kind allowed | the schema test |
| D5 | an option of another attribute allowed | the schema test |

Nine survived the first run, and each one's test was missing a case that told
two rules apart. No test bounded a range from below at a product's own value
(T3), and every product held one number, so a range on the wrong attribute
matched the same products (T5); the fixture gained a second number. Nothing
counted a select's total after an option was removed (T8), gave an option a
rank (T11), filled a select to its cap (T14) or wrote a product's values twice
(T15). The value refused for a select carried no option, so it was refused as an
empty select before its kind was read (V1), and the eleven filters named one
attribute eleven times, so the rule against naming it twice refused them first
(V7); both fixtures were separated. No test filtered by an attribute beside the
availability filter, whose scan reads the catalog in chunks (V20). H1, H2 and H3
did not compile in their first form and were rewritten.
