# A promotion's latest uses — measured 2026-10-01

The evidence behind [ADR 0313](../adr/0313-the-panel-shows-a-promotion.md).

## The bench

- A scratch container, PostgreSQL 16.15 (postgres:16-alpine), with the
  promotion module's migrations 000001–000004 applied by `psql`.
- 50 active promotions and 300,000 uses with identifiers rising with time:
  200,000 on the busiest coupon, the rest spread over 49 others. Later, a
  coupon with 30 uses older than every other, and a seasonal coupon with
  5,000 uses older than every other. `VACUUM ANALYZE` after each load.
- The statement is the page's read, `PREPARE`d and run three times with
  `EXPLAIN (ANALYZE, COSTS OFF, TIMING OFF, SUMMARY ON)`: the promotion's
  uses, `ORDER BY id DESC LIMIT 20`.

## With the 000001 index on `promotion_id` alone

| Coupon | Plan | Rows removed | Execution |
|---|---|---|---|
| busiest, 200,000 uses | primary key backwards, filtered | 10 | 0.02–0.05 ms |
| 2,041 uses spread through the table | primary key backwards, filtered | 2,795 | 0.31 ms |
| 30 uses, all the oldest | `promotion_idx`, then a sort | — | 0.05–0.09 ms |
| seasonal, 5,000 uses, all the oldest | primary key backwards, filtered | 300,000 | 21.5–21.6 ms |

The planner expects a coupon's uses to be spread through the table; for the
seasonal one it walks every other promotion's uses backwards before reaching
its own. The cost grows with the table, not with the coupon.

## With `(promotion_id, id)` in its place

| Coupon | Plan | Execution |
|---|---|---|
| busiest | primary key backwards, filtered (10 removed) | 0.02–0.04 ms |
| 2,041 uses | the new index backwards | 0.05 ms |
| 30 uses, all the oldest | the new index backwards | 0.03–0.05 ms |
| seasonal | the new index backwards | 0.02–0.03 ms |

The admin API's first page (`ORDER BY id LIMIT 50`) of the busiest coupon
kept its plan, 0.02–0.03 ms, and the release lookup kept the unique partial
index on `(promotion_id, reference)`.
