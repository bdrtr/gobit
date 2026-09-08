# What the agreement report costs — measured 2026-09-09

Evidence for [ADR 0074](../adr/0074-the-agreement-is-counted-on-read.md). One
question: what does counting the model's agreement with the operators cost, and
does it need an index?

## The rig

The same one section 1 of
[0073](0073-review-suggestion-filter.md) describes, built by
`gobit seed -reviews 505000`:

```
total=505000   evidence=151500
```

"Evidence" is the rows a person has decided about that carry a proposal — the
population this report counts. PostgreSQL 16 in Docker, `VACUUM ANALYZE` first,
best of five `EXPLAIN (ANALYZE, TIMING)` runs.

## The number

**43.0 ms.** The plan is a parallel sequential scan over the whole table with two
workers, sorted and grouped:

```
Finalize GroupAggregate  (actual time=36.949..42.934 rows=1 loops=1)
  Group Key: suggestion_model
  ->  Gather Merge  (actual time=36.940..42.925 rows=3 loops=1)
        Workers Planned: 2  Workers Launched: 2
        ->  Sort  (actual time=27.530..27.531 rows=1 loops=3)
```

One group, because the rig runs one model. A shop that has changed models gets
one row per model and the same scan.

## Why no index was added

The cost is linear in the TABLE and not in the answer, so an index would have to
cover the archive — every review ever decided that carries a proposal, growing
without bound. That is the shape [ADR 0073](../adr/0073-a-proposal-outlives-the-decision.md)
rejected for the queue's filter, and the argument is stronger here: the queue's
filter is read all day and this is read when somebody is deciding whether to
keep paying for a model.

The crossing point is stated rather than hidden. At ten times this table the
report is most of a second, and a shop that puts it on a dashboard refresh is the
case that wants a stored counter — the same sentence the review SUMMARY carries,
and for the same reason.

## What this does NOT measure

- Concurrency. One connection, idle database.
- The report under a table where several models have run. The rig has one, so
  the GROUP BY produces one row; a shop with four would sort four.
- Write cost. Nothing was added to the write path, so there is nothing to
  measure — which is itself the argument for leaving it unindexed.
