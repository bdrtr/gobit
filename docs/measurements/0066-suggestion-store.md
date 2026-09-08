# A suggestion store: both ends re-measured, 2026-09-08

Evidence for
[ADR 0066](../adr/0066-a-suggestion-belongs-to-the-module-that-owns-the-row.md).

Gap B16 was written 2026-09-06 and says a suggestion store is open at both ends:
nothing would write one and nothing would read one. Every claim below was re-run
against the tree rather than quoted.

## The row, claim by claim

| B16 claims | verdict 2026-09-08 |
|---|---|
| No table named for a suggestion exists in any migration | HOLDS — 82 tables, none |
| Nothing would write one | HOLDS, and it is narrower than the row says |
| Nothing would read one | HOLDS, and it is wider than the row says |
| Every feature that proposes is an AI row | HOLDS for features; the tree already has two proposing JOBS |

## The census

    find . -path ./.git -prune -o -name "*.up.sql" -print | grep -v testdata

52 production up-migrations. The `CREATE TABLE` statements in them declare 82
distinct tables, and the full list was read by eye: no `suggestion`,
`proposal`, `recommendation`, `draft` or `pending_*` among them. The nearest
things by shape are `audit_log` (a record of what happened), `job_run` (a record
of what ran) and `event_outbox` (a record of what is owed) — three tables that
carry a HISTORY or a QUEUE, and not one that carries a PROPOSAL.

    grep -rlin "suggest\|proposal\|propose\|recommend\|confidence" \
        --include="*.sql" . | grep -v testdata

One file: `internal/modules/invoice/migrations/000001_invoice_init.up.sql`,
where the word is prose about the migration's own statements. No column anywhere
holds a confidence, a source or a pending state.

    grep -rni "anthropic\|openai\|llm\|langchain\|embedding" --include="*.go" .

No hit is about a model. Every one is the word "embedding" used of the embedding
program, which is what ADR 0025 calls the project that imports gobit.

## What proposes today, and why neither needs a store

The brief B16 comes from asks for the shape "the system proposes, a human
applies". That shape is already in the tree twice, and neither instance stores
anything.

| producer | what it emits | where it lands | how a reader gets it back |
|---|---|---|---|
| `internal/jobs/sagawatch` | a COUNT of abandoned sagas; the ids go to the log | one line in `job_run`'s detail column, printed by `gobit jobs` | `gobit stuck` reruns the same listing query |
| `internal/jobs/paymentrecon` | "examined 12, agreed 12, DIVERGENT 1" | the same detail column, plus three separately levelled log lines | the next pass asks each provider again |

Both are deliberate: sagawatch's package documentation says it counts, logs and
stops, and paymentrecon's says nothing here writes anything. Both point at a
human command rather than at a stored row.

What makes them cheap is not restraint, it is REPRODUCIBILITY. Their findings
are functions of committed rows plus, for reconciliation, a provider that can be
asked again. Storing them would create a second copy able to disagree with the
first, which is the defect a report avoids by being recomputed.

`job_run`'s detail column is the channel they use, and its limits are written
into the migration: one line per run, no personal data, printed in a table an
operator scans. It can carry a count. It cannot carry a target row id, a
proposed value, or a per-item decision that survives the next pass — which is
exactly the difference between a report and a suggestion.

## What would write one

The features that would produce a per-row proposal are C11 (review summaries),
C3 (an operator assistant) and the forecast suggestions measured in
docs/measurements/ai-powered-commerce-features.md. None is built, and the model
grep above shows nothing to build them on. Two of the three are blocked by something
that is not the store: C3 waits on a return-creation surface, and forecasts wait
on a stock ledger (B7) that does not exist because inventory overwrites its
quantities in place.

## What would read one

The row says nothing would read one. Measured, the reader end is emptier than
that: the surface a review suggestion would appear on does not exist either.

- The moderation queue is an admin API endpoint — `GET /admin/v1/reviews` with
  a status filter, `GET /admin/v1/reviews/{id}`, and `POST` to the id's status
  path to decide.
- `internal/adminui/templates` holds thirteen templates — login, error, layout,
  products, product, product edit, variant, orders, order, customers, customer,
  inventory, sales. There is no review page at all.

So a suggestion written today would be visible only to whoever wrote SQL by
hand. A proposal nobody can see is not a proposal.

## Why it cannot be built ahead of its producer

This is mechanical rather than cultural, and both gates were read:

- `internal/arch/columns_test.go` fails a column that no INSERT or UPDATE names,
  replaying every module's migrations in order, so a column added by a later
  ALTER TABLE counts too. Its exemption map is empty, and its own documentation
  says an empty map is what stops the next entry hiding behind an old one.
- `internal/arch/consumers_test.go` fails a published topic with no subscriber,
  and its exemption map is empty too.

A suggestion table landing before its writer needs entries in the first map. A
suggestion arriving as an event needs one in the second.

## The two real targets, and the shape each forces

**A proposal to CHANGE a row — the review case.** `reviews` carries `status`
(submitted, approved, rejected), `moderated_at` and `moderation_note`, with a
CHECK named `reviews_moderation_mirror` asserting that `status = 'submitted'` is
the same statement as `moderated_at IS NULL`. A suggested status is therefore
not free to store beside them: it must be shown NOT to be a moderation, or the
constraint stops meaning what it says. Columns on the row are the cheap shape —
the apply is the moderation endpoint the operator already calls, and the scope,
the reader and the retention are the review's own.

**A proposal to CREATE a row — the category case.** `product_category_map` is
`(product_id, category_id, created_at)` with the pair as its primary key.
The setter in `internal/modules/product/queries/taxonomy.sql` deletes every row
for a product and reinserts the set, so a pending suggestion row in that table
is erased by the next human edit of the product's categories, silently. A
pending row is buildable there, but only in a change that also teaches that
setter about it.

## Applying the trigger to today's tree

ADR 0066's trigger asks two questions of a producer. Both must be YES.

| candidate | proposes a value for one named row? | is it beyond a reader's recomputation? | store needed |
|---|---|---|---|
| sagawatch | no — a count and a class | no — `gobit stuck` reruns it | no |
| paymentrecon | no — a divergence, and it refuses to conclude what to do about it | no — ask the provider again | no |
| outbox relay | no — it publishes what was already written | n/a | no |
| a review-moderation suggester | yes | yes — a model call | YES |
| a reorder forecast | yes | yes — over a history nothing keeps | YES, after B7 |

The last two do not exist. That is the whole finding.

## What was not measured

- Whether any operator wants a proposal today. Nobody was asked, and this
  document claims nothing about demand.
- What a model call costs, in money or in latency. Out of scope: the trigger
  does not ask how expensive the call is, only whether the answer survives it.
- Whether a generic applier could be made safe by a registry of per-module write
  methods. That is argued in the ADR's rejected options from module isolation
  and from the drift a second list of write surfaces invites; it was not built
  and therefore not measured.
