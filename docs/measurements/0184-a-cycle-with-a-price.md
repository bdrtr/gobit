# A cycle with a price — measured 2026-09-25

The evidence behind [ADR 0184](../adr/0184-the-graphql-product-names-its-neighbors.md).

## 1. The cost model before the field

`complexityCosts` priced the `products` and `product` queries as a round trip
(`rootQueryCost`, 1,000) plus their children, and seven nested lists by
`collectionEstimate` (10 per element). The page envelope's `items` list is
deliberately unpriced, because the page size is charged on the `products`
query. Nothing checked that the list was complete.

## 2. The complexity of the new documents

Read from gqlgen's own refusal message, as `measuredComplexity` does:

| Document | Complexity | Outcome at 50,000 |
|---|---:|---|
| product page, every field and three `related` lists of cards | 6,440 | passes |
| `related { id }` under a page of 47 | 48,564 | passes |
| `related { id }` under a page of 48 | 49,576 | passes |
| `related { id }` under a page of 49 | 50,588 | refused |
| `related { id }` under a page of 50 | 51,600 | refused |
| a chain of three `related` lists | 113,000 | refused |
| a chain of eight, the deepest the default depth allows | 11,211,112,000 | refused |

The card is `id handle title thumbnail inStock variants { id priceSet
inventoryItem }`. Priced as a nested list instead of a round trip, the page of
50 measured 1,600, a thirty-second of 51,600, and passed.

## 3. The calibration document was two fields light (D134)

`TestAllProductFieldsSelectsTheWholeTree` parses `allProductFields` against the
generated schema and compares each object type it reaches with what it selects.
Its first run failed on two fields:

```
allProductFields does not select Product.typeId, ...
allProductFields does not select Image.altText, ...
```

`typeId` entered the schema in cc6eda7 (ADR 0101) and `altText` in e385d8c
(ADR 0104), both on 2026-09-10. Selecting them moved the calibrated rows:

| Document | Before | After |
|---|---:|---:|
| product page, every field | 2,379 | 2,390 |
| every field, default page of 20 | 28,660 | 28,880 |
| every field, page of 100 | 139,300 | 140,400 |

## 4. Bytes

The request column is the length of the document itself, the same measure
behind the table's unchanged rows (the category list is 118 bytes). The
response column is the body the endpoint wrote with `measurementCatalog`, and
four related products per list where the document asks for them:

| Document | Request | Response |
|---|---:|---:|
| product page, every field | 675 B | 6.9 KiB |
| product page with three `related` lists | 1,014 B | 13.2 KiB |
| every field, default page of 20 | 686 B | 137.0 KiB |
| every field, page of 100 (ceiling lifted) | 698 B | 685.1 KiB |
| `related { id }` under a page of 50 (ceiling lifted) | 73 B | 4.6 KiB |
| a chain of three `related` lists (ceiling lifted) | 118 B | 1.3 KiB |

The same measurement of the category list, whose document did not change, gives
14.9 KiB where the table says 15.1 KiB, and the page of 100 without the two
fields gives 682.5 KiB where it said 686 KiB. The fixture has drifted a little
since those rows were taken. They were not rewritten, because their documents
are the ones the table names.

## 5. The field on the real statements

`TestGraphQLReadsTheNeighborsRESTReads` runs against PostgreSQL. For every kind
in both channels, the GraphQL `related` list equals the REST address's list for
the same product. The fixture leaves a draft and another channel's product out
of channel A's cross-sell list, so the comparison has something to disagree
about.

## 6. Mutations

| Mutation | Result |
|---|---|
| the resolver passes no channels | red: the integration test and `TestRelatedAsksForItsProductWithTheIdentitysChannels` |
| the resolver ignores the kind | red: the integration test |
| the product's `related` priced as a nested list | red: the calibration and the two-sided separation |
| the cost line of the product's `related` removed | red: `TestEveryListFieldIsPriced` |
| `typeId` and `altText` left out of `allProductFields` | red: `TestAllProductFieldsSelectsTheWholeTree` (its first run) |
