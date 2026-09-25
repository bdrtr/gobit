# A product page and its neighbors — measured 2026-09-25

The evidence behind [ADR 0180](../adr/0180-a-product-names-its-neighbors.md).

## 1. What was there before

`git grep -il -e "cross.sell" -e "up.sell" -e "related_product" HEAD -- internal core plugins examples`
returned nothing. Adding `-e substitute` returned seven files, and every hit was
the ordinary English word in an unrelated sentence. The feature list's B2.4 row
names Solidus and Magento as the platforms that have product relations.

## 2. Where the storefront's rules come from

The read uses two rules that already existed and adds no third:

| Question | Answered by | Also used by |
|---|---|---|
| may the storefront show the product the read starts from | `visibleStoreProduct` (taken out of `GetStoreProduct`) | the single product endpoint |
| which of the related products may it show, in what order | `StoreProductsByIDs` | search, which hands it ranked ids |

`visibleStoreProduct` is the single product endpoint's body moved into a
function of its own, with no change to its behavior. The single product tests
went green again without being edited.

`StoreProductsByIDs` keeps the order it is given, skips an id it may not show
without saying so, and refuses more than `MaxLimit` (100) ids. `MaxRelations`
is 50. `TestAFullListFitsTheStorefrontRead` writes a full list and reads it
back, so raising the limit past 100 turns that test red.

## 3. The rank

The first migration had the primary key `(product_id, type,
related_product_id)` plus an index on `(product_id, type, rank)` for the
ordered read, and nothing stopped two rows from sharing a rank. The write gives
each row its position in the array, so no row shared one. The table did not
enforce that, though, and a tie would have let two reads order the list
differently.

The index became `UNIQUE (product_id, type, rank)`. That constraint serves the
same ordered read and makes the order total. A list is replaced by deleting it
and inserting it again inside one transaction, and the unique check does not
count the rows that transaction deleted. The replacement test shows this
directly: its second write reuses ranks 0 and 1.

## 4. What bites

Each row is one mutation, applied and then restored with a sha check. Every
command was green before the mutation was applied.

| Mutation | Result |
|---|---|
| the source's channel is not asked | unit red, integration red |
| the related products are not filtered by channel | unit red, integration red |
| the storefront read orders by id instead of rank | integration red |
| the admin read orders by id instead of rank | integration red |
| the write stores the ranks reversed | integration red (three tests) |
| the soft delete keeps the relations | integration red |
| an id that names no product is accepted | unit red; integration red — the foreign key still refuses the row, as `product_invalid_reference`, but its message names the constraint and not the id the operator typed |
| the product itself is accepted | unit red |
| duplicates are accepted | unit red |
| the 51st entry is accepted | unit red, **after the fixture was fixed** (below) |
| the handler answers an empty list as `null` | api unit red |
| the `type` parameter is left out of the description | api describe test red, lint red (`unused`); the query-parameter arch gate stays green |
| the kind CHECK is dropped | raw SQL witness red |
| the self CHECK is dropped | raw SQL witness red |
| the unique rank is dropped | raw SQL witness red |

**The limit case survived its first run.** Its fixture was 51 made-up ids. With
the limit gone, the next rule refused them as missing, and the test asserted
only that the error was a 422. The fixture now seeds 51 real products, and each
refusal case asserts its own rule's words. The mutation then turned the test
red.

**The arch gate stays green by design.** Its unit is the package. The
parameter's literal lives in `relationTypeParameter`'s own body, so that
literal is in the package whether or not any description calls the function.
The per-endpoint describe test and `unused` are the gates that answer "is this
endpoint's parameter described".

## 5. End to end

`TestAProductPageReadsItsNeighbors` (`internal/e2e`) runs through the production
router and a publishable key. It creates a published shirt, a published belt
and a draft, relates the draft and then the belt to the shirt, and reads the
storefront list, which holds only the belt. It then publishes the draft. The
list becomes the draft and the belt, in the operator's order.

Its first run failed on an assertion of mine. The assertion said every
neighbor carries variants, and a product created over the admin API with no
variant has none. The assertion was removed, since the describe tests hold the
body's shape.

## 6. The lanes

Before the commit, the migration test's version went from 8 to 9, and
`product_relation` joined its table list.
