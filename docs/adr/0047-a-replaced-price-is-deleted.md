# ADR 0047 — A replaced price is DELETED, and what survived a replace was never a history

- **Status:** Accepted
- **Date:** 2026-09-07
- **Phase:** after the roadmap

## Context

Gap A14 asks whether the price history this repository already accumulates
should be promoted into a feature or dropped. The accumulation is real and
nobody chose it: `ReplacePrices` opens a transaction, stamps every live price of
the set with `deleted_at`, and inserts the new ones. The stamped rows stay in the
table forever.

Measured on the development cluster (PostgreSQL 16.14, `--locale=C`) in a
rolled-back transaction, running the module's own statements: one price set taken
through four generations — 10000, then 12000, then 9000, then 15000 minor units.
The read the module actually issues, `WHERE price_set_id = $1 AND deleted_at IS
NULL`, returned ONE row. A `count(*)` over the same set returned FOUR.

So the rows are there. What they are NOT is a history, and every reason is
structural rather than a matter of polish.

- **Nothing reads them.** All four reads in
  `internal/modules/pricing/queries/price.sql` carry `deleted_at IS NULL`, and no
  route exists that could reach a stamped row.
- **No index contains them.** The table's two non-primary-key indexes,
  `price_set_id_idx` and `price_list_id_idx`, are both partial on
  `deleted_at IS NULL` — confirmed against the live schema. A retained row is in
  NEITHER of them, so "what did this set cost before" can only ever be a
  sequential scan.
- **Successive generations of one price share no identity.** `buildPrice` calls
  `NewPriceID` on every replace, so the row that held 12000 and the row that
  replaced it have unrelated primary keys. There is no thread to follow — only
  rows that happen to name the same set.
- **Nothing in a row says WHY it was retired.** `ReplacePrices` and
  `DeletePriceSet` call the same statement with the same value. A row stamped
  because a merchant edited a price and a row stamped because the whole set was
  deleted are byte-identical.

**And the historical question a shop actually asks is already answered
elsewhere, permanently.** `internal/modules/order/migrations/000001_order_init.up.sql`
copies `unit_price` onto the line and says why in its own words: even if the
catalog changes later, or the variant is deleted, the name and the amount seen on
the invoice do not change. The cart does the same. What a customer PAID is
recorded by the transaction that charged them. What the retained rows would add
is the price on a day nobody bought — and the only named consumer for that is a
forecast `docs/gaps.md` itself records as absent, whose other missing input, a
stock level with no history behind it, is B7.

One further fact decides between the two candidates rather than merely describing
them: **nothing outside pricing names a price id.** Searching `PriceID` and
`price_id` across the tree outside `internal/modules/pricing` returns two lines,
both in a link-registry test fixture under `core/query`; cart and order keep their
own copied amount. A retained price row is referenced by nothing, so removing it
destroys no other module's handle on anything.

## Decision

**A replaced price is DELETED, and nothing is kept.** `ReplacePrices` stops
stamping the set's old rows and removes them, inside the transaction it already
opens and already serializes with `GetPriceSetForUpdate`.

Measured, same cluster, same rolled-back-transaction method, on the matched
pair: three generations of one price leave THREE rows under the soft delete and
ONE under the hard one.

**The statement keeps the liveness predicate** —
`DELETE FROM price WHERE price_set_id = $1 AND deleted_at IS NULL` — and that is
not leftover ceremony. A partial index can only serve a statement whose own
predicate implies the index's, and `price_set_id_idx` is partial on exactly this
one. The planner agrees: the plan for the predicated delete is an index scan
using `price_set_id_idx`, and the plan for the same delete WITHOUT the predicate
is a sequential scan of the whole table. (The probe table was empty, so the SHAPE
of the two plans is the measurement here and their cost is not.) A price edit is
an operator-facing write on a
table that every storefront price calculation also reads, and paying a full scan
on every edit in order to tidy rows nobody reads is the wrong trade. What the
predicate costs is that the delete cannot reach rows an earlier version of the
code already stamped — stated as a consequence below rather than hidden here.

**`price.deleted_at` stays, and after this it has exactly ONE writer.** The
column is not the accident; the second caller is. `DeletePriceSet` stamps the set
and its prices in one transaction, and that stamp on the price rows is the only
thing hiding a deleted set's prices from a calculation: `ListPriceCandidates` and
`ListPriceCandidatesBySets` do not join `price_set` at all, and `CalculatePrice`
consults `GetPriceSet` only when zero candidates came back — which is precisely
why the happy path is a single round trip.

**The rules go with the price, and it is the foreign key that takes them rather
than new code.** `price_rule.price_id` references `price(id)` ON DELETE CASCADE,
and a stamp has never fired it. Measured on the same cluster, with ONE rule
attached to each of the four generations: under the stamp, THREE `price_rule`
rows stood live behind stamped parents — one per superseded generation. Under
the hard delete, ZERO, and the single rule row left standing belonged to the
live price. The three is a property of the probe and not of the table: a stamp
leaks every rule of every superseded generation, so a shop's real number is
however many rules it has attached and however often it has re-priced.

## Rejected alternatives

**Promote the retention: give the rows a reader and an index.** The tempting
option, because the data appears to be free — the rows exist, so a listing
endpoint and one index look like the whole cost. They are not, because the rows
do not mean what a reader would take them to mean, and three separate repairs
come before the first honest read. The identities would have to be made stable,
which means a replace can no longer be delete-and-insert. The two stamping
callers would have to become distinguishable, which means a second column or a
second writer. And the record is silently PARTIAL in a way no migration can fix
retroactively: a price set
created once and never edited has no retained row at all, so the timeline for
every such variant begins on the day of its first edit rather than the day of its
first price. Promoting this is not cheaper than designing a history — it is
designing a history while also carrying an unusable first chapter.

**Build a real price-change table, written by the same transaction.** This is
what price history should look like if it is ever wanted: the old amount, the new
amount, the moment, and — the thing no retained row can carry — WHO. It is
rejected on the strength of one fact rather than a preference: nothing consumes
it. `docs/gaps.md` records the forecast as absent and this as the read surface a
forecast would need, not the forecast. Under ADR 0025 gobit is a library, so a
table every installation pays to write while no shipped code reads it is a cost
imposed on every embedder — and it is the exact mistake ADR 0037 names, quoted
back at this repository in four separate places, about an audit log written on
every admin WRITE and read by nothing until a reader was finally built for it.
The scope is narrower than "every request" and that only sharpens the parallel:
`Audit` in `core/http` records POST, PUT, PATCH and DELETE and returns early for
everything else unless a path is on the caller's explicit read list, so the
write-only ledger was already the cheapest version of itself and was still a
cost nobody was collecting on.
Committing that mistake a second time with the lesson already written down would
be the worse version of it.

**Keep the rows and merely declare that they are not a feature.** The cheapest
option on the day: change no code, write one sentence, close the gap. It is
rejected because the rows are not inert. A soft-delete column on this table
currently carries TWO meanings — "this set was deleted" and "this price was
superseded" — and only one of them was ever decided. A record that blesses the
second while promising nothing will be read by somebody who then has to guess
which writer stamped a row, and the schema will offer them no way to find out. A
decision whose entire content is that an accident may stay is not a decision;
it is the accident with a citation.

**Drop the column, the way three other modules dropped theirs.** It reads like
the settled move in this repository:
`internal/modules/inventory/migrations/000002_reservations_are_never_deleted.up.sql`,
`internal/modules/region/migrations/000003_reference_data_is_not_soft_deleted.up.sql`
and `internal/modules/fulfillment/migrations/000003_fulfillments_are_never_deleted.up.sql`
each removed a `deleted_at`, and this would be the fourth such migration. The
files and the columns are not the same number: region 000003 takes TWO in one
statement, `country`'s and `currency`'s, so the three precedents removed FOUR
columns between them. The difference is one sentence: **all four of those
columns had never been written, not once** — and each file says so in its own
words. This
one is written by two callers, and the second is not the accident — remove the
column and a soft-deleted price set keeps pricing, because nothing else on the
calculation path checks the set's liveness.

The mechanics were measured rather than argued, on the same cluster.
`ALTER TABLE price DROP COLUMN deleted_at` left `price_pkey` as the table's ONLY
index: both partial indexes name the dropped column in their predicate and
PostgreSQL removed them with no notice — the hazard those three precedent
migrations each measured and wrote into their own headers, so it is the loudest
thing they say rather than a trap they hide. In the same statement the four
retained rows became four live prices. **What it does NOT do is fail quietly:**
the module's own unchanged read against the dropped column returns SQLSTATE
42703, `column "deleted_at" does not exist`. A bare drop takes pricing off the air
on the first request. The silent version — a shop quietly re-priced from rows it
believed were gone — needs the same commit to strip the predicate from all four
reads and regenerate the generated code, and by then the author is looking
straight at the promoted rows. **This alternative is rejected for what it breaks,
not for what it hides**, and the distinction matters: if someone later drops the
column for a reason this record has not foreseen, the hard delete has to land
FIRST, and that caution belongs in the migration header exactly as the three
precedents write their own.

**Publish a price-changed event and let the embedder keep whatever record it
wants.** The most library-shaped answer, and ADR 0023's outbox already makes a
promised event part of the transaction that promised it, so the durability would
be free. Two things kill it here. Pricing publishes NO events at all today —
there is no event bus reference anywhere in the module — so this is not reusing a
mechanism, it is building the module's first event on the strength of a consumer
nobody has named. And it does not answer A14 even if built: the question is what
becomes of the ROWS, and under this option they are still there.

## Consequences

**Positive**

- **The schema stops carrying a store nobody can read.** Measured: one row where
  three stood, over the same sequence of the module's own calls.
- **One soft-delete column, one meaning.** After this, a stamped `price` row can
  only have come from `DeletePriceSet`. That follows from the callers rather
  than from the schema: `SoftDeletePricesBySet` in
  `internal/modules/pricing/queries/price.sql` is the ONLY statement that writes
  the column, and it has exactly two callers — `ReplacePrices` and
  `DeletePriceSet` — of which this decision removes one. The migration cannot be
  cited for it, because its single line about the column says only that deletion
  in this module is soft, module-wide, which endorsed both callers equally.
- **A price's rules die with it.** Measured on the four-generation probe carrying
  one rule per generation: three orphaned live rules before, zero after.
  Honesty about what that is worth: those rules were unreachable —
  `Service.ListPriceRules` verifies the price through `GetPrice` first and 404s,
  and `GetPriceRule` has no route at all — so this closes a latent inconsistency
  rather than a live defect. It is the same shape `docs/gaps.md` recorded for
  promotion's orphaned rules in D20, where measuring that the orphan does nothing
  is what kept the entry honest.
- **The delete is an index scan**, because it keeps the predicate the index is
  partial on. The tidy-up costs a price edit nothing it was not already paying.

**Negative, and accepted**

- **A mistaken bulk replace becomes unrecoverable from the table.** Today the
  previous generation is physically present and an operator with SQL access could
  read the old amounts back by hand. After this it is gone, and recovery is
  whatever backup the operator keeps — this repository ships none and ADR 0015
  names no backup requirement. This is the real price of the decision. It is
  accepted because the replace is already destructive in every way a user can
  see: the endpoint is a replace, `Service.SetPrices` documents itself as one and
  logs the count as a destructive operation, and a recovery route that exists only
  for someone willing to hand-write SQL against a table with no index for the
  question is not a recovery route anybody should be relied on to find.
- **"What was this variant's price last month" becomes permanently
  unanswerable.** The audit log answers who and when — `Audit` in `core/http`
  writes the request's own URL path and deliberately NOT the route pattern, its
  comment saying that a pattern would record the same string for every order, so
  the row for a price edit carries the touched set's real id in the path and
  names that one set; and ADR 0037 gave that table a reader. It records no
  amounts, by design. So after a suspicious price an operator can name the person
  and the moment and cannot name the old number. That gap stops being an accident
  and becomes a decision.
- **The rows already retained are NOT removed by this.** The delete keeps the
  liveness predicate, so a row stamped by an earlier version of the code is
  invisible to it and stays forever. An installation that has been replacing
  prices since the module shipped keeps every one of them, unread and unindexed,
  unless somebody writes a cleanup. This record does not write one, and the shape
  it should take is the one ADR 0038 chose for the same class of problem: a
  one-off correction belongs in a command an operator runs, not in a hidden step
  at every boot. Naming it here is what keeps it from being forgotten.
- **The forecasting gap loses its one accidental input.** `docs/gaps.md` already
  records that stock is a current-value column with no history behind it (B7);
  price now joins it deliberately. A forecast will have to build its own record,
  which is more work than querying rows that happened to survive — and the right
  amount of work for something that has to be trusted.

## What this deliberately does NOT do

- **It does not drop `price.deleted_at`, and a later record that wants it gone
  owes an argument this one does not make** — specifically, what hides a deleted
  price set's prices from `ListPriceCandidates`, which does not join `price_set`.
- **It does not remove the soft delete anywhere else in pricing.** `price_set`,
  `price_list` and `price_rule` keep theirs, and each has a delete path a user can
  actually reach. This decision is about ONE call site.
- **It does not say a price history is a bad idea.** It says the rows that
  survive a replace are not one, and that nothing in this repository consumes one
  yet. A shop that needs a price timeline builds it on purpose, with a table that
  records who changed what, and this record is not an argument against that day.
- **It does not decide whether pricing should publish an event.** That question
  is real and untouched here; the module publishes nothing today, and giving it
  its first event is a decision with its own consumer problem.
- **It does not touch what a cart or an order recorded.** Those copies are the
  record of what was CHARGED. Nothing here reaches them, and nothing here should.

## Related

- [ADR 0037](0037-the-audit-log-gains-a-reader.md) — the reader that answers who
  changed a price and when, and the write-only-ledger mistake this decision
  refuses to repeat.
- [ADR 0025](0025-gobit-is-a-library-not-a-template.md) — why a table every
  installation writes and no shipped code reads is a cost imposed on every
  embedder.
- [ADR 0041](0041-a-price-filter-compares-the-base-price-at-quantity-one.md) —
  the other pricing decision of this round, over the same tables.
- [ADR 0023](0023-a-promised-event-is-part-of-the-transaction.md) — the outbox a
  price-changed event would ride on, if that question is ever answered.
- [ADR 0015](0015-postgresql-cluster-contract.md) — the written contract for the
  cluster, which names no backup requirement, so the recovery path this decision
  gives up is the operator's own.
