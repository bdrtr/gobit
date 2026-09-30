# The subtree a plan could not see — measured 2026-09-30

The evidence behind [ADR 0261](../adr/0261-a-storefront-category-lists-its-subcategories.md).

## The bench

- Container `gobit-postgres`, PostgreSQL 16.15 (postgres:16-alpine), initdb
  `--locale=C.UTF-8`.
- Database `gobit_tree_measure`, built by `gobit seed` with the default shape:
  52,004 products, 52,000 channel assignments, and 20 categories of 2,600
  products each (product n is in category (n-1) mod 20).
- Added by hand, after the seed:
  - a tree over the flat categories: `pcat_ALL` holds `pcat_A20` and
    `pcat_5`…`pcat_20`, and `pcat_A20` holds `pcat_1`…`pcat_4`;
  - `pcat_SMALL`, holding `pcat_TINY` (26 products adjacent in the listing
    order, rows 10,000 to 10,025) and `pcat_SPREAD` (26 products, every
    2,000th row).
  - Later, for the wide tree only: 50 categories under `pcat_ALL` with 100
    each below them, 5,050 categories holding no product.
  - `VACUUM (ANALYZE)` after each change.
- Each figure is the median of 10 warmed `EXPLAIN (ANALYZE, BUFFERS)`
  executions of a `PREPARE`d statement, after one cold execution. The
  statements are the listing's own: status, the filter under test, the sales
  channel filter when marked `+ch`, and for the list the keyset seek with
  `LIMIT 20`. "generic n/11" counts the executions whose plan still carried
  `$2`.

## One id: the new clause against the old one

`category_id = $2::text` against `category_id = ANY($2::text[])` with the same
single category, plan_cache_mode auto:

| category | shape | `= $2` | `= ANY` (one id) |
|---|---|---|---|
| pcat_6 (5%) | count | 7.30 ms, 1,172 buf | 7.50 ms, 1,172 buf |
| pcat_6 (5%) | count +ch | 6.70 ms, 18,588 buf | 7.33 ms, 18,588 buf |
| pcat_6 (5%) | list | 0.40 ms, 1,275 buf | 0.48 ms, 1,275 buf |
| pcat_6 (5%) | list +ch | 0.98 ms, 2,464 buf | 0.90 ms, 2,464 buf |
| pcat_TINY (26 adjacent) | count +ch | 0.11 ms, 186 buf | 0.14 ms, 186 buf |
| pcat_SPREAD (26 spread) | count +ch | 0.13 ms, 186 buf | 0.13 ms, 186 buf |
| pcat_SPREAD (26 spread) | list +ch | 0.15 ms, 186 buf | 0.17 ms, 186 buf |

The buffers are identical and the plans are the same shape.

## Subtrees under each plan mode

| subtree | shape | custom | generic (forced) | auto |
|---|---|---|---|---|
| pcat_6, 1 id (5%) | count +ch | 7.39 ms, 18,588 | 7.58 ms, 18,588 | 7.52 ms, 18,588 |
| pcat_A20, 5 ids (20%) | count +ch | 28.93 ms, 73,202 | 29.76 ms, 73,202 | 29.32 ms, 73,202 |
| pcat_A20, 5 ids (20%) | list +ch | 0.32 ms, 698 | 0.33 ms, 698 | 0.31 ms, 698 |
| pcat_ALL, 22 ids (100%) | count | 19.59 ms, 1,168 | 18.77 ms, 1,252 | 18.96 ms, 1,252 |
| pcat_ALL, 22 ids (100%) | count +ch | **87.49 ms, 157,181** | 156.79 ms, 364,468 | **152.23 ms, 364,468** |
| pcat_ALL, 22 ids (100%) | list +ch | 0.13 ms, 153 | 0.12 ms, 153 | 0.12 ms, 153 |
| pcat_SMALL, 3 ids (52 products) | count | 0.11 ms, 211 | 7.20 ms, 792 | 0.11 ms, 211 |
| pcat_SMALL, 3 ids (52 products) | list | 0.18 ms, 211 | 9.73 ms, 32,168 | 0.15 ms, 211 |
| pcat_SMALL, 3 ids (52 products) | list +ch | 0.24 ms, 365 | **21.40 ms, 62,214** | 0.25 ms, 365 |
| wide, 5,072 ids (100%) | count +ch | 93.20 ms, 157,181 | — | 163.02 ms, 374,568 (generic 6/11) |
| wide, 5,072 ids (100%) | list +ch | 0.28 ms, 153 | — | 0.29 ms, 153 |

The no-criterion count under the channel filter is 74.15 ms and 156,798
buffers on the same rig.

What auto did: at the whole-catalog subtree the count's generic plan was
adopted after the fifth execution (6 of 11), and it is a Nested Loop that
de-duplicates the map rows and probes `product_pkey` 52,000 times beside the
channel subplan's 52,000 loops. The custom plan is a Hash Semi Join over a
sequential scan of `product`, the unfiltered count's plan plus a hash of the
map. With `enable_nestloop = off` the same statement ran in 86.99 to 101.92 ms
at 157,181 buffers.

What auto did not do, and could: at the 52-product subtree a generic plan
lists in 21.40 ms against 0.24 ms. It was not adopted in these runs because
its cost estimate lost the comparison with the custom plans, and one
statement text serves every subtree on a connection, so which plans a
connection has averaged depends on what it was asked before.

## Resolving the subtree

The recursive statement of `CategorySubtree`, from `pcat_ALL`:

| tree | median |
|---|---|
| 22 categories | 0.15 ms |
| 5,072 categories | 7.58 ms |

## Planning for the ids

The statements carrying the filter run through pgx's
`QueryExecModeCacheDescribe`: an unnamed statement, planned at bind time with
the values. The integration test `TestATreeStatementIsPlannedForItsOwnIDs`
reads `pg_prepared_statements` on a one-connection pool after eight runs of
each of the three statements and finds none carrying the clause, while the
single category's listing leaves one.
