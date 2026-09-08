# Gap inventory

What this repository has, what it does not, and — for each absence — whether it
is a GAP or a DECISION. The two are never listed together: acting on a decision
as though it were a gap overturns an architecture on autopilot.

One row per item: the question, and where the answer is. A closed row names its
ADR and stops. The reasoning is in the ADR, the history is in `git log`, and the
numbers are under [`measurements/`](measurements/).

Compacted 2026-09-08 from 4,615 lines. Nothing was decided or reopened in that
pass; two rows were found stale and are marked below.

## A. Decisions — no code until they are answered

| # | Question | Answer |
|---|---|---|
| A1 | Which packages become public | ADR 0026 + 0027 — `core/`, fourteen packages plus a four-method facade; no commerce model among them |
| A2 | Is gobit the data controller | ADR 0029 — no, the embedder is; gobit owes the mechanism, not the policy |
| A3 | May the customer pay a different amount than the merchant receives | ADR 0042 — not now; when it comes it arrives as a settlement ROW, not by relaxing the equality |
| A4 | Invoice retention against erasure | ADR 0032 — an issued invoice refuses erasure, and the refusal lives in the schema |
| A5 | Group price for a customer in several groups | ADR 0049 — one group decides, and the merchant ranks the groups |
| A6 | `allow_backorder` and the three flags beside it: reader or deletion | ADR 0048 — all four get a reader; none stops being published |
| A7 | Does the panel become an admin-API client | ADR 0030 — yes, an SPA on the same cookie |
| A8 | Catalog cacheability | ADR 0044 — the sales channel moves into the path; all four routes moved |
| A9 | Does ADR 0008 stand on customer identity | ADR 0043 — it stands; gobit requires an identity and still issues none |
| A10 | pgvector: reopen the cluster contract | ADR 0045 — no; pgvector is a separate opt-in extension module |
| **A11** | **Where translated content lives** | **OPEN.** ADR 0050 fixes gobit's POSITION (one language is stored, the second is the embedder's) and says in its own words that it does not close this gap. What is missing first is not storage but a way for a request to ASK for a language |
| A12 | JWT TTL policy | ADR 0031 — a fixed twelve hours, bounded in shared environments |
| A13 | Metrics posture | ADR 0046 — metrics leave by scrape, OTLP keeps the traces |
| A14 | Price history: promote the accidental retention or drop it | ADR 0047 — drop it; what survived a replace was never a history |
| A15 | May the storefront accept content from a party it cannot identify | ADR 0051 — only when the write is confined or inert, held mechanically |
| A16 | What amount does a price filter compare | ADR 0041 — the base price, request's currency, quantity tier one, no group context |
| A17 | What does "in stock" mean for a product | ADR 0040 — at least one variant unmanaged, backorderable, or with quantity above zero |
| A18 | What counts as a match when a shopper filters by an option value | ADR 0039 — the folded form, stored beside the merchant's spelling |
| **A19** | **Does a callback get a durable ledger of its own** | **OPEN.** ADR 0056 answers the mechanical half — no `audit_log` row, and the ring's log records every outcome including the ones a guard refused. A `callback_log` table fits the facts and needs three things this repository cannot derive: a reader, a scope beside `audit:read`, and a retention answer for a population the caller chooses |

## B. Foundations — each unblocks several features

| # | Foundation | Status |
|---|---|---|
| B1 | A guarded inbound-callback class | Built — ADR 0028, `core/http.CallbackRegistry` |
| B2 | Storefront filter surface | Built — category and tag 2026-09-05; sort, option value, price and in-stock 2026-09-08 (ADR 0039/0040/0041) |
| B3 | Storefront vocabulary endpoints | Built — `GET /store/v1/{collections,categories,tags}` |
| B4 | Review module | Built — ADR 0051 applied in the SQL rather than quoted |
| B5 | Order ↔ fulfillment link | Built. A shipment cannot yet be created from the panel |
| B6 | A money-event read surface | Built — `first_captured_at` / `last_refunded_at`, loaded only when asked for |
| **B7** | **Inventory movement ledger + inventory events** | **OPEN, blocked by its own first consumer.** A topic no production file subscribes to is refused and the exemption map is empty by policy |
| B8 | Customer module events | Not delivered, deliberately — ADR 0033 met the obligation with an interface that returns an answer |
| **B9** | **Stored payment instrument** | **OPEN, blocked on a published-contract decision.** No table, no column, no symbol; both halves would widen `core/provider` |
| **B10** | **Carrier-capable quote input** | **OPEN.** The state-machine half was built 2026-09-06; the quote input is larger than this row used to say |
| B11 | Order addresses | Built — written in the same transaction as the order's header and lines |
| B12 | Outbound delivery machinery | Built — `next_attempt_at`, `dead_lettered_at`, capped doubling backoff |
| B13 | Plugin host: let a plugin register a job | Built — `plugin.Host.RegisterJob`, arrived with its first consumer |
| B14 | Order line-item read entity | Built — second read-layer entity, with a date filter and an index |
| **B15** | **File events** | **OPEN, same blocker as B7.** The file module has never published an event |
| **B16** | **A suggestion store** | **OPEN at both ends.** Nothing would write one and nothing would read one |
| B17 | KVKK erasure, export, retention | Erasure built — ADR 0033/0034, `core/personaldata`. Export and retention are not |
| B18 | A per-column round-trip test | Built — `TestEveryColumnIsWrittenBySomething` |

## C. Features — after the above

| # | Feature | Waits on |
|---|---|---|
| C1 | Back-in-stock waitlist | B7 — the table, the event and the subscriber are all missing |
| C2 | Order timeline | Built — `GET /admin/v1/orders/{id}/timeline`, composed, each entry names its clock |
| C3 | Operator assistant in the panel | A return-creation surface |
| C4 | Consent records and data-subject endpoints | Nothing. Actionable since 2026-09-07; the endpoints exist |
| C5 | Outbound webhooks | Nothing. `plugins/webhookout` is built and installable (see D22) |
| C6 | Carrier plugins | B10's quote input, and a place for a carrier to deliver what it receives |
| C7 | Installment table + providers | A3 |
| C8 | Digital product delivery | — |
| C9 | B2B quotes, terms, minimum order | Nothing — A5 is answered |
| C10 | NL search layer | Nothing — B2 and B3 are built |
| C11 | Review summaries and Q&A | A read-layer provider on the review module, absent until its first reader |
| C12 | Subscriptions | B9 |
| C13 | Feature flags, then A/B | A9's assignment key |
| C14 | Panel extension points, then the SPA | The three things ADR 0030 names as owed by whoever implements it |
| C15 | Multi-language | A11 |
| C16 | Real-time stock | B7, plus a fan-out the bus cannot do |
| C17 | Edge caching | Nothing — A8 is answered and built |
| C18 | Multi-vendor marketplace | A3 and most of the above; last, deliberately |

## D. Corrections

Live rows first. A fixed row keeps its line because the defect CLASS is what the
row is for; the reproduction is in the commit that closed it.

| # | Finding | Status |
|---|---|---|
| **D9** | Neither order nor payment ever soft-deletes: ten `deleted_at` columns nothing writes, behind reads that all carry `deleted_at IS NULL` | **OPEN.** Dropping them is a schema decision; taking the deletes on is a product one |
| **D18** | Nine columns nothing has ever written, invisible until D16's fix | **Eight closed. The ninth is `stock_locations.deleted_at`**, and the question its exemption states is not "delete or status": a location has no delete OR update path, availability sums `inventory_levels` without joining locations, and both level and reservation rows CASCADE. What a closed location OWES — do its levels move, zero out or stop counting, and what happens to live reservations — decides the mechanism |
| D1 | `/paytr/callback` sat outside every guarded prefix | Fixed — ADR 0028, and the residue answered 2026-09-08: a callback is recorded in the ring's LOG and not in `audit_log` (ADR 0056) — a provider is not an actor and the table has no column for the outcome. Every outcome now leaves a line, refusals included; four were silent, all of them the class where the handler RAN. What remains is a decision, A19, and not a residue |
| D2 | `allow_backorder` published and read by nothing | Fixed — ADR 0048 |
| D3 | The address book's storefront endpoints were unauthenticated | Fixed — ADR 0043. Residue: the cart's `customer_id` and b2b's copy of the boundary |
| D4 | `order_exchanges.completed_at` / `canceled_at` never written | Fixed. The audit built to catch this never caught it — see D16 |
| D5 | Archiving an order left no timestamp | Fixed — migration 000007 |
| D6 | Two repository-internal transactions could not compose | Fixed, and the entry had named the wrong second module |
| D7 | OpenAPI claimed `q` searched title and handle | Fixed |
| D8 | The link's far side named an entity with no Query provider | Fixed |
| D10 | Nothing stopped a module's SQL naming another module's table | Fixed, and the residue closed 2026-09-08. Ownership comes from every migration set in the tree (82 tables, 25 owners) instead of the modules alone, so the ten tables the plugins and the core create are no longer owned by nobody — which under this rule meant legal for everybody. The SCANNED set is walked from the tree rather than derived from ownership: six of the ten plugins ship no migrations, and deriving the population from ownership would let a plugin leave the audit by being the thing it looks for. **Widened once more the same day**: "owners plus plugins" was the same proxy one ring out, and the component sitting in it was the rig — see D13 |
| D11 | `make load-test` printed green and measured nothing | Fixed — the `-run` selector named no test |
| D12 | The panel's product list did not make the storefront's Graph call | Corrected |
| D13 | The measured catalog existed in one Docker volume and could not be rebuilt | Fixed, and the residue closed 2026-09-08. The SQL gate's population is every production PACKAGE now, not the migration owners plus the plugins: measured across 156 of them, `internal/rig` was the only component in the repository naming a table it does not own, and it was outside the walk for the same reason six plugins had been. Its crossing is exempt per TABLE rather than wholesale — the eleven it fills are listed, a twelfth is a finding, and a listed table nobody names is a finding too, which is what makes the list the exemption's own blindness floor |
| D14 | `make load-test` measured a catalog of zero products | Fixed — D11 one layer down |
| D15 | Performance figures nobody could re-check | Re-measured and corrected |
| D16 | The column audit named D4 as what it catches and never caught it | Fixed. The fix produced nine live findings — D18 |
| D17 | `CancelReturn` / `CancelClaim` had no production caller | Fixed |
| D19 | `SetPassword` read and wrote across a gap a delete could land in | Fixed. Residue: `LinkSalesChannel` locks the channel, not the key |
| D20 | Three godocs called an orphaned rule "structurally impossible" | Fixed — it was measurably possible |
| D21 | A job that succeeds could not say anything | Fixed. Residue: a PLUGIN still cannot report a detail |
| D22 | A plugin can be complete, tested, documented and impossible to install | Fixed — `webhookout` is in the composition root's catalog. **The row said otherwise until 2026-09-08** |
| D23 | Three audits were green on the very defect they were written for | Fixed. Residue: the blindness control has no row in the README's invariant table |
| D24 | A carrier's events arrive out of order and the state machine refused all of it | Fixed |
| D25 | A handler could read a query parameter it never describes | Fixed — `TestEveryQueryParameterAHandlerReadsIsDescribed`. **The row said "not shipped" until 2026-09-08** |
| D26 | Two module api packages had no test at all and nothing asked why | Fixed |
| D27 | A data subject could be told "you are not here" by the one holder required to keep their document | Fixed — ADR 0038 |
| D28 | A guard that stops guarding at the ASCII boundary | Fixed — ADR 0038's amendment and `case_folding_test.go` |
| D29 | The documents were audited against the code and 91 statements were false | Swept once. Residue: there is no standing check for a prose claim |
| D30 | A plugin held four columns of personal data and declared none | Fixed. Every mutation proof needs `-count=1` |
| D31 | A data race in `TestCancellationActuallyStopsRemainingMigrations`, seen once | **Diagnosed and closed 2026-09-08 — ADR 0052.** golang-migrate v4.19.1 leaves the migrator's `isGracefulStop` unguarded next to an `isLocked` its mutex does guard, and the one line arming the write was gobit's `GracefulStop` send. Measured both ways: same error code, same version, same dirty flag, same regression test — the layer bought nothing. The send is gone and a gate refuses its return; the race is DORMANT rather than fixed, because the field is unexported |
| D32 | The language ratchet could not see a file until it was too late to matter | Fixed — the population is now tracked ∪ untracked-not-ignored |

## E. Out of framework scope — written, not forgotten

- **e-Fatura transmission** needs the merchant's certificate and an integrator
  contract. gobit owes the document, the numbering and the slot; the first two
  are built (ADR 0024).
- **Customer identity** is the embedder's job (ADR 0008, upheld by ADR 0043).
- **A/B assignment** likewise: the framework has no visitor.

## F. Standing work

- **Translation ledger: 200 files.** ADR 0012 lets it only shrink.
- **Panel: five sections over four of seventeen modules.** Thirteen modules have
  no screen, nothing can be created or deleted from it, and a plugin has no way
  to add one. Review is the first screenless module that is not configuration —
  its moderation queue is daily operator work.

## G. Found while building, not yet decided

- ~~**The migration role and the runtime role are ONE superuser account.**~~
  **DECIDED 2026-09-08: ADR 0060 — the split is the operator's to provision, and
  the binary does not change.** The grant list it needs is in
  [`security.md`](security.md).
- ~~**The rig cannot reproduce the case that motivated the change it paid
  for.**~~ **DECIDED 2026-09-08: ADR 0058 — `Spec.SkewedCategorySize` builds TWO
  small categories, and zero builds neither.** The uniform taxonomy keeps every
  member it had, so the figures taken at 5% selectivity stay measurable on the
  same rig; the skewed rows are additional memberships, which incidentally gives
  the generator its first product belonging to two categories.

  **What made it two rather than one is the measurement's own sharpening, and it
  is exactly what a single small category would have hidden.** Two categories of
  the same size gave two different plans — 12.5 ms where the members were
  adjacent in the listing order, 163.5 ms where they were spread across it — so
  the cost is not a property of the size but of which of two legal plans the
  statistics led the planner to. A rig carrying one of them would reproduce a
  number and invite the next reader to take it for a law. The two are therefore
  picked by the storefront's own ordering, not by a run of ids: consecutive
  numbers are not consecutive in it, so the obvious spelling would have built a
  spread category and called it adjacent.

  **The plan-shape acceptance test does NOT pin the difference, and that is a
  decision rather than an omission.** The shipped statement no longer carries the
  disjunction that collapsed, so there is nothing on the shipped path to pin; the
  two plans were measured as a coin the planner flips, so an assertion on either
  would go red on a legal plan; and the rebuild's acceptance check is four
  numbers on the unfiltered count query, which the taxonomy does not touch.
  Residue: the skew is opt-in, so `gobit seed` with no `-skew` still builds a rig
  that cannot show the case.
- ~~**Two clocks on one axis.**~~ **DECIDED 2026-09-08: ADR 0053 — they stay,
  and every moment names its clock.** Six columns, not five (`returned_at` was
  added after this row was written), and "every other moment comes from the
  database" was false — auth, promotion and six modules' `updated_at` are
  process-stamped too.

  **The cost this row named was measured, and then the measurement was found to
  be pricing the wrong change.** "Lose the injectable clock" costs two tests;
  the move it was offered as evidence for would also delete `stampFor`, seven
  query parameters and the fake store's mirror of four schema CHECKs — the one
  place they hold without a database — and would turn a stamp assertion into a
  tautology. That correction came from an adversarial pass over this row's own
  entry in this ledger, written the same day.

  What settled it is that for two of the six the database clock is WORSE:
  `now()` is transaction START and the capture's transaction wraps the provider
  call, so the stamp would record when gobit began trying rather than when the
  processor took the money; and the invoice's single `now` feeds both the series
  year and the stamp, so splitting them lets a document be numbered 2027 and
  dated 2026.

- **`authorized_at` does not exist**, and this row's premise was wrong about the
  other half. `refunded_at` is already closed: `refunds` has no UPDATE statement
  anywhere, so a refund row is immutable and `created_at` IS the refund moment —
  which the query and the model both say in prose. `authorized_at` is genuinely
  absent (zero hits in the whole tree). The decision is not the column but
  whether the published money-event surface gains a THIRD moment beside
  `first_captured_at` and `last_refunded_at`.
