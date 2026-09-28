# The count beside the listing — measured 2026-09-28

The evidence behind [ADR 0226](../adr/0226-the-graphql-storefront-counts-what-it-lists.md).

## 1. What there was

| Part | State |
|---|---|
| the GraphQL roots | `products`, `product`, and ADR 0225's `collections`, `categories`, `tags`, `productAttributes` |
| the facet count | REST alone: `GET /store/v1/sales-channels/{sales_channel_id}/product-facets`, the service's `StoreFacets(StoreListOptions)` |
| what `StoreFacets` reads | `CollectionID`, `CategoryID`, `TagID`, `OptionValue`, `VariantIDs`, `Attributes`, `Search`, `SalesChannelIDs`; refuses `InStock` and `Price`; ignores `Order`, `Limit`, `Offset`, `After`, `SkipCount` |
| its round trips | the attributes, their handles when the filters name any, one count per filtered attribute, one for the rest |
| the option vocabulary | REST alone: `GET /store/v1/sales-channels/{sales_channel_id}/option-values`, the service's `ListOptionValues` with `PublicOnly`, offset pages |
| the listing's channels on GraphQL | every channel the key carries (`SalesChannelIDsFromContext`); the REST path names one of them |
| `service.Facet`'s fields | `handle`, `title`, `kind`, `options`, `true`, `false`, `products`, `min`, `max`; counts are `int64` |

`true` and `false` are Names in GraphQL's grammar and are reserved only as enum
values; gqlgen generates the schema's `true` and `false` fields of a facet bound
to the struct's `True` and `False`.

## 2. The resolvers

`TestTheFacetsAreTheRESTReadsOwn`: one document giving all seven filters, two of
them padded, reaches `StoreFacets` with exactly the trimmed filters and the
identity's two channels, no page and no order, and answers a select, a boolean
and a number facet as the service counted them.
`TestTheOptionValuesAreTheRESTReadsOwn`: `optionValues(limit: 5, offset: 2)`
asks for the published values in the identity's channel at that page. `TestAFailedVocabularyReadIsAnError` reads both
new roots: a failed read answers the error's code and no data though the fake
holds facets and values to return. `TestEmptyTextArgumentBuildsNoFilter` walks
`productFacets`' text arguments as it walks the listing's.

## 3. The price

A facet selection of one field is 1, times 100 attributes, plus 1,000 for the
root: 1,100. `TestTheFacetsArePricedByTheirRoundTrips`: under a ceiling of 1,500
the unfiltered count passes and the same count filtered on one attribute
(2,100) is refused before the service is asked. Every field of a facet with its
options' three fields selects 38 per facet: 4,800 unfiltered, 14,800 on ten
filtered attributes, under the default ceiling of 50,000.
`TestTheVocabularyIsPricedByItsPage` reads `optionValues` beside the other three
pages; `TestEveryListFieldIsPriced` reads a facet's `options` as priced and
the option vocabulary page's `items` as priced on its root.

## 4. The gates

`TestProductFacetsTakesTheListingsFilters`: every `productFacets` argument is a
`products` argument of the same type, and `limit`, `offset`, `after`, `sort`,
`inStock` and `price` are the listing's arguments it leaves out, each with a
reason. `TestEveryObjectTypeIsBound` failed on the first run with the four new
types missing from `bindings()`, as D153's gate is meant to: `Facet`,
`FacetOption` and `OptionValuePair` leave out no field, `OptionValuePairList`
its cursor.

## 5. On the production wiring

`TestTheGraphQLStorefrontCountsWhatRESTCounts`: a select attribute and three
products in one collection, one published, one published and bound to another
sales channel, one a draft. Through the publishable key, `productFacets` with
the collection and the attribute answers the facets the REST count answers for
the key's channel, the filtered attribute counting its chosen option once and
its other option, held by the other two products, zero. `optionValues(limit:
100)` equals the REST page and its count, names the published product's value
and not the other two's.

## 6. Mutations

| # | Mutation | Killed by |
|---|---|---|
| F1 | the facets' channels dropped | the resolver test, the end-to-end test |
| F2 | the facets' attributes dropped | the resolver test |
| F3 | the collection passed untrimmed | the resolver test, the empty text test |
| F4 | the search read from `optionValue` | the resolver test |
| F5 | the variants dropped | the resolver test |
| F6 | a failed facet read answered empty | the failed read test |
| F7 | draft products' values listed | the resolver test, the end-to-end test |
| F8 | the option values' channels dropped | the resolver test, the end-to-end test |
| F9 | the option values' limit and offset swapped | the resolver test |
| F10 | a failed option values read answered empty | the failed read test |
| F11 | the filtered attributes unpriced | the facet pricing test |
| F12 | a facet's options unpriced | the list pricing gate |
| F13 | the option values unpriced | the page pricing test |
| F14 | `Facet` left out of the gates | the binding gate |
| F15 | `productFacets(collectionId:)` typed `String`, regenerated | the argument gate |
| F16 | the option value passed untrimmed | the empty text test |
| F17 | the facets asking for a page | the resolver test |
| F18 | `inStock` dropped from the gate's reasons | the argument gate |

Eighteen mutants, all killed on the first run.
