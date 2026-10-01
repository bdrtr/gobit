# The carts nobody came back for — measured 2026-10-01

The evidence behind [ADR 0301](../adr/0301-an-abandoned-cart-is-deleted-after-the-shops-period.md).

## The bench

- A scratch container, PostgreSQL 16.15 (postgres:16-alpine), with the cart
  module's migrations 000001–000008 applied by `psql`.
- 500,000 carts, one every 30 seconds back from now, a tenth of them
  completed; 216,720 are open and untouched for more than 90 days. `ANALYZE`
  after loading.
- The statement is the job's selection, `PREPARE`d and run seven times with
  `EXPLAIN (ANALYZE, COSTS OFF, TIMING OFF, SUMMARY ON)`: the open carts
  untouched since the cutoff, oldest first, `LIMIT 500 FOR UPDATE SKIP
  LOCKED`.

## Without an index on the idle open carts

Every execution: a sequential scan of the table, the 216,720 matching rows
sorted on disk (external merge, 8.5 MB), 80–86 ms for one batch of 500. The
first run after a period is set reads 434 batches, about 35 seconds.

## With `carts_abandoned_idx`

`ON carts (updated_at, id) WHERE completed_at IS NULL`: every execution is an
index scan stopping at 500 rows, 0.25–0.47 ms.

The whole delete of one batch — the selection, the 500 rows by primary key
and the cascade into the four child tables, which were empty here — took
16.5 ms in a transaction rolled back afterwards.
