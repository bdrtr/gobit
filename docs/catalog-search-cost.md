# What the catalog search costs — measured 2026-09-05

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

**The category never narrows the scan.** The subquery runs once per catalog
row — `loops=52,004` in every row of that table — whatever the search does, and
it runs even in the rows where the search has already excluded the product,
because the planner put it first. Adding a category to a search that cost
0.03 ms makes it cost at least 29 ms, and that is with an EMPTY table on the
inner side; with real memberships each of those 52,004 loops becomes an index
probe instead of a scan of nothing.

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
The index the schema creates on `category_id` alone is unreachable from the
query as written.

This is a lead, not a recommendation, and three things stand between it and a
change. It is a property of the SHARED filter body, so it cannot be changed for
the category without deciding what happens to the other six optional
predicates; the same `OR` idiom is what lets one statement serve every
combination of filters, which is a virtue this measurement does not get to
trade away on its own; and the rig cannot say what the semi-join would actually
cost, because there is no category data to join to. What is established is only
that the current shape cannot use the index, and that this is caused by the
`OR` rather than by the data.

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
- Nothing about a catalog with real categories, tags or collections. The rig
  has none, and the category section above is a floor.
- Nothing about wider rows. These titles are 15.5 characters and these
  descriptions are empty; a real catalog is more expensive per row by an amount
  this rig cannot state.
- Nothing about the Go side of the request. The figures are SQL execution time
  and pgbench round trips; the response path was benchmarked separately and is
  recorded in `docs/gaps.md`.
- Nothing about a multi-channel shop. There is one sales channel here and
  almost every product is in it, so the visibility filter removes almost nothing
  and its subplan is at its cheapest.
