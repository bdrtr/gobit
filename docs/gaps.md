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
| A9 | Does ADR 0008 stand on customer identity | ADR 0043 — it stands; gobit requires an identity at the address book and still issues none. ADR 0057 carries the same comparison to the b2b storefront and the cart without the requirement: a bound identity is BELIEVED against the claim, an absent one leaves those two surfaces serving |
| A10 | pgvector: reopen the cluster contract | ADR 0045 — no; pgvector is a separate opt-in extension module |
| A11 | Where translated content lives | ADR 0061 — neither half of the language axis is built, and two gates refuse a locale arriving without a record. ADR 0050 fixed the POSITION; what closes the row is that each half blocks the other — a locale key no request supplies is a key nothing can use, and a path segment whose two values return byte-identical bodies has no first consumer. The shape is settled by ADR 0044's precedent. Two triggers, either one: a SECOND component naming a locale, or one shop serving one catalog in two languages over one `inventory_levels`, one order `display_id` sequence or one `invoice_series` |
| A12 | JWT TTL policy | ADR 0031 — a fixed twelve hours, bounded in shared environments |
| A13 | Metrics posture | ADR 0046 — metrics leave by scrape, OTLP keeps the traces |
| A14 | Price history: promote the accidental retention or drop it | ADR 0047 — drop it; what survived a replace was never a history |
| A15 | May the storefront accept content from a party it cannot identify | ADR 0051 — only when the write is confined or inert, held mechanically. Of the two open defects it recorded, the cart's is NARROWED and not closed (ADR 0057): a bound identity now decides it, and an installation that has bound none still believes the claim — the residue that closes by binding a verifier. `plugins/webpush` is still a standing authority |
| A16 | What amount does a price filter compare | ADR 0041 — the base price, request's currency, quantity tier one, no group context |
| A17 | What does "in stock" mean for a product | ADR 0040 — at least one variant unmanaged, backorderable, or with quantity above zero |
| A18 | What counts as a match when a shopper filters by an option value | ADR 0039 — the folded form, stored beside the merchant's spelling |
| A19 | Does a callback get a durable ledger of its own | ADR 0062 — no; the durable record is the table the RECEIVING MODULE already owns, and the ring's log (ADR 0056) holds the refusals no module can see. The reader, the scope and the retention ADR 0056 asked for are answered there: a listing, a module scope, and rows ADR 0054 never deletes. A second callback PROVIDER reopens it, and a census of the bound sources fails the day one appears |

## B. Foundations — each unblocks several features

| # | Foundation | Status |
|---|---|---|
| B1 | A guarded inbound-callback class | Built — ADR 0028, `core/http.CallbackRegistry` |
| B2 | Storefront filter surface | Built — category and tag 2026-09-05; sort, option value, price and in-stock 2026-09-08 (ADR 0039/0040/0041) |
| B3 | Storefront vocabulary endpoints | Built — `GET /store/v1/{collections,categories,tags}` |
| B4 | Review module | Built — ADR 0051 applied in the SQL rather than quoted |
| B5 | Order ↔ fulfillment link | Built. A shipment cannot yet be created from the panel |
| B6 | A money-event read surface | Built — `first_captured_at` / `last_refunded_at`, loaded only when asked for |
| **B7** | **Inventory movement ledger** | **OPEN, and it is the half of this row nothing was blocking.** The event half closed as ADR 0063; the ledger is a table with an internal reader and no event gate ever touched it. `audit_log` does not cover it — it records the REQUEST, holds no delta and no item, and never sees a reservation the checkout saga takes. What it needs before it is written is four answers, not an unblocking: whether a reservation is a movement, whether `stocked_quantity` becomes derived from it, who the actor is when no admin made the change, and how long a row is kept |
| B8 | Customer module events | Not delivered, deliberately — ADR 0033 met the obligation with an interface that returns an answer |
| B9 | Stored payment instrument | **DECIDED 2026-09-08 — ADR 0064: not now, and what it waits for is a PROVIDER.** The trigger is an integration under `plugins/` whose upstream mints a customer-scoped credential it will accept on a later charge with no shopper present; deliberately not "when subscriptions arrive", because C12 waits on B9 and that would be a deadlock. **This row's own claim was half wrong**: only ONE half would widen `core/provider`. Paying with a stored token is already wired storefront-to-provider — `payment_data` travels unread from the cart's completion route into the session input, whose godoc names a card token as what belongs there — so what is missing is MINTING (no provider here returns a reusable credential: PayTR's iframe token is spent when its payment closes and its own godoc says the plugin stores it nowhere; the Stripe skeleton errors on all five money methods) and OWNING (the payment module's 22 columns name no customer). The shape when it comes is fixed in the ADR, including that the surface refuses under ADR 0043's row rather than ADR 0057's |
| B10 | Carrier-capable quote input | Closed as a DECISION — ADR 0065. The state-machine half was built 2026-09-06; the quote half is not widened, because the blocker is the PRODUCER and not the struct. `QuoteInput.TotalWeight` is the rehearsal: the one field of this shape the input has, handed a literal zero by the only trusted producer. **Trigger, two checkable facts:** `product_variant` carries the three dimension columns `product` already has, and the sub-country unit below the city means ONE thing across the customer address book, the cart and the order. A gate refuses a field of that input the tree does not fill |
| B11 | Order addresses | Built — written in the same transaction as the order's header and lines |
| B12 | Outbound delivery machinery | Built — `next_attempt_at`, `dead_lettered_at`, capped doubling backoff |
| B13 | Plugin host: let a plugin register a job | Built — `plugin.Host.RegisterJob`, arrived with its first consumer |
| B14 | Order line-item read entity | Built — second read-layer entity, with a date filter and an index |
| B15 | File events | Decided — ADR 0063, with B7's event half. No event is published; the file module's one cross-module relationship is read synchronously BOTH ways, so nothing waits to be told. Trigger: a second holder of an upload's bytes or address inside this repository — a cache in front of the object store is the concrete case |
| B16 | A suggestion store | ADR 0066 — none is built; a suggestion belongs to the module that owns the row it proposes, and the trigger is the first proposal a query cannot reproduce |
| B17 | KVKK erasure, export, retention | Erasure built — ADR 0033/0034, `core/personaldata`. Export and retention are not |
| B18 | A per-column round-trip test | Built — `TestEveryColumnIsWrittenBySomething` |

## C. Features — after the above

| # | Feature | Waits on |
|---|---|---|
| C1 | Back-in-stock waitlist | A15, and not B7. **The row said B7 until 2026-09-08** and was measurably incomplete: three files in the tree already name C1 as the thing that FAILS A15, because the waitlist row is an unverified contact detail with no unsubscribe. ADR 0063 makes the trigger a bound identity, which turns that row into a customer gobit already holds |
| C2 | Order timeline | Built — `GET /admin/v1/orders/{id}/timeline`, composed, each entry names its clock |
| C3 | Operator assistant in the panel | A return-creation surface |
| C4 | Consent records and data-subject endpoints | Nothing. Actionable since 2026-09-07; the endpoints exist |
| C5 | Outbound webhooks | Nothing. `plugins/webhookout` is built and installable (see D22) |
| C6 | Carrier plugins | A place for a carrier to deliver what it receives — the module's cross-module write surface cannot move a shipment, and a plugin can reach neither the method nor a structural interface naming it. **No longer B10**: ADR 0065 measured that dependency pointing the wrong way, and a plugin can ship against the quote surface as it stands with a flat option or a tariff in the option's own data. Its tariff is what names the fields B10 would otherwise guess |
| C7 | Installment table + providers | A3 |
| C8 | Digital product delivery | — |
| C9 | B2B quotes, terms, minimum order | Nothing — A5 is answered |
| C10 | NL search layer | Nothing — B2 and B3 are built |
| C11 | Review summaries and Q&A | A read-layer provider on the review module, absent until its first reader |
| C12 | Subscriptions | ADR 0064's trigger — a provider that can mint a reusable credential. B9 is decided, not open, and the instrument is only the first of C12's parts |
| C13 | Feature flags, then A/B | A9's assignment key |
| C14 | Panel extension points, then the SPA | The three things ADR 0030 names as owed by whoever implements it |
| C15 | Multi-language | A11 is answered — ADR 0061 defers it with two named triggers, and the gates fire when one arrives |
| C16 | Real-time stock | B7, plus a fan-out the bus cannot do |
| C17 | Edge caching | Nothing — A8 is answered and built |
| C18 | Multi-vendor marketplace | A3 and most of the above; last, deliberately |

## D. Corrections

Live rows first. A fixed row keeps its line because the defect CLASS is what the
row is for; the reproduction is in the commit that closed it.

| # | Finding | Status |
|---|---|---|
| **D18** | Nine columns nothing has ever written, invisible until D16's fix | **All nine closed** — the last by ADR 0055: `stock_locations.deleted_at` became `closed_at`, written by a close that is refused while the location still holds stock or a live promise. **Residue, two open questions for the owner: a location still cannot be RENAMED, and a close is FINAL — should a mis-close be undoable?** A reopen verb was drafted with the close and dropped: nothing in the invariant needs it, and clearing `closed_at` would make it the schema's one mutable stamp |
| **D33** | `province` names two different things, and the address book names neither | **OPEN, found 2026-09-08 while measuring B10.** The tax schema defines a province region as the sub-country unit under a country root — for Turkey an il. The repository's own end-to-end shipping test files `City: "Istanbul", Province: "Kadikoy"` and says why in its own assertion: *a domestic carrier prices on the district* — and Kadikoy is an ilce, not an il. Both readings compile and both suites are green; the two have never met because the cart's tax request sends the province ALWAYS EMPTY, so nothing has ever compared them. The cart address field carries no godoc line at all. Underneath both, `customer_address` has no sub-country column of any kind, and the cart's copy is made by the CALLER — so a shopper picking a saved address cannot bring a province to the cart, because the book never held one. This is the second half of ADR 0065's trigger: a district in a published quote input would be a FOURTH reading of a word that already has two |
| D1 | `/paytr/callback` sat outside every guarded prefix | Fixed — ADR 0028, and the residue answered 2026-09-08: a callback is recorded in the ring's LOG and not in `audit_log` (ADR 0056) — a provider is not an actor and the table has no column for the outcome. Every outcome now leaves a line, refusals included; four were silent, all of them the class where the handler RAN. What remains is a decision, A19, and not a residue |
| D2 | `allow_backorder` published and read by nothing | Fixed — ADR 0048 |
| D3 | The address book's storefront endpoints were unauthenticated | Fixed — ADR 0043. The residue was NARROWED 2026-09-08 by ADR 0057: b2b's copy and the cart's `customer_id` both go through one published comparison, and a tree-wide gate holds it. The cart's claim was measurably an oracle for a stranger's e-mail address as well as their spending window. **Still open where no identity is bound** — those two surfaces then serve the claim, because withdrawing them from a working installation costs more than the leak; binding a verifier closes it |
| D4 | `order_exchanges.completed_at` / `canceled_at` never written | Fixed. The audit built to catch this never caught it — see D16 |
| D5 | Archiving an order left no timestamp | Fixed — migration 000007 |
| D6 | Two repository-internal transactions could not compose | Fixed, and the entry had named the wrong second module |
| D7 | OpenAPI claimed `q` searched title and handle | Fixed |
| D8 | The link's far side named an entity with no Query provider | Fixed |
| D9 | Neither order nor payment ever soft-deletes: ten `deleted_at` columns nothing writes, behind reads that all carry `deleted_at IS NULL` | Fixed — ADR 0054. Both halves answered at once: the columns are DROPPED and the deletes are not taken on. Four of the ten indexes were UNIQUE among LIVING rows, so one hand-written UPDATE reused an idempotency key — measured, and on `payments` that key is what stands between a retry and a second charge. `fulfillment` 000003's claim that "D9's ten carry no such rule" was false and is corrected. A gate refuses the columns' return |
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
| D29 | The documents were audited against the code and 91 statements were false | Swept once. **One class now has a standing check 2026-09-08 — `TestTheRouteAddressesInTheProseExist`:** a route address written next to its verb must be one the tree binds. 393 addresses in scope, three defects on the day it was written — a measurement naming a moved endpoint, a module godoc naming a renamed one, and a plugin godoc dropping the trailing slash its own sibling file warns about. Residue: the other classes the sweep named — counts, universal negations, cross-references — have none, and the check reads 393 of the 491 addresses in the tree; the other 98 are in the changelog and in the ADR records up to 0058, which the check does not read. 17 of those 98 name a route the tree no longer binds |
| D30 | A plugin held four columns of personal data and declared none | Fixed. Every mutation proof needs `-count=1` |
| D31 | A data race in `TestCancellationActuallyStopsRemainingMigrations`, seen once | **Diagnosed and closed 2026-09-08 — ADR 0052.** golang-migrate v4.19.1 leaves the migrator's `isGracefulStop` unguarded next to an `isLocked` its mutex does guard, and the one line arming the write was gobit's `GracefulStop` send. Measured both ways: same error code, same version, same dirty flag, same regression test — the layer bought nothing. The send is gone and a gate refuses its return; the race is DORMANT rather than fixed, because the field is unexported |
| D32 | The language ratchet could not see a file until it was too late to matter | Fixed — the population is now tracked ∪ untracked-not-ignored |

## E. Out of framework scope — written, not forgotten

- **e-Fatura transmission** needs the merchant's certificate and an integrator
  contract. gobit owes the document, the numbering and the slot; the first two
  are built (ADR 0024).
- **Customer identity** is the embedder's job (ADR 0008, upheld by ADR 0043).
  ADR 0057 makes a bound one decide every storefront surface that names a
  customer; where none is bound, the cart and the b2b storefront believe the
  claim and say so.
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

- ~~**`authorized_at` does not exist.**~~ **DECIDED 2026-09-08: ADR 0054 — the
  surface keeps two moments and the column is not added.** An authorization
  moves no money, so it is not a money event; and while the hold is what a
  reader is asking about, the moment is already readable —
  `payment_sessions.updated_at` IS it, because every transition out of
  `authorized` leaves the status and re-authorizing is a no-op that writes
  nothing. `ListSessionsForReconciliation` and its index have always rested on
  that reading. After capture the moment is gone and nothing asks for it: a
  third field would have to cross the ADR 0004 read map, a DTO and the OpenAPI
  text for a reader nobody has named. The `refunded_at` half was already closed
  and stays closed — `refunds` carries no UPDATE anywhere, so `created_at` IS
  the refund moment.
