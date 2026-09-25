# A list shown in one read — measured 2026-09-26

The evidence behind [ADR 0191](../adr/0191-the-catalog-reads-a-list-of-variants.md).

## 1. The shape of the clause

The measurement catalog from `gobit seed`: 52,004 products, 54,000 variants,
52,000 channel assignments, PostgreSQL 16 initialized with `C.UTF-8`, after
`VACUUM ANALYZE`. The storefront listing's own query, with the channel filter,
the keyset seek and `LIMIT 101`, and its count, for 100 and for 5 variant ids
drawn at random. Prepared statements under `force_generic_plan`, the plan a
prepared statement may switch to after its fifth execution; the custom plans
were the same shape.

| Clause | Plan, list and count |
|---|---|
| `EXISTS (… pv.product_id = product.id AND pv.id = ANY ($n) …)` | Nested Loop over a HashAggregate of the variants read by `product_variant_pkey`, then `product_pkey`; a sort of the matches |
| `product.id IN (SELECT pv.product_id … pv.id = ANY ($n) …)` | the same |
| `product.id = ANY (ARRAY(SELECT pv.product_id …))` | the variants in an InitPlan by `product_variant_pkey`, then `product_pkey` |

None of the three walks the catalog in page order. The clause is the EXISTS,
as the category, tag and option-value filters beside it are.

With the EXISTS, warm, the second and third runs of each:

| Ids | List (`LIMIT 101`) | Count | Shared buffers hit |
|---|---|---|---|
| 100 | 0.49–0.72 ms | 0.44–0.65 ms | 1,095 |
| 5 | 0.17 ms | 0.12–0.13 ms | 56 |

The buffers follow the number of ids, about eleven each, and not the catalog.
The first run of the prepared list took 8.0 ms and is left out as the cold one.

## 2. What comes back

`TestTheCatalogShowsTheProductsOfAListOfVariants` names two variants of one
product, a variant of a product in another channel, a variant of a draft, a
deleted variant and an id that never existed, beside a product nobody named:

| Read | Products |
|---|---|
| in the request's channel | the shirt, once, with its three variants; the count is 1 |
| with no channel | the shirt and the other channel's product |
| naming no variant | none |

## 3. Mutations

| Mutation | Result |
|---|---|
| the REST handler ignores `variant_id` | red: `TestTheVariantFilterReachesTheServiceAsGiven` |
| the resolver drops `variantIds` | red: `TestTheVariantFilterReachesTheServiceFromGraphQL` |
| the listing leaves the filter off the repository's | red: `TestTheVariantFilterKeepsTheProductsOwningThem` |
| the in-stock scan leaves it off its own listing options | red: the same, its scan case |
| the storefront listing leaves it off | red: the same, and the bound test |
| the bound one over a page | red: `TestAVariantFilterIsRefusedPastAPageOrMalformed`, and in the database |
| the ids never judged | red: the same |
| the clause never written | red: `TestEachCriterionAloneWritesOnlyItsOwnClause` |
| a deleted variant still names its product | red: `TestTheCatalogShowsTheProductsOfAListOfVariants` |
| the variant not tied to the product | red: the same |
