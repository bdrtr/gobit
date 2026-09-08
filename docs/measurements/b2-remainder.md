# B2's remainder is four different kinds of work — measured 2026-09-05

Moved out of `docs/gaps.md` on 2026-09-08 unchanged. A measurement is
evidence, not a decision: it is kept in full and read when a number is
questioned, not on the way to understanding the repository.

**All four are built as of 2026-09-08** (sort on 2026-09-06; the option value,
the price and the availability in one round on 2026-09-08, behind ADR 0039, 0041
and 0040). The section is kept because the SPLIT it measured is the thing that
turned out to be right, and the three subsections below carry what the build
changed about each. What is left of B2 is multi-value, which is a
parameter-shape decision nobody has taken.

~~"Still missing: price, in-stock, option value, sort"~~ — that line listed four
names as though they were one gap of one kind, and they are not. Two of them are
builds, two of them are not builds at all. The bag is the finding: while the
four sat on one row, the cheapest of them (sort) looked as expensive as the one
that cannot be started without a written decision (price), so nothing moved.

The order below is by kind, not by value.

### Sort — a real build, and the only straightforward one

The listing's `ORDER BY` is a COMPILE-TIME CONSTANT — `created_at DESC, id DESC`
in `internal/modules/product/queries/product.sql` and, again, in the
hand-written channel-scoped query in
`internal/modules/product/repository/saleschannel.go`. There is no sort argument
on either surface, REST or GraphQL, so there is nothing to widen: the parameter
has to be introduced before it can be honoured.

What the product module can sort by from its OWN tables, measured against the
indexes in `internal/modules/product/migrations`:

| sort | answerable | index |
| --- | --- | --- |
| `created_at` | yes, and it is the one in use | `product_created_at_idx` |
| `handle` | yes | `product_handle_uniq` |
| `title` | yes | NONE — a sort on it is a sort of the whole catalog |
| price | no | there is no price to sort by; see A16 |
| stock | no | there is no stock column; see A17 |
| popularity | no | there is no TABLE, not merely no index |

**The trap is the cursor, and it is silent.** The keyset cursor carries three
things — the listing's name, a time and an id (the type lives in
`internal/core/page`) — and NOTHING about the order the page was cut in. A
cursor minted under `created_at DESC` decodes cleanly under a `title` sort: the
listing name still matches, the time and id are still well-formed, and the query
pages happily through the WRONG ORDER. It does not fail; it returns plausible
rows. So the sort parameter and the cursor's payload are one change and have to
be decided together — either the cursor carries the sort key and rejects a
mismatch, or the sorted listings are offset-only and say so.

### Option value — a build, behind one decision (now A18) and one shape choice

**Built 2026-09-08.** Two of the three obstacles below were answered exactly as
written; the third was answered by a MIGRATION, which is the correction worth
carrying: A18's record claimed it had settled the index as well, and it settled
the index of IDENTITY. `product_option_value_folded_uniq` leads with `option_id`
and a shopper filtering by "Color: red" has no option id, so 000004 adds
`product_option_value_folded_idx` — partial on the soft delete, NOT unique,
leading with the column the predicate names. The clause also guards the soft
delete on BOTH parents, because deleting an option is not a cascade.

All five tables are inside the product module (`product_option`,
`product_option_value`, `product_variant_option_value`, plus the variant and the
product), so ADR 0001 does not block this one at all. Three things do:

- **The filter cannot be on a value ID, and what a text match MEANS is now
  A18.** The normalisation question this bullet raised was measured on
  2026-09-06 and written out as a decision with three priced candidates, because
  it is the head of everything else here: it decides the index as well as the
  predicate, and the "is Renk the same axis as Color" half of it is a merchant's
  data question rather than a rule this framework can pick.
- **The filter cannot be on a value ID.** `product_option.product_id` and
  `product_option_value.option_id` are both NOT NULL, so an option-value id
  resolves to exactly ONE product by construction — filtering the catalog by one
  would return at most one product. The filter has to be on the (option title,
  value) TEXT pair, which needs a normalisation decision: case, whitespace, and
  whether "Renk" and "Color" are the same axis.
- **No index leads with either column.** The two unique indexes that exist are
  `product_option (product_id, title)` and
  `product_option_value (option_id, value)`; both lead with the PARENT id, which
  is exactly the column a catalog filter does not have.
- ~~**There is no vocabulary endpoint, and a vocabulary of ids would be
  useless.**~~ **Built 2026-09-06** as ~~`GET /store/v1/option-values`~~, and
  **moved into the channel path by ADR 0044 on 2026-09-08:**
  `GET /store/v1/sales-channels/{sales_channel_id}/option-values` is the fourth
  vocabulary endpoint and the only one that returns TEXT: it hands back the
  DISTINCT (option title, value) pairs, because an option belongs to exactly one
  product and an id would name one product's one value. It is SCOPED exactly as
  the product listing is — published products and the channels of the request's
  publishable key — and that is not a nicety: every entry exists BECAUSE some
  product carries it, so an unscoped vocabulary would name the option values of
  a draft product and be the hole in a wall the listing keeps. Proved at both
  layers, and the mutation that drops the status predicate fails the unit test
  AND the integration test.

And one question category and tag never had to answer, because each shipped as a
single scalar per axis: **multi-value.** "Red or blue, in size M" is OR within
one option and AND across two, and neither the parameter shape nor the SQL for
it exists.

### Price — a decision, not a build. Now A16

**Answered by ADR 0041 and built 2026-09-08.** The sentence below — "the
storefront's prices arrive in a second round trip made AFTER `LIMIT`/`OFFSET`
has already cut the page — filtering there would filter a page that was chosen
before the filter ran" — is exactly right and is the reason the built filter
SCANS rather than post-filtering one page. It is also why the filter has no
index: the predicate ADR 0041 describes is indexable by PRICING, and the catalog
may not join those columns nor push a predicate to a provider that accepts only
`id`.

Filed as a filter, measured as a definition problem: there is no amount on the
page to compare against. The full reasoning is in A16; the short form is that a
product has no price, "the price" is a selection function with five ordered
tie-breakers, and the storefront's prices arrive in a second round trip made
AFTER `LIMIT`/`OFFSET` has already cut the page — filtering there would filter a
page that was chosen before the filter ran.

### In-stock — also a decision. Now A17

**Answered by ADR 0040 and built 2026-09-08, badge and filter, plus the
field-name audit the decision owed.** The sentence below still holds and is
worth keeping: the product module can reach a link table legally and so can
answer "has an inventory item", which is not the question anybody asks — what it
now does instead is read the ONE published field name inventory serves through
the Query layer, and that pairing is audited rather than trusted.

Same shape, different module. "In stock" for a PRODUCT is defined nowhere here;
availability is defined below the product, twice. The product module can reach a
link table legally and so can answer "has an inventory item", which is not the
question anybody asks. The full reasoning is in A17.

### ~~The half that IS built has not reached the read layer~~ — it reached it

**Closed 2026-09-05**, hours after the sentence below was written, and it is
left standing because the shape of the gap is worth keeping:

> The panel does not read the storefront listing. It reads the cross-module read
> layer's `product` provider, and that provider accepts `status`, `handle`,
> `collection_id` and `id`/`ids` — **not `category_id` and not `tag_id`.** So the
> read layer's product surface is now BEHIND the REST and GraphQL surfaces that
> B2 extended, and the visible consequence is that the shop's customers can
> narrow the catalog by category while the shop's operator cannot.

This was believed to be impossible, and the belief was written down: the godoc
on the panel's product list claimed it made "the same Graph call the storefront
listing uses, so the screen cannot drift". The two were never the same call.
The comment has been corrected (see D12).

The provider now takes both taxonomy filters, and the interesting measurement is
what they cost: two switch cases and no SQL at all. `ProductFilter.CategoryID`
and `ProductFilter.TagID` were already wired into the listing AND the count as
EXISTS subqueries, so the read layer had been one `switch` short of a capability
the database could already answer. That is the honest shape of this class of gap
— not missing machinery, missing a case in the surface that offers it.

Two things had to be decided rather than typed. The first: `id`/`ids` combined
with a taxonomy filter is REFUSED with an invalid-argument error, not answered.
The id path reads products by identity and its records carry no category or tag
membership at all (the row-to-model conversion never fills them), so a Go-side
re-check would compile, match nothing, and hand back a confidently empty page —
and fetching the memberships instead would write the membership predicate a
SECOND time in Go beside the SQL EXISTS, which stops being one truth the day the
SQL learns to match a category's descendants. The refusal is data-independent
and it is pinned by a test. The second: the panel needs a vocabulary, because
an operator does not know `pcat_…`. So the product module offers a second
read-layer entity, `category` (`internal/modules/product/service/category_provider.go`),
which needed no new SQL either — the by-ids query existed and was generated, and
nothing had wrapped it.

~~What is still BEHIND, precisely: the storefront listing takes a text search and
the provider does not, so the panel has a category dropdown and no search box.~~
**That was true for a few hours.** The rule that left it out was this
repository's own — a capability with no consumer is a surface whose correctness
is tested nowhere — and the consumer arrived with the filter, in the same round:
the provider answers `q` and the panel's product list has a search box. The
subsection below carries what it cost when it was measured. The panel still
offers no TAG control, and that is a different kind of absence — a decision, not
a gap: a tag is free text with no dropdown to be, while a category is a tree an
operator maintains.

The hand-copied names are pinned where they can be. `TestThePanelCatalogNamesAgree`
in `internal/arch` binds the panel's `category` entity string to the module's own
constant at compile time. The FILTER names cannot be bound that way: both copies
of `category_id`, and both copies of `q` after it, are unexported constants in
their own packages, so there is no pair to hand to an assertion. They are pinned
one step weaker instead — `TestThePanelCatalogFilterKeysAgree` reads the four
shared keys out of the two packages' SOURCES and compares the values, which is
where a constant lives when the compiler has inlined it. What the source pin
cannot reach is the record FIELD names, because the module writes those as
literal keys with no constant to read; that limit is recorded in the entity
test's own "does not cover" section instead of being silently true.

### And the consumer is real — for one of the two filters

This file names C10 (natural-language search) as B2's consumer, and **C10 still
does not exist in code.** What changed on 2026-09-05 is that `category_id` no
longer needs it: the panel's product list is a real named consumer, in
production code, on a screen an operator opens, and it exercises the filter
through the read layer rather than through a module import.

`tag_id` has no consumer at all. It is offered by the provider, covered by unit
and integration tests, and called by nothing — which is precisely the class this
repository refuses to call finished (ADR 0009), so it is written down here
rather than counted as built. It came in the same change as `category_id`
because the two are one switch and one argument; keeping it out would have meant
the read layer disagreeing with the storefront on a filter the storefront
already answers.

### The text search — B2's last filter, and the first measured ceiling

Built 2026-09-05, and measured the same day on the 52,004-product rig. The full
record with every plan and every buffer count is `docs/measurements/catalog-search-cost.md`;
what belongs here is the part that constrains future work.

The filter itself cost no SQL: the term becomes `ProductFilter.Search` and the
shared filter body already turned it into `title ILIKE '%' || $4 || '%'` for the
listing AND the count. The decisions were where the work was. An empty or
whitespace-only term builds NO filter, because the two ways of passing it
through are silent and point in opposite directions — `''` reaches SQL as
`ILIKE '%%'` and matches everything, `'   '` matches nothing, and no caller can
tell which it got. And `q` beside `id`/`ids` is REFUSED rather than re-checked in
Go, on a measurement rather than a preference: `ILIKE` folds case the way the
CLUSTER's CTYPE folds it and Go folds it the way Unicode does. On the C-CTYPE
cluster in this workspace, uppercase-in-title against lowercase-in-term, the two
disagree on both non-ASCII pairs tried — a capital C-cedilla against a lowercase
one, and a dotted capital I against a plain `i`, where SQL finds no match and Go
finds one — and agree on the ASCII pair; on a C.UTF-8 cluster all three agree.
The letters are spelled out in words here rather than written, because a
markdown file carrying them would land in the language ledger; the module's own
godoc shows them as escapes for the same reason. Which means a
Go-side re-check would be right or wrong depending on how somebody ran `initdb`,
which is exactly what `core/db.CaseFolding` probes at startup and what ADR 0015
was written about.

**The cost does not follow the term, it follows how far down the ordering the
page's last match sits.** No index on this repository's `title` column can serve
the predicate — the pattern has a leading wildcard and there is no trigram or
full-text index — and the obvious conclusion from that, "the search is a
sequential scan and therefore slow", is half wrong in the half that decides what
to do about it. Listing, no channel filter, `LIMIT 25`:

| filter | plan | time | buffers |
| --- | --- | --- | --- |
| none | index scan on `(created_at DESC, id DESC)` | 0.03 ms | 7 |
| a term matching 52,000 of 52,004 | same index scan, term as a filter | 0.03 ms | 9 |
| a term matching 111 | same, 12,473 index entries walked | 2.6 ms | 2,635 |
| a term matching 1 | sequential scan + sort | 9.1 ms | 730 |

**The broad search is free and the selective one is expensive — and the
selective one is what a search box receives.** Three consequences worth carrying
into anything that touches this path:

- **The count moves in BOTH directions.** With the sales channel filter on, an
  unfiltered count is about 74 ms and 156,743 buffers; a term matching one
  product drops it to 12.9 ms and 734 buffers, because the `ILIKE` runs ahead of
  the per-row visibility subplan and removes 52,003 of its invocations. A broad
  term raises it to about 84 ms. The wall in the count was never the search; it
  is the visibility probe this file already recorded (67 ms → 0.65 ms, above).
- **One plan degrades silently under a prepared statement, and it is the
  mechanism the cursor's `COALESCE` sentinel was written for.** Executions one
  through five of the channel-filtered listing use a custom plan at 14.4 ms and
  734 buffers; from the sixth, PostgreSQL switches to a generic plan — an index
  walk over the whole ordering index at about 25 ms and 10,982 buffers — and
  does not switch back. Which plan a connection lands on depends on the terms
  its first five executions carried, so it is variance between connections
  rather than a constant tax, and variance that depends on history is the
  hardest kind to reproduce from a bug report.
- **Throughput is where the ceiling actually is.** pgbench, terms randomized per
  transaction, 16 clients: an unfiltered listing runs at 11,564 per second, a
  selective search at 856 (no channel) and 638 (with it) — **about forty times
  the latency and a thirteenth of the throughput.** For a panel used by a handful
  of operators that is irrelevant; for a storefront search box it is the first
  ceiling anybody hits, and it arrives long before the catalog grows.

**Where it stops.** The scan is linear at 0.18 to 0.235 microseconds per row,
measured from 10,000 rows up with no knee, so a selective search extrapolates to
roughly 20 ms at 100,000 products, 50 ms at 250,000 and 100 ms at 500,000. That
is an extrapolation from a measured slope and not a measurement, and it holds
only while the rows stay this narrow (these titles average 15.5 characters and
the descriptions are empty, so a real catalog is already worse), the table stays
in memory, and the concurrency stays low. The honest boundary is not a row count
but a pair of conditions: **the search stops being fast enough when the catalog
no longer fits in memory, or when concurrent searches exceed a few hundred per
second — whichever comes first.** On this hardware the second arrives first.

~~**A lead for the rest of B2, measured read-only.** The filter body spells every
optional predicate as `($n IS NULL OR …)`, and for the taxonomy filters the
second half of that `OR` is an `EXISTS`. PostgreSQL pulls an `EXISTS` in a
`WHERE` clause up into a semi-join BEFORE it folds constants, so an `EXISTS`
wrapped in an `OR` is never a candidate: the plan runs the subquery once per
catalog row (`loops=52,004`) and the index on `product_category_map` is
unreachable from the query as written. Adding a category to a search that cost
0.03 ms makes it cost at least 29 ms — with an EMPTY map table on the inner
side.~~ **The lead was taken on 2026-09-06 and the filter body changed; half of
the paragraph above was wrong, and the half that was wrong is why the change was
worth making.** Correct: the sublink is never pulled up, in either plan mode.
Wrong: "the index is unreachable". Under the default `plan_cache_mode` the
statement is re-planned on every call, the planner sees the literal id, folds
the disjunction away and reaches `product_category_map_category_idx` through a
hashed subplan at `loops=1` — 11.5 ms and 1,117 buffers at the rig's 5%
categories, where the paragraph predicted at least 29 ms and 52,004 loops. The
per-row shape does happen, but only when the planner orders the CHANNEL subquery
ahead of the taxonomy one, and then it is 147 ms rather than 29.

**A criterion the request did not carry now writes NO CLAUSE and consumes NO
PARAMETER** (`productFilterSQL` in
`internal/modules/product/repository/saleschannel.go`, which returns the body
and its arguments as one pair so the listing and the count cannot disagree about
the numbering). What that bought, measured on the rebuilt rig: nothing at all at
the shape the storefront serves most often — no taxonomy criterion, same plan,
same buffers, same milliseconds — between 1.4x and 2.4x at the rig's uniform 5%
categories, and between 34x and 586x at a category holding a handful of
products. **It is the SKEW that pays for it, and the rig has none**, which is
the same sentence as "this was invisible on the only catalog the repository
could measure". The risk the change carried was that a cheaper statement becomes
eligible for a CACHED generic plan that cannot see which category was asked for;
it was measured rather than reasoned about, and `pg_prepared_statements` reports
the list statements at 0 generic / 30 custom on every shape tried while the
count statements flip and lose nothing by it. That is a cost comparison and not
a guarantee, so the godoc names the one query that detects it changing.

**What could not be measured, and what changed about that.** ~~The rig used for
these numbers has ZERO rows in `product_category`, `product_category_map` and
`product_tag_map`, so every figure in the category paragraph above is a FLOOR —
which is also the reason the panel's own headline request, "search inside a
category", has no honest number yet.~~ **Measured 2026-09-06 on the rebuilt rig
(D13): the taxonomy filters have numbers now and the floor is gone.** At the
rig's 5% categories the filter costs 0.7 ms for a page of the listing and
11.5 ms for the count, 16.5 ms with the sales channel filter added; the panel
runs the listing only, so its category dropdown costs it the first of those
figures and nothing else. What the rig still cannot supply is the case that
mattered: its
twenty categories hold exactly 2,600 products EACH — 5.0%, by construction,
since `internal/rig/catalog.go` maps product n to category `(n - 1) % C + 1` —
so a selective category does not exist in it and had to be hand-built on a
scratch database. `rig.Spec` has no skew option; adding one is the difference
between a defect anybody can reproduce and one that needs a note. Also
unmeasured: collections (no product in the rig carries a `collection_id`), a
product in SEVERAL categories, cold caches (every figure is warm and the plans
say so), a multi-channel shop, and the Go side of the request, which is
benchmarked separately above.

**Options, none taken.** A trigram GIN index is the one index type that could
serve this predicate; `pg_trgm` is available in the image and not installed, and
everything else about it is unmeasured deliberately — creating it would be a
write to a measurement instrument and a migration is a schema decision with a
rollback and a per-write cost that one measurement does not get to make alone.
Reusing `plugins/searchpg`'s full-text shape would mean the same catalog searched
by two different definitions of "matches". Leaving it alone is the default, it
is what the measurement supports at this size, and it now has a written ceiling
instead of an unknown one.
