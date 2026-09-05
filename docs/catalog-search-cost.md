# What the catalog search costs — measured 2026-09-05, extended 2026-09-06

A measurement record, not a decision record and not a gap inventory. It answers
one question with numbers: what does the product read layer's new free-text
filter cost on a real catalog, and at what size does that answer change.

This file is new because there was nowhere to put it. `docs/gaps.md` carries
measurements, but it is an inventory of what the repository has and has not;
`docs/mimari.md` describes the architecture. A measurement is neither, and the
next person to measure a read path should have a place to append to rather than
a second new file to invent.

This file is in English because ADR 0012 makes language a property of the file
and every new file is English.

**It now carries TWO rounds.** Round one, 2026-09-05, measured the text search
and could only BOUND the category filter, because the rig it ran on held no
taxonomy rows at all. Round two, 2026-09-06, measured the category filter for
real on a rig rebuilt from the repository, and it overturned two sentences round
one had written. Nothing from round one is deleted: where it was wrong the
sentence is struck in place and the correction stands next to it, because a
figure with no visible argument against it is a figure nobody re-measures.

---

## The claim being tested

The read layer answers a `q` filter by putting the term into
`ProductFilter.Search`, and the shared filter body of the listing and the count
turns it into `title ILIKE '%' || $4::text || '%'`
(`internal/modules/product/repository/saleschannel.go`). Two structural facts
were checkable without running anything, and both hold:

- The product table carries no index whose leading column is `title`. The
  module's schema creates a unique partial index on `handle`, one on `status`,
  one on `collection_id` and the listing's own `(created_at DESC, id DESC)`, and
  no trigram, full-text or expression index anywhere
  (`internal/modules/product/migrations/000001_product_init.up.sql`). The rig's
  live index set was compared against that file and matches it exactly, which
  matters because the rig was built months before the search was.
- The predicate carries a LEADING wildcard, which no B-tree can serve even if
  one existed on `title`.

From those two facts the expected conclusion is "the search is a sequential
scan and therefore slow". That conclusion is half right, and the half that is
wrong is the half that decides what to do about it.

---

## The headline

**At 52,004 products the search is not slow — it is slow only for the terms
that match few products, and it is those searches that are the point of a search
box.** A term matching almost the whole catalog is answered in 0.03 ms because
the listing stops after 25 matches. A term matching one product costs 9.1 ms
and reads the entire table, because there is nothing to stop early for. The cost
does not follow the term, it follows how far down the ordering the page's last
match sits.

**The count is where the money already was, and the search moves it in BOTH
directions.** With the sales channel filter on, an unfiltered count is about
74 ms; add a term matching one product and it drops to about 13 ms, because the
`ILIKE` runs before the per-row visibility subplan and removes 52,003 of its
52,004 invocations. Add a term matching almost everything and it rises to about
84 ms.

**One plan degrades silently under a prepared statement.** From the sixth
execution of the storefront listing on a connection, PostgreSQL switches to a
generic plan and the selective search goes from 14.4 ms and 734 buffers to about
25 ms and 10,982 buffers, and stays there.

---

## The rig, and what it cannot say

`gobit_load` in the `gobit-postgres` container. PostgreSQL 16.14 on x86_64,
`shared_buffers` 128 MB, `work_mem` 4 MB, 16 cores on the host. 52,004 products,
54,000 variants, 52,000 rows in the sales channel link table. Statistics were
already analyzed; nothing in the rig was written to, and the row counts and the
database size were re-read afterwards and are unchanged.

The product table is 15 MB in total (5,840 kB heap, 9,824 kB indexes), so it
fits in `shared_buffers` several times over. **Every figure below is warm**: the
plans report `shared hit` and no `read` after the first execution of a session.
A cold cache was not measured and cannot be simulated from inside the container;
on a cold cache the same sequential scan has to pull its 730 pages from disk
first, and none of these numbers apply.

Every time below is the median of six warm executions of the same prepared
statement in one session, with the first execution of the session discarded.
**The machine is shared and the numbers drift.** Two batches taken about an hour
apart agreed to within a few percent on the cheap statements and differed by up
to ten percent on the expensive ones — the channel-filtered count came out at
67.7 ms in one batch and 74 ms in the other. Where the two batches disagree the
text says "about". Buffer counts, plan node types and row counts do NOT drift
and are quoted exactly; they are the part of a measurement worth trusting to
three digits.

Four properties of the rig limit what it can answer, and each of them makes the
measured numbers OPTIMISTIC rather than pessimistic:

- Every product is `published` and none is soft-deleted, so the status filter
  and the `deleted_at IS NULL` predicate remove nothing.
- There is exactly one sales channel and 52,000 of the 52,004 products are in
  it, so the visibility filter removes almost nothing either.
- **There are zero rows in `product_category`, `product_category_map` and
  `product_tag_map`, and no product has a `collection_id`.** The category
  composition therefore could not be measured properly; see its section below.
- The rows are unusually narrow. Titles average 15.5 characters and are of two
  shapes, `Urun N` and `Buyuk Urun N`; every description is empty. A real
  catalog fits fewer rows per 8 kB page and gives `ILIKE` more characters to
  walk per row, so a real catalog costs MORE per row than this one. How much
  more is not known and is not guessed at here.

The statements measured are the real ones. `listProductsSQL` and
`countProductsSQL` were extracted mechanically from
`internal/modules/product/repository/saleschannel.go` — the string constants
concatenated the same way the Go source concatenates them, including the sales
channel template substitution — rather than retyped, so a transcription slip
could not quietly change what was being measured. They were run through
`PREPARE` and `EXPLAIN (ANALYZE, BUFFERS) EXECUTE` with the same parameter
positions the repository passes: `$1` status, `$2` collection, `$3` handle,
`$4` search, `$5` category, `$6` tag, `$7` channels, `$8`/`$9` limit and offset,
`$10`/`$11` cursor.

Terms used. "Broad" is `urun`, which matches 52,000 of 52,004 titles.
"Selective" is `Buyuk Urun 43707`, which matches exactly one. Two intermediate
terms match 111 and 11 titles.

---

## The listing, with no sales channel

This is the panel's path and the cross-module read path: `productProvider.List`
does not apply a channel filter at all, and `internal/adminui/catalog.go`
reaches the catalog only through the read layer. `LIMIT 25`, `OFFSET 0`.

| filter | plan | rows the scan touched | time | buffers |
| --- | --- | --- | --- | --- |
| none | Index Scan `product_created_at_idx` | 25 | 0.03 ms | 7 |
| q matching 52,000 | Index Scan + `title ~~*` as Filter | 29 | 0.03 ms | 9 |
| q matching 111 | Index Scan + Filter | 12,473 | 2.6 ms | 2,635 |
| q matching 11 | Seq Scan + Sort | 52,004 | 9.7 ms | 730 |
| q matching 1 | Seq Scan + Sort | 52,004 | 9.1 ms | 730 |

The first two rows are the same number, and that is not a rounding accident:
they differ by two buffers and the difference in time is below this rig's noise
floor. **A broad search is free.** The shape of the rest of the column is the
finding. The listing has an ordering index and a `LIMIT`, so the planner's first
choice is to walk `(created_at DESC, id DESC)` and stop as soon as 25 rows have
passed the filter. With a broad term the 25th match is 29 rows in. With a term
matching 111 products the scan has to walk 12,473 index entries to collect 25 of
them, and the buffer count rises by a factor of 290. Below roughly a hundred
matches the planner stops trying: it estimates that the ordered walk would have
to consume most of the table anyway, switches to a sequential scan with an
explicit sort, and pays the full 730 pages.

So there is no single answer to "how fast is the search". There are two regimes
with a planner-chosen boundary between them, and the expensive regime is the one
an operator typing a product name lands in.

---

## The listing, with the sales channel filter

The storefront path. Same limit and offset, channel array set to the rig's one
channel.

| filter | plan | time | buffers |
| --- | --- | --- | --- |
| none | Index Scan + visibility SubPlan, 25 loops | 0.08 ms | 83 |
| q matching 52,000 | Index Scan + SubPlan, 25 loops | 0.08 ms | 85 |
| q matching 1, custom plan | Seq Scan + Sort, SubPlan 1 loop | 14.4 ms | 734 |
| q matching 1, generic plan | Index Scan walking the whole index | about 25 ms | 10,982 |

The visibility subplan is cheap here for the same reason the search is cheap in
the broad case: it runs once per row the scan actually returns, and the scan
returns 25. It becomes expensive only where nothing stops the scan early, which
is the count.

### The generic plan is not hypothetical, and it is not the cursor's problem

The last row is a plan the repository has met before. The godoc of
`listProductsSQL` records why the cursor bound is a `COALESCE` sentinel and not
an `OR`: PostgreSQL plans a prepared statement per call for its first five
executions and then considers a GENERIC plan, and an `OR` that folds away in a
custom plan survives into a Filter in the generic one. The search has now
reproduced that mechanism on a different clause.

Measured by running ten executions of the same prepared statement in one
session with `plan_cache_mode` at its default `auto`: executions one through
five use a custom plan — Seq Scan, 734 buffers, 13.9 to 14.4 ms. From execution
six onward the plan is the generic one — Index Scan over the whole
`(created_at DESC, id DESC)` index, 10,982 buffers, 24.3 to 25.8 ms — and it
does not switch back. Forcing `plan_cache_mode` to `force_generic_plan`
reproduces the same plan and the same figures directly, and shows why: in the
generic plan every `$n IS NULL OR ...` branch survives into the Filter,
including the search, so the term can no longer be used to choose a plan.

Three qualifications, because this is easy to overstate:

- It happens only with the channel parameter set. Without it, nine consecutive
  executions of the same statement kept the Seq Scan plan and 8.8 to 9.9 ms.
- It happens only to the LISTING. The count kept its plan across ten
  executions with the channel set; a count has no ordering index to be tempted
  by.
- Which plan a connection ends up on depends on the terms its first five
  executions carried, because `auto` compares the generic plan's estimated cost
  against the AVERAGE of the custom ones. That makes it a source of variance
  between connections rather than a constant tax, and variance that depends on
  history is the hardest kind to reproduce from a bug report.

---

## The count

The count has no `LIMIT`, so nothing can stop it early and none of the
two-regime behavior above applies to it.

Without the sales channel filter:

| filter | plan | time | buffers |
| --- | --- | --- | --- |
| none | Index Only Scan `product_status_idx` | 3.3 ms | 44 |
| q matching 52,000 | Seq Scan | 12.0 ms | 730 |
| q matching 111 | Seq Scan | 10.4 ms | 730 |
| q matching 1 | Seq Scan | 10.1 ms | 730 |

With it:

| filter | plan | time | buffers |
| --- | --- | --- | --- |
| none | Seq Scan + SubPlan, 52,004 loops | about 74 ms | 156,743 |
| q matching 52,000 | Seq Scan + SubPlan, 52,000 loops | about 84 ms | 156,731 |
| q matching 1 | Seq Scan + SubPlan, 1 loop | 12.9 ms | 734 |

The second table is the interesting one, and it says something the "no index on
title" premise does not predict: **a selective search makes the channel-filtered
count about five times CHEAPER.** PostgreSQL orders the filter clauses by
estimated cost and puts the `ILIKE` ahead of the correlated visibility
subquery, so 52,003 of the 52,004 subplan invocations never happen and the
buffer count falls from 156,743 to 734. The search does not add a new wall to
the count. The wall was already there — it is the per-row visibility probe,
which the godoc of `countProductsSQL` and the godoc of
`ListProductsOptions.SkipCount` already measured and wrote down — and a
selective term walks straight past it. A broad term is the case that makes it
worse, by about 10 ms.

This is also the shape that makes the request-level number worth stating.
REST's `with_count` defaults to true (`internal/modules/product/api/store.go`),
so a storefront search that does not opt out issues both statements: about
27 ms for a selective term (14.4 + 12.9), and about 84 ms for a broad one
(0.08 + 84). GraphQL runs the count only when the `count` field is selected, and
the panel never runs it at all — it asks for one row more than the page and
reads the answer off the length.

---

## Offset, which the panel composes with the search

The panel pages by `OFFSET`, and its search is carried in the address across
those pages, so the two compose. Broad term, no channel:

| offset | time | buffers |
| --- | --- | --- |
| 0 | 0.03 ms | 9 |
| 2,475 (page 100) | 0.6 ms | 529 |
| 51,975 (the last page) | 12.4 ms | 10,978 |

The same last page with no search at all is 5.2 ms, so the search roughly
doubles the deep-offset case rather than changing its class: the offset already
makes the database walk and discard everything it skips, and the `ILIKE` is
evaluated on each row it walks past. This is the cost `ListProductsOptions.After`
exists to avoid, and the panel does not use it.

---

## The category composition — measured as a SHAPE, not as a cost

**SUPERSEDED 2026-09-06. Read this section for the plans and then read round
two, which measured what this one could only bound.** The floor was a floor and
is now a number; one of the two conclusions drawn here turned out to be false at
the rig's own selectivity, and the other is true only under a plan the shipped
statement never actually got. Both are struck below, in place.

The panel's search box and its category dropdown are one form on purpose, so
"search inside a category" is the request the box was built for. It could not be
measured honestly here, and the reason is worth stating plainly rather than
burying: **the rig contains no categories at all** — zero rows in
`product_category` and zero in `product_category_map`. Every figure in this
section is therefore a FLOOR, produced against an empty map table, and a
populated one can only be more expensive.

What the plans do say, with a category id set:

| statement | plan | time | buffers |
| --- | --- | --- | --- |
| listing, q matching 52,000 + category | Index Scan over the whole index, SubPlan 52,004 loops | 29.3 ms | 10,978 |
| listing, category alone | same shape | 27.4 ms | 10,978 |
| listing, q matching 1 + category | Seq Scan + Sort, SubPlan 52,004 loops | about 28 ms | 730 |
| count, q matching 52,000 + category | Seq Scan, SubPlan 52,004 loops | 27.8 ms | 730 |
| count, q matching 1 + category | Seq Scan, SubPlan 52,004 loops | about 29 ms | 730 |

Two things survive the rig's emptiness, because they are properties of the plan
rather than of the data.

~~**The category never narrows the scan.** The subquery runs once per catalog
row — `loops=52,004` in every row of that table — whatever the search does, and
it runs even in the rows where the search has already excluded the product,
because the planner put it first. Adding a category to a search that cost
0.03 ms makes it cost at least 29 ms, and that is with an EMPTY table on the
inner side; with real memberships each of those 52,004 loops becomes an index
probe instead of a scan of nothing.~~

**Struck 2026-09-06: FALSE, and the emptiness of the map table is exactly what
made it look true.** With rows on the inner side and a category id in the
statement, the planner hashes the whole subquery ONCE per statement rather than
once per row — a "hashed SubPlan" over a single bitmap index scan, `loops=1`,
4 buffers — and the filter then removes 49,404 of the 52,004 rows before the
channel subquery is evaluated at all. At the rig's own selectivity the category
filter makes the channel-filtered count FASTER than no filter (16.5 ms against
71 ms), not slower. The prediction "each of those 52,004 loops becomes an index
probe" describes a plan that was never chosen. What survives is the second half
of the sentence in a form this section could not see: the per-row shape does
happen, but only when the planner orders the CHANNEL subquery first, and then it
costs 147 ms rather than 29.

**The reason is the `OR`, and it is measurable read-only.** The filter body
spells every optional predicate as `($n::text IS NULL OR ...)`, and for the
taxonomy filters the second half of that `OR` is an `EXISTS`. PostgreSQL pulls
an `EXISTS` in a `WHERE` clause up into a semi-join, but it does so BEFORE it
folds constants, so an `EXISTS` wrapped in an `OR` is never a candidate. Two
plans, same data, same session:

```
-- bare EXISTS
Aggregate
  ->  Nested Loop
        ->  Seq Scan on product_category_map
              Filter: (category_id = 'pcat_x'::text)
        ->  Index Scan using product_pkey on product
              Index Cond: (id = product_category_map.product_id)

-- the same EXISTS inside ('pcat_x'::text IS NULL OR ...)
Aggregate
  ->  Seq Scan on product
        Filter: ((deleted_at IS NULL) AND (SubPlan 1))
        SubPlan 1
          ->  Seq Scan on product_category_map
                Filter: ((product_id = product.id) AND (category_id = 'pcat_x'::text))
```

The first plan is driven FROM the category — it starts at the map and looks up
the products, which is the shape that would make a narrow category cheap no
matter how large the catalog grows. The second is the one the repository gets.
~~The index the schema creates on `category_id` alone is unreachable from the
query as written.~~

**Struck 2026-09-06: true only under a GENERIC plan, which this statement never
got.** Re-measured with rows in the map table: under the default
`plan_cache_mode` the shipped statement is re-planned on every call, the planner
sees the literal category id, folds the disjunction away and reaches
`product_category_map_category_idx` through a hashed subplan. Forced to a
generic plan it does exactly what the sentence says — the branch survives into
the Filter and the index goes unused, 124.9 ms and 156,743 buffers. The
structural half of the paragraph above holds unchanged: the sublink is never
pulled up into a semi-join, in either mode. What was wrong was the consequence
drawn from it. The narrower and stranger truth is that the index was reachable
only because the statement could never be cached.

This is a lead, not a recommendation, and three things stand between it and a
change. It is a property of the SHARED filter body, so it cannot be changed for
the category without deciding what happens to the other six optional
predicates; the same `OR` idiom is what lets one statement serve every
combination of filters, which is a virtue this measurement does not get to
trade away on its own; and the rig cannot say what the semi-join would actually
cost, because there is no category data to join to. What is established is only
that the current shape cannot use the index, and that this is caused by the
`OR` rather than by the data.

**The lead was taken on 2026-09-06 and all three of those obstacles were
answered rather than argued around:** the change was made for all seven optional
criteria and not for the category alone, the one statement per criterion
combination was counted rather than feared, and the rig now has category data to
join to. The measurement is in round two below and the change is in
`internal/modules/product/repository/saleschannel.go`.

---

## Where the cost actually goes

The sequential scan was decomposed by turning the index paths off and counting
the same 52,004 rows three ways. Three warm executions each:

| statement | time |
| --- | --- |
| `count(*)` with `deleted_at IS NULL` only | 4.5 to 5.8 ms |
| the same plus `title LIKE '%...%'` | 3.7 to 4.3 ms |
| the same plus `title ILIKE '%...%'` | 8.8 ms |

`LIKE` is faster than no pattern at all only because it removes 52,003 rows
before the aggregate counts them; the scan itself is identical, 730 buffers in
all three. The pair that matters is the last two: **case folding roughly
doubles the match cost**, about 5 ms per 52,004 short titles, or about 100
nanoseconds per row. That is a cost of `ILIKE`, not of the missing index, and no
index removes it — it is what makes the search case-insensitive, which is the
behavior the read layer's godoc and
`docs/adr/0015-postgresql-cluster-contract.md` argue for and what
`db.CaseFolding` probes at startup.

The scan is linear in the number of rows, measured rather than assumed, by
scanning prefixes of the table:

| rows scanned | time |
| --- | --- |
| 10,000 | 2.7 ms |
| 20,000 | 4.7 ms |
| 40,000 | 9.5 ms |
| 52,004 | 12.2 ms |

That is 0.235 microseconds per row across the whole range, with no knee. The
bare statement without the prefix harness comes to 0.18 microseconds per row
(9.1 ms over 52,004). Both slopes hold only while the table stays in memory.

---

## Throughput

`pgbench`, simple protocol, ten seconds per run, warm, with the search term
randomized per transaction so no single result could be cached. The selective
scripts pick one of the 52,004 product names at random each time.

| script | 1 client | 16 clients |
| --- | --- | --- |
| listing, no filter | 0.219 ms, 4,568 /s | 1.384 ms, 11,564 /s |
| listing, broad term | 0.253 ms, 3,956 /s | 1.405 ms, 11,391 /s |
| listing, selective term, no channel (the panel) | 9.891 ms, 101 /s | 18.701 ms, 856 /s |
| listing, selective term, with channel | 13.187 ms, 76 /s | 25.067 ms, 638 /s |
| count, selective term, with channel | 13.182 ms, 76 /s | 25.160 ms, 636 /s |

The unfiltered listing figures are consistent with the pgbench measurement
already recorded in `docs/gaps.md` (0.47 ms and 33,830 per second at 16 clients,
taken with a different script and a different page shape).

The last three rows are the number to carry: **a selective search costs about
forty times the latency and one thirteenth of the throughput of an unfiltered
listing**, and sixteen concurrent selective searches saturate sixteen cores at
around 850 per second. For a panel used by a handful of operators that is
irrelevant. For a storefront search box it is the first ceiling anybody would
hit, and it arrives long before the catalog grows.

---

## So: is it fast enough, and until when

**At this size, yes, and the panel is not the reason to worry.** The panel's
worst case is a 10 ms query on a screen that runs one of them per form
submission and never runs the count. Nothing on that screen needs to change.

**The storefront's search is fast enough today and has the smallest headroom.**
A single-product search is about 27 ms of SQL with the count on, 14 ms with it
off, and about 850 per second across sixteen cores. That is a working search
box, not a fast one.

**Where it stops.** The sequential scan is linear at 0.18 to 0.24 microseconds
per row depending on how it is isolated, measured from 10,000 rows up, so the
selective search costs roughly 20 ms at 100,000 products, 50 ms at 250,000 and
100 ms at 500,000 — an extrapolation from a measured slope, not a measurement,
and it holds only while three things stay true: the rows stay this narrow, the
table stays in memory, and the concurrency stays low. The first of those is
already false for any real catalog, since these titles average 15.5 characters
and these descriptions are empty. The second fails somewhere above a few
hundred thousand products on a default `shared_buffers`, and when it fails the
cost stops being CPU and becomes I/O, at which point none of these numbers
extrapolate at all.

The honest boundary is therefore not a row count but a pair of conditions:
**the search stops being fast enough when the catalog no longer fits in memory,
or when concurrent searches exceed a few hundred per second — whichever comes
first.** On this hardware and this data the second one comes first, and it comes
at a catalog size where the first is nowhere in sight.

---

## Options, none of them taken here

This is a measurement. It does not implement anything and it does not get to
choose. What it can do is name what was measured about each option and what was
not.

**A trigram GIN index on `title` (`pg_trgm`).** The obvious answer: it is the
one index type that can serve a leading-wildcard `ILIKE`. `pg_trgm` version 1.6
is AVAILABLE in this PostgreSQL image and is NOT installed — the only extension
in the rig database is `plpgsql`. Everything else about it is unmeasured, and
deliberately so: creating the extension and the index would be a write to the
rig, which is a measurement instrument nothing in this repository can rebuild,
and creating an index in the product module is a MIGRATION — a schema decision
with a version number, a rollback and a cost on every catalog write, which one
measurement does not get to make alone. What a decision would need before it
could be taken: the index's size against the 9,824 kB of indexes the table
already carries, the write amplification on product create and update, the
build time on a live catalog and therefore whether it needs
`CREATE INDEX CONCURRENTLY`, and its behavior for terms shorter than three
characters, which trigram indexes cannot serve and which a search box receives
constantly.

**Denormalizing into a `tsvector` and a full-text index.** The repository
already owns this shape once, in `plugins/searchpg`. Choosing it for the
in-module filter would mean the same catalog is searched by two different
definitions of "matches", and the storefront could then get different results
from the panel. Not measured; named because the alternative exists inside the
repository and would otherwise look like a free option.

**Leaving it alone.** The default, and the measurement supports it at this
size. It costs nothing, it keeps one definition of the search, and it has a
known ceiling written down above rather than an unknown one.

**Not an option in any case: re-checking the match in Go.** The read layer
already refuses `q` beside an id filter for this reason, and the reason is
measured — `ILIKE` folds case the way the cluster's CTYPE folds it and Go's
`strings.ToLower` does not, so the two disagree on non-ASCII input. See
`docs/adr/0015-postgresql-cluster-contract.md` and `core/db/casefold.go`.

---

## What this measurement does not say

- Nothing about cold caches. Every figure is warm and the plans say so.
- ~~Nothing about a catalog with real categories, tags or collections. The rig
  has none, and the category section above is a floor.~~ **Answered for
  categories and tags by round two below.** Still nothing about collections:
  no product in the rig carries a `collection_id`, in round two either.
- Nothing about wider rows. These titles are 15.5 characters and these
  descriptions are empty; a real catalog is more expensive per row by an amount
  this rig cannot state.
- Nothing about the Go side of the request. The figures are SQL execution time
  and pgbench round trips; the response path was benchmarked separately and is
  recorded in `docs/gaps.md`.
- Nothing about a multi-channel shop. There is one sales channel here and
  almost every product is in it, so the visibility filter removes almost nothing
  and its subplan is at its cheapest.

---

# Round two — 2026-09-06: the category filter, measured instead of bounded

Round one ended two of its sections with the same sentence in different words:
the rig has no taxonomy rows, so this is a floor. Round two exists because that
stopped being true. The rig is rebuildable from the repository now
(`internal/rig/rig.go`, reached as `gobit seed`), and the shape it rebuilds
carries twenty categories and twenty tags.

Round two answers three questions. What does the category filter actually cost.
Whether the `OR` finding round one called "a lead, not a recommendation" is
worth acting on. And which of the performance sentences already written in the
product module's godoc still reproduce — because the rebuild made them
falsifiable for the first time, and a sentence nobody can check is not a
measurement.

---

## The instrument, and the two things that had to be verified before using it

**The cluster folds case, and that was checked rather than assumed.** Round one
ran on `gobit_load` in `gobit-postgres`, whose data directory was `initdb`'d
with a C locale and which therefore FAILS the probe the repository runs at
startup (`core/db/casefold.go`, ADR 0015). One clause under measurement here is
an `ILIKE`, and case folding is the one operator cost that comes from the
cluster rather than from anything this repository controls. The probe was run
verbatim on every cluster available, its two halves being an `ILIKE` between an
uppercase Turkish letter and its lowercase form and the same pair through
`to_tsvector` and `websearch_to_tsquery`:

| cluster / database | datcollate | datctype | ILIKE folds | full text folds |
| --- | --- | --- | --- | --- |
| gobit-postgres / gobit_load | C | C | no | no |
| gobit-postgres / a fresh scratch database | C | C | no | no |
| gobit-searchbench / bench | C | C.UTF-8 | yes | yes |
| gobit-searchbench / the round-two rig | C | C.UTF-8 | yes | yes |
| gobit-pg-utf8 / gobit_tr | C | C.UTF-8 | yes | yes |

All three containers are postgres:16-alpine running PostgreSQL 16.14, with
identical planner settings: `shared_buffers` 160 MB, `work_mem` 4 MB,
`effective_cache_size` 5,120 MB, `random_page_cost` 4,
`max_parallel_workers_per_gather` 2, `jit` on. Note that the first of those
differs from round one's stated 128 MB; round two ran on a different container
and says so rather than inheriting the sentence.

Round two therefore ran on `gobit-searchbench`, on a scratch database seeded for
the purpose. Two operational notes worth writing down because they cost time.
`gobit-searchbench` publishes NO host port, so it has to be reached at its
bridge address, which is read out of `docker inspect` and CHANGES when the
container restarts; `gobit-pg-utf8` on the loopback at port 5434 is the
same-locale alternative with a stable port. And a fresh scratch database is the
right way to get a rig: `rig.Reset` takes about four and a half minutes to
delete what `rig.Seed` writes in thirteen seconds, so nothing here reset
anything.

**The rebuild produces the rig, and that was checked against the recorded
plan rather than against a row count.** Seeding reported `seeded in 13.35s`,
17.27 s of wall clock including migrations and module bootstrap; the same
command against a fresh database on `gobit-postgres` took 11.91 s and 15.80 s.
What it built:

| table | rows |
| --- | --- |
| product | 52,004 |
| product_variant | 54,000 |
| price_set / price | 54,000 / 58,000 |
| inventory_items / inventory_levels | 54,000 / 54,000 |
| link_product_sales_channel | 52,000 |
| link_product_variant_price_set | 54,000 |
| link_product_variant_inventory | 54,000 |
| product_category / product_category_map | 20 / 52,000 |
| product_tag / product_tag_map | 20 / 52,000 |

The acceptance check is the one the rebuild ships with, and it is a PLAN and not
a stopwatch. `countProductsSQL`, prepared, warmed, under
`EXPLAIN (ANALYZE, BUFFERS)`: `Seq Scan on product` at `rows=52004`, the
subquery an `Index Only Scan using link_product_sales_channel_pkey` at
`loops=52004`, `Heap Fetches: 0`, `Buffers: shared hit=156743` of which 156,013
the subquery's. Those are the four numbers the product module's godoc records,
and every one of them matched to the digit. The `VACUUM (ANALYZE)` that makes
`Heap Fetches: 0` possible is done by the seeder itself, after the commit.

So every figure below was taken on a database that is provably the rig rather
than a lookalike, on a cluster that folds case the way a shop's customers would
be served.

---

## The rig's taxonomy is PERFECTLY UNIFORM, which is why none of this was ever seen

Every one of the twenty categories holds exactly 2,600 products — 5.0% of the
catalog — and so does every one of the twenty tags. Not approximately: the
largest and the smallest are both 2,600, at both ends of the sorted count.

This is not an accident of the data, it is the generator's rule.
`internal/rig/catalog.go` maps product n to category number `(n - 1) % C + 1`,
and 50,000 products of one family plus 2,000 of another divide by twenty
exactly. The four hand-made products carry no taxonomy row at all, which is why
there are 52,000 map rows and not 52,004.

**A uniform taxonomy cannot produce the case that decides half of this round.**
"A category holding few products" — a storefront's newest collection, a shop's
one clearance category — does not exist in the default shape and cannot be asked
for: `rig.Spec` takes a category COUNT and has no skew option. So the selective
end was built by hand on the scratch database, as `pcat_SMALL` and `pcat_TINY`,
26 products each, 0.05% of the catalog, VACUUM ANALYZEd like the rest. **Every
figure taken against them says so, and a writer quoting one must say that the
rig does not contain such a category.** If the selective case is ever to become
a permanent fixture it needs an option on `rig.Spec`, and that is a change to
the generator rather than a note in a measurement.

---

## The category filter at the rig's own selectivity — the floor, replaced

2,600 of 52,004 rows. Medians of seven warmed `EXPLAIN (ANALYZE, BUFFERS)`
executions of a prepared statement, `plan_cache_mode` left at its default
`auto`, which is what pgx gets. The statement is the one the repository shipped
on the morning of 2026-09-06 — every optional criterion wrapped in
`($n IS NULL OR …)`.

| statement | time | buffers |
| --- | --- | --- |
| count, category alone | 11.53 ms | 1,117 |
| count, category + channel | 16.46 ms | 8,918 |
| count, no criterion + channel (for reference) | 71.19 ms | 156,743 |
| count, category + a q matching one product + channel | 20.67 ms | 738 |
| count, category + a q matching 50,000 + channel | 30.96 ms | 8,618 |
| list LIMIT 20, category alone | 0.67 ms | 473 |
| list, category + channel | 0.71 ms | 534 |
| list, category + a q matching one product + channel | 23.35 ms | 10,985 |
| list, category + a q matching 50,000 + channel | 0.82 ms | 534 |

The plans, because they are the part worth trusting to three digits. The count
with a category alone is a `Seq Scan on product` whose Filter reads
`(deleted_at IS NULL) AND (hashed SubPlan 2)` and reports
`Rows Removed by Filter: 49404`; SubPlan 2 is a `Bitmap Heap Scan on
product_category_map` returning 2,600 rows in 387 buffers
(`Heap Blocks: exact=383`), driven by a `Bitmap Index Scan on
product_category_map_category_idx` costing 4 buffers, at `loops=1`. Add the
channel and the same hashed subplan is joined by the correlated `bool_or`
subquery at `loops=2600` — not 52,004 — for 7,801 buffers. The list is a
`Limit` over an `Index Scan using product_created_at_idx` with the hashed
subplan as its Filter, walking 375 rows past to collect 20.

**Three findings, and the first one is the one that replaces the floor.**

**At 5% selectivity the category filter is CHEAP, and the index IS used.** One
bitmap index scan per statement, not one probe per row. Round one predicted the
opposite and could not have seen otherwise: with an empty map table there is
nothing for a hash to be built from.

**The category filter makes the channel-filtered count FASTER than no filter at
all** — 16.5 ms against 71 ms — because the hashed subplan prunes 49,404 rows
before the channel subquery is ever evaluated. That is the same mechanism round
one measured for a selective `q`, arriving from a different clause.

**The tag filter mirrors the category filter at every point, within noise.**
Count alone 11.88 ms / 1,117 buffers, with the channel 16.53 ms / 8,918, list
alone 0.75 ms / 473, list with the channel 0.69 ms / 534, with a selective q
19.28 ms / 738, with a broad q 27.58 ms / 8,618. The only differences in the
plan are the index name and a row estimate of 2,694 against 2,583. Below this
point the tag path is re-measured only once, at the selective end; everywhere
else, where the text says "category" the tag behaves the same and is not quoted
twice.

Stability, so the digits are not over-read: the count figures spread 2 to 10%
run to run and their medians are reliable to about three percent; the
sub-millisecond list figures have an absolute noise floor near 0.02 ms and
should be quoted as "about 0.7 ms", never to three digits.

---

## The selective end — where the shipped statement collapses

`pcat_TINY`, 26 products of 52,004, hand-built as described above. Same
statement, same session, same data; the only thing that changed is the category
id.

| statement | shipped OR form | bare EXISTS | ratio |
| --- | --- | --- | --- |
| count + channel | 146.98 ms, 156,746 buffers | 0.13 ms, 186 buffers | 1,131x |
| list LIMIT 20 + channel | 54.70 ms, 142,738 buffers | 0.16 ms, 186 buffers | 342x |
| list LIMIT 20, no channel | 4.36 ms, 9,393 buffers | 0.09 ms, 107 buffers | 48x |

The mechanism is legible in the Filter. The shipped count reads
`(deleted_at IS NULL) AND COALESCE((SubPlan 3), true) AND (hashed SubPlan 2)` —
the CHANNEL subquery is ordered first, so it runs `loops=52004` and spends
156,013 buffers, while the category subplan that would have answered the whole
question sits behind it and returns 26 rows in 3 buffers. The bare form is a
`Nested Loop` driven by a `Bitmap Heap Scan on product_category_map`, 26 rows,
probing `product_pkey`, with the channel subplan at `loops=26`. Reproduced on
`pcat_SMALL` (139.84 ms against 0.13 ms) and on the tag path (148.16 ms).

**This is the finding that decided the change.** A storefront category page for
a small category was a 147 ms count and a 55 ms first page, and both are about
0.15 ms once the clause is appended conditionally instead of being wrapped in a
disjunction.

**And it is invisible at the rig's own uniform 5% taxonomy**, which is exactly
why nobody had seen it. The same statement, the same catalog, one different
category id: 16.5 ms or 147 ms.

**One sharpening, from a second batch and worth more than the number it
corrects.** The implementation round re-measured the same collapse on its own
bench and found that the magnitude is not a property of the category's SIZE
alone. A 26-product category whose products are ADJACENT in the listing order
came out at 12.5 ms and 812 buffers, while a 27-product category spread every
2000th row came out at 163.5 ms and 156,746 buffers — the second plan being the
one measured above. Both plans are legal, both come out of one statement, and
which one the planner picks is decided by statistics. So the honest statement of
the defect is not "the OR form costs 147 ms at a small category"; it is **the OR
form's cost is not a figure at all, it is a coin the planner flips**, and the
losing side of that flip runs the correlated channel subquery 52,004 times to
return 26 rows. That is worse than a slow number, because a slow number can be
budgeted for.

---

## What round one got right about the `OR`, and what it got wrong

Round one wrote that an `EXISTS` under a disjunction is never pulled up into a
semi-join, and concluded from that that the index on `category_id` is
unreachable. **The first half reproduces exactly. The second half is false as
stated, and the truth is stranger.**

| plan_cache_mode | Filter | index | time | buffers |
| --- | --- | --- | --- | --- |
| auto (the default, and what pgx gets) | `(deleted_at IS NULL) AND (hashed SubPlan 2)` | `product_category_map_category_idx`, `loops=1` | 11.53 ms | 1,117 |
| force_generic_plan | `($5 IS NULL) OR (SubPlan 1)` | the map table's primary key, `loops=52004` | 124.91 ms | 156,743 |

Never a `Hash Semi Join`, in either mode — that part of round one stands. But
under a custom plan the literal category id is IN the plan, so the planner folds
the `IS NULL` branch away and reaches the index through a hashed subplan.
Under a generic plan it cannot, and there the index really is unreachable.

**So the index was reachable only because the statement was re-planned from
scratch on every single call** — and that is not a figure of speech. Measured
directly, from `pg_prepared_statements` after 60 executions of the shipped count
(30 with a category, 30 without) and 30 of a bare-EXISTS control:

| statement | generic plans | custom plans |
| --- | --- | --- |
| shipped count, OR-wrapped | 0 | 60 |
| bare EXISTS count, no channel | 25 | 5 |

The reason is in the costs. The shipped statement's generic plan costs 695,113,
because with seven criteria that might all be live it has to plan all three
sublinks as per-row subplans; any of its custom plans costs 231,952 to 232,538.
`plan_cache_mode=auto` adopts the generic plan only when it is no dearer than
the average custom one, so this statement never will.

Two consequences, and both are counter-intuitive enough to be worth writing
where a reader will find them. **The OR-wrapped body was SAFE from the
generic-plan trap** — the trap round one measured on the search term, and the
trap the cursor's `COALESCE` sentinel exists to dodge — and it was safe for the
same reason it was slow. **And it paid a full re-plan on every call:** measured
median planning time for the shipped count is 0.14 to 0.94 ms (0.54 to 0.94 ms
for the first `EXPLAIN` in a session, 0.12 to 0.24 ms after), against 0.008 to
0.017 ms for the bare form once it goes generic. Roughly 0.15 ms per request
that a conditionally built statement stops paying.

---

## The whole comparison, OR-wrapped against bare, on the same data

Category `pcat_7`, 2,600 rows, 5.0%. "custom" and "generic" are the two
`plan_cache_mode` settings, measured rather than reasoned about, because the
whole risk of removing the `OR` is that the cheaper statement becomes eligible
for a cached plan that cannot see which category was asked for.

| statement | OR-wrapped | bare EXISTS |
| --- | --- | --- |
| count, no channel, custom | 11.53 ms, 1,117 | 6.59 ms, 1,117 |
| count, no channel, generic | 124.91 ms, 156,743 | 7.83 ms, 1,117 |
| count + channel, custom | 16.46 ms, 8,918 | 7.45 ms, 18,588 |
| count + channel, generic | not adopted | 8.12 ms, 18,588 |
| list, no channel, custom | 0.67 ms, 473 | 0.35 ms, 1,272 |
| list, no channel, generic | 7.71 ms, 1,272 | 7.82 ms, 1,117 |
| list + channel, custom | 0.71 ms, 534 | 1.07 ms, 2,458 |
| list + channel, generic | not adopted | 9.38 ms, 18,588 |

Three things in that table were not obvious before it existed.

**The bare form reads MORE buffers on the channel-filtered count and is still
twice as fast.** 18,588 against 8,918, 7.45 ms against 16.46 ms. Buffers are not
a proxy for time here; the driving relation is.

**The bare form's plan does not degrade when it goes generic on the COUNT.**
Same `Bitmap Index Scan`, same buffer count, `Index Cond: (category_id = $1)`
instead of a literal — 7.83 ms against 6.59 ms. There is nothing for a generic
plan to lose, because the shape does not depend on which category was named.

**The CHANNEL-FILTERED LIST is the one place the OR form wins, and it wins for a
reason that could change.** At 5% with the channel on, the shipped form is
0.71 ms and the bare form 1.07 ms — the only cell in the table where that
direction holds, and the one that matters most, because it is the storefront's
category page. If the bare form's LIST ever adopted a generic plan it
would be 9.38 ms, because a generic plan cannot let the `LIMIT` stop early.
**A rewrite that stopped at "remove the OR" would trade a rare catastrophe for a
permanent regression on the storefront's hottest query.** That risk was measured
and not assumed: after the change, `pg_prepared_statements` reports the LIST
statements at 0 generic / 30 custom on every shape tried, and the COUNT
statements do flip to generic at 25 / 5 and lose nothing by it. The reason the
LIST does not flip is a cost comparison rather than a guarantee, which is why
the module's godoc names the query that would detect it changing.

---

## The decision that was taken

**The filter body now writes only the clauses the request carried.** A criterion
that was not given produces NO SQL and consumes NO parameter;
`productFilterSQL` returns the body and the arguments it stands for as one pair,
so the listing and the count cannot disagree about the numbering. The
implementation, the full before-and-after table on the implementation's own
bench, and the three prices this cost — parameter numbers stopped being fixed,
prepared-statement reuse changed shape, and the statement can no longer be read
out of a single string by grep — are all in the godoc of
`internal/modules/product/repository/saleschannel.go`. They are not repeated
here, because a measurement record that duplicates a godoc is a second copy that
drifts.

What belongs here is the shape of the verdict this measurement supports.

**It is a real win and it is not a free one.** At the request shape the
storefront serves most often — no taxonomy criterion at all — the two forms
produce the same plan, the same buffers and the same milliseconds, so nothing
was given up to buy the rest. At the rig's own uniform 5% the change buys
between 1.4x and 2.4x. At a skewed taxonomy it buys between 34x and 586x on the
implementation's bench, and between 48x and 1,131x on the hand-built categories
this file measured — two batches, one conclusion, and the gap between them is
the coin flip described above rather than an error. **It is the SKEW that pays
for this, and the rig has none** — which is the same
sentence as "this defect was invisible on the only catalog the repository could
measure", written from the other side.

**The honest scope.** This measurement supports the change for the count
unreservedly and for the list conditionally, on the evidence that the list
statements do not currently adopt a generic plan. The condition is checkable in
one query and the godoc names it. If it ever stops holding, the symptom is a
storefront list an order of magnitude slower with no code change.

---

## Sentences elsewhere in the repository that this round checked

The rebuild made a class of claim falsifiable that had not been falsifiable
before: figures written months ago against a database nothing could reconstruct.
Eight recorded figures were re-run. **Three reproduce and five do not**, and the
three that reproduce are all STRUCTURAL — plan nodes, loop counts, rows removed
— while every one of the five that failed is a millisecond. Each was corrected
at its source on the same day; what follows is the record of what was observed,
not the correction itself.

**Reproduces exactly.** The count's recorded plan: `rows=52004`,
`loops=52004`, `Heap Fetches: 0`, 156,743 shared hits of which 156,013 the
subquery's — every structural number, in every run, on both the OR-wrapped and
the conditionally built statement.

**Reproduces exactly, and the millisecond needed a nudge.** The rejected
`OR IS NULL` cursor bound: `Rows Removed by Filter: 50001` — exact — at a cursor
sitting 50,000 rows deep. The recorded 4.3 ms measured 5.24 ms and 10,629
buffers under a generic plan, against the shipped `COALESCE` sentinel at 0.07 ms
and 70 buffers, which keeps its `Index Cond` even when forced generic. Under a
custom plan the two forms are indistinguishable, 0.08 against 0.07 ms — which is
the recorded claim's own point about why a test would not catch it.

**Direction right, both magnitudes wrong.** The two-EXISTS visibility rule
against today's single `bool_or`, recorded as "26.8 ms against 0.8 ms". The
mechanism reproduces verbatim: both sublinks become a full `Seq Scan on
link_product_sales_channel` over 52,000 rows before the first row leaves the
plan. The numbers do not. Measured at LIMIT 20: 22.71 ms against 0.07 ms as
`EXPLAIN` execution time, 17.90 ms against 0.165 ms as a client round trip. The
likeliest reading is that the two halves of the recorded pair were never read
off the same clock — 0.8 ms has the shape of a round trip and 26.8 ms the shape
of an `EXPLAIN`. **Whichever pair a writer prefers, both halves have to come
from one clock**; that is how a 0.8 ms survived for months next to a query that
costs 0.07 ms.

**The rejected-alternatives block, three claims of which one survives.**
Counting the same 52,004 products three ways, medians of seven, channel filter
on:

| shape | no criterion | recorded | selective q | recorded |
| --- | --- | --- | --- | --- |
| correlated (today's) | 69.63 ms | 62-71 ms | 19.90 ms | 13.8 ms |
| two EXISTS | 53.45 ms | 43-54 ms | 20.84 ms | never claimed |
| GROUP BY + hash join | 51.70 ms | 33-45 ms | 37.72 ms | 30.0 ms |

Four recorded figures in that block, and one of them holds. The two-EXISTS band
holds, at its ceiling. The hash form's band does not — the measured median sits
entirely outside it. "Twice as fast unfiltered" is 1.35x. The "fixed ~30 ms
floor" is about 36 ms, and it is visible in the plan: the `HashAggregate` over
52,000 link rows alone accounts for 24.3 ms of actual time before anything is
joined. Both numbers in the selective column were low.

**The ARGUMENT those numbers were written to support survives every one of the
corrections**, and that is the part worth carrying: the trade CHANGES DIRECTION.
The hash shape lays down a floor no filter can get under, so it is faster than
today's shape when nothing is filtered and slower the moment a criterion is
selective. Correcting the magnitudes does not touch that, and the list query is
obliged to take the correlated shape anyway, because its ability to stop at the
`LIMIT` comes from being correlated.

---

## Two batches, and where they disagree

Round two's figures and the figures in the product module's godoc were taken by
two different runs on two different containers within the same day, and they do
not agree to three digits. The disagreements are stated rather than reconciled,
because reconciling them would mean picking one and deleting the evidence for
the other.

| figure | this file's batch | the module godoc's batch |
| --- | --- | --- |
| unfiltered channel-filtered count | 71.19 ms | about 75 ms, of which 4 ms JIT |
| two EXISTS at LIMIT 20 | 22.71 ms | 21.4 ms |
| single `bool_or` at LIMIT 20 | 0.07 ms | 0.12 ms |
| GROUP BY + hash join, unfiltered | 51.70 ms | 45.5 ms |
| the hash form's floor | about 36 ms | about 39 ms |
| hash against correlated, unfiltered | 1.35x | 1.6x |
| the small-category collapse | 146.98 ms | 12.5 ms adjacent, 163.5 ms spread |

The first six rows differ by up to 12% and are the machine, not the finding:
every structural number underneath them — plan node, driving relation, loop
count, buffer count — is identical across both batches. **The last row is not
the machine and must not be read as noise.** Two categories of the same size
gave two different plans, and that IS the finding; see the sharpening in the
selective-end section above.

One figure the other batch has that this one does not: the `ILIKE` cost of case
folding, measured across the two clusters. A bare count with a leading-wildcard
`ILIKE` over the same 52,004 rows costs 8.7 ms where the folding is broken and
14.5 ms where it works — 1.66x. **Every figure in round one was taken on the
cluster where it is broken**, so round one's search costs are understated: by
1.66x on the one statement where the two clusters were compared directly, and by
an amount nobody has measured on the rest. And the qualitative half is worse
than the ratio. On that cluster an uppercase Turkish letter does not
`ILIKE`-match its own lowercase form, so round one's 9.1 ms selective listing
describes a search that, on a Turkish catalog, would have returned nothing at
all for any word carrying one.

---

## What round two still cannot say

- Nothing about a SKEWED taxonomy that the repository can rebuild. The selective
  categories were hand-built on a scratch database and are gone with it. Until
  `rig.Spec` grows a skew option, the case that motivated the whole change
  cannot be reproduced by running a command.
- Nothing about collections. No product in the rig carries a `collection_id`, so
  the third taxonomy-shaped criterion was never on the inner side of anything.
- Nothing about a product in SEVERAL categories. The generator gives every
  product exactly one category and one tag, so the `EXISTS` never had to stop at
  the first of many matches, which is the reason it is an `EXISTS` and not a
  join.
- Nothing about cold caches, still. Every figure is warm and the plans say so.
- Nothing about a multi-channel shop, still. One channel, 52,000 of 52,004
  products in it.
- Nothing about concurrency. Round two measured single statements under
  `EXPLAIN`; round one's throughput section was not re-run, and its numbers were
  taken on the other cluster.
