# The few carts an operator opened — measured 2026-10-01

The evidence behind [ADR 0296](../adr/0296-a-cart-names-the-operator-who-opened-it.md).

## The bench

- A scratch container, PostgreSQL 16.15 (postgres:16-alpine), with the cart
  module's migrations 000001–000008 applied by `psql`.
- 500,000 carts a shopper opened, one a second back from now, and 30 an
  operator opened (`opened_by = 'usr_1'`), one every 1,000 seconds, so the
  operators' carts sit among the newest 30,000 shopper carts. None completed,
  none deleted. `ANALYZE` after loading.
- The statements are the module's own, `ListCarts` and `CountCarts` as sqlc
  generated them, `PREPARE`d and run eight times each with
  `EXPLAIN (ANALYZE, COSTS OFF, TIMING OFF, SUMMARY ON)`, plan_cache_mode auto.
  The panel's read: `completed = false`, `opened_by_operator = true`,
  `LIMIT 21`.

## Without an index on the opener

| statement | executions 1–5 (custom) | executions 6–8 |
|---|---|---|
| list | parallel seq scan, 500,000 rows filtered, 22–30 ms | generic plan: `carts_alive_idx` walked, 20,997 rows filtered, 2.8–3.2 ms |
| count | parallel seq scan, 22–24 ms | the same, 22 ms |

The generic list stops after 21 matches only because the operators' carts are
recent here; with fewer than 21 open, it walks the whole index.

## With `carts_opened_by_idx`

`ON carts (created_at DESC, id DESC) WHERE opened_by IS NOT NULL AND deleted_at
IS NULL`:

| statement | executions 1–8 |
|---|---|
| list | index scan on `carts_opened_by_idx`, 21 rows, 0.03–0.08 ms |
| count | index scan on `carts_opened_by_idx`, 30 rows, 0.02–0.04 ms |

The custom plan folds `$4 IS NULL OR (opened_by IS NOT NULL) = $4` to
`opened_by IS NOT NULL`, which the index's predicate covers, and the plan cache
keeps the custom plan because the generic one costs far more.

## Mixed with unfiltered reads

The same prepared statements run six times with no filter, then three times
with the panel's: the unfiltered list walks `carts_alive_idx` (0.03–0.09 ms),
the unfiltered count is an index-only scan of `carts_region_idx` (9–15 ms), and
the panel's three list and three count executions that follow still take
`carts_opened_by_idx` (0.03–0.04 ms).
