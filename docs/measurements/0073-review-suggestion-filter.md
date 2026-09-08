# Narrowing the moderation queue by what a model proposed — measured 2026-09-09

Evidence for [ADR 0073](../adr/0073-a-proposal-outlives-the-decision.md). It
answers three questions: what the filter costs unindexed, which index shape
serves it, and what the shape of the data does to that answer.

## 1. The rig, and why it is a rig at all

The review module's own migration prices its partial index "against PostgreSQL
16 on 505,000 reviews over 20,001 products". That database is gone. It is the
hole `internal/rig` was written to close for the catalog — a performance
sentence resting on rows nobody promised to keep — and it was still open for
reviews, so the rig grew a review family before anything here was measured.

`gobit seed -reviews 505000` now builds it. This measurement ran on:

```
total=505000  submitted=50500
queue_with_proposal=25250  archive_with_proposal=151500
```

505,000 reviews over 20,204 products: one row in ten waiting, one in ten
refused, the rest published. Half the queue carries a proposal, and so do three
in ten of the decided rows — see section 4 for why that second number is the
one that decides everything.

PostgreSQL 16 in Docker, `VACUUM ANALYZE` before each scenario, best of three
`EXPLAIN (ANALYZE, TIMING)` runs, first page of twenty.

## 2. The table

Times in milliseconds.

| | backlog, with the index | backlog, WITHOUT | steady state, with the index |
|---|---|---|---|
| A page: queue, unfiltered | 0.028 | 0.028 | 0.025 |
| B page: queue + `suggested=rejected` | **0.032** | **7.686** | 0.028 |
| C page: queue + `suggested=none` | 0.026 | 0.027 | **11.397** |
| D count: queue, unfiltered | 4.150 | 3.877 | 4.349 |
| E count: queue + `suggested=rejected` | **1.035** | **14.477** | 1.031 |
| F page: `suggested=rejected`, no status filter | 38.300 | 38.831 | 40.384 |
| G job: awaiting a proposal | 9.453 | 7.730 | **12.111** |

`reviews_suggestion_idx` is 2,416 kB on the backlog and 3,912 kB in the steady
state, against a 49 MB `reviews_moderation_idx` beside it.

"Steady state" is the job having caught up: every waiting review carries a
proposal.

## 3. What the index buys, and what it does not

It buys B and E, which are the operator's triage: the page goes from 7.7 ms to
0.03 ms and the count beside it from 14.5 ms to 1.0 ms. It costs nothing
measurable on A and D, the unfiltered queue.

It does NOT serve F — the same filter with no status, which asks about the
archive rather than the queue. 38 ms, and left there deliberately; section 4 is
why.

It does not serve G either, and G is a little SLOWER with it present (9.5 ms
against 7.7 ms). The difference is one planner choice on a query that runs every
fifteen minutes, and it is reported rather than tuned.

## 4. The finding: the first measurement recommended the wrong index

Four shapes were built. On a rig where only the QUEUE carried proposals —
which is what an installation looks like in its first month — two of them were
indistinguishable:

| shape | size | B | E | F |
|---|---|---|---|---|
| none | — | 9.8 | 15.4 | 24.0 |
| `(status, suggested_status, created_at DESC, id DESC)` | 33 MB | 0.034 | 1.30 | 23.8 |
| partial `WHERE status='submitted' AND suggested_status IS NOT NULL` | 1,480 kB | 0.032 | 1.15 | 24.1 |
| partial `WHERE suggested_status IS NOT NULL` | 1,480 kB | 0.031 | 9.65 | **0.029** |

On that shape the last row looks best: the same size, the same page cost, and it
is the only one that serves F.

Then the rig was corrected. **Nothing clears the proposal columns when an
operator decides**, so a shop that has been running the job carries proposals on
its archive as well as on its queue — and the archive grows without bound while
the queue does not. Rebuilt with 151,500 archive rows carrying a proposal, on
the SAME 505,000 rows:

| shape | size | B | E | F |
|---|---|---|---|---|
| partial `WHERE status='submitted' AND …` | **1,480 kB** | **0.027** | **1.07** | 39.6 |
| partial `WHERE suggested_status IS NOT NULL` | **10,216 kB** | **8.39** | **15.01** | 0.034 |

The index that had looked equal is now seven times larger on the same rows and
fifteen times slower on the operator's count — and it will keep growing, because
what it indexes is the archive. The first measurement was not wrong about the
numbers; it was taken on a shape that stops being the real one after a month.

## 5. The other finding: the job's query degrades as the job succeeds

C and G both ask for the reviews with NO proposal, and both are cheap while
there is a backlog — 0.026 ms and 7.7 ms — because the answer is near the top
of the index. In the steady state, where the job has proposed on everything
waiting, there is nothing to find and the scan walks the whole queue: 11.4 ms
and 12.1 ms.

Neither is fixed and both are accepted, for reasons that differ. G runs every
fifteen minutes, so twelve milliseconds is not a cost anybody can measure. C is
the operator's "what has the model not reached yet", and its slow case is the
one that answers "nothing" — the good news. An index over the unproposed queue
would fix both and would be an index whose whole content is the work the job has
not done yet: empty exactly when the system is healthy, and largest when it is
behind.

## 6. What this does NOT measure

- Write cost. The index is maintained on every insert into `reviews` and on
  every proposal; no measurement was taken and none is claimed.
- Concurrency. Every figure is a single connection against an idle database.
- The 505,000-row figures in the module's own migration. They were taken on a
  database that no longer exists and over 20,001 products rather than this
  rig's 20,204; this report does not replace them and does not confirm them.
