# Gap inventory

A working note, not a decision record. It says what this repository has, what it
does not, and — for each absence — whether it is a GAP or a DECISION. Those two
must never be listed together: acting on a decision as though it were a gap
means overturning an architecture on autopilot.

Everything here is measured against the code and cited. Where a section says
"measured on", that is the date the citation was checked.

This file is in English because ADR 0012 makes language a property of the file
and every new file is English.

---
---

## What to do, in order

Derived entirely from the measured sections below. Nothing here is a date or a
size estimate — it is an ORDER, chosen so that what gets more expensive the
longer it waits comes first.

Four sequencing facts govern the whole list:

- **A decision costs nothing today and more every week.** Every item in group A
  is a question that code is currently answering by accident.
- ~~**The public-surface decision (A1) has a clock on it.**~~ Answered on
  2026-09-05 by ADR 0026: `core/` is the published tree and a package enters it
  by an edit, not by a file move. A contract added from here on is classified
  when it is written, which is what the clock was about.
- **Four of the five AI features are blocked by something that is not AI**, and
  three of the four Turkey-specific items are blocked by one shared piece of
  plumbing. The model is never the first step, and the carrier is not the first
  step either.
- **Two apparently unrelated features need the same single change.** Saved cards
  and subscriptions both need a stored payment instrument; installments and
  multi-vendor both need the money model to admit that what the customer pays
  and what the merchant receives can differ. Each pair is one change, not two.

### A. Decisions — no code until they are answered

| # | decision | what it blocks | section |
| --- | --- | --- | --- |
| A1 | ~~**Which packages become public**~~ **Answered 2026-09-05: ADR 0026.** Fourteen packages under `core/`, 7.8% of the codebase, no commerce model among them. Four audits enforce it | ~~the library transition~~ — the remaining half is the composition root, still a program | Importable core |
| A2 | **What gobit is legally** — data controller, or a library whose embedder is the controller | every KVKK item; ADR 0025 points at the second answer, which changes the obligation from "implement consent" to "publish the hooks and the erasure contract" | Turkey-specific |
| A3 | **May the customer pay a different amount than the merchant receives?** | installments (vade farki) AND multi-vendor commission. Today four guards including DB CHECKs forbid it | Turkey-specific, Commerce models |
| A4 | **Invoice retention vs KVKK erasure** | all erasure work; unwritten, somebody deletes an invoice and puts a hole in the series ADR 0024 exists to prevent | Observability and security |
| A5 | **Group-price selection for a customer in several groups** | B2B group pricing; the discount context has the identical hole | Commerce models |
| A6 | **`allow_backorder`: give it a reader or stop publishing it** | pre-order; today a published flag does nothing, and the flags measurement names it as the live proof of ADR 0009's second error class | Commerce models |
| A7 | **Does the panel become an admin-API client?** (supersedes ADR 0011) — and if so, the auth story, because a cookie on `/admin/v1` destroys its CSRF immunity and a token in JS is the exposure the cookie avoids | the SPA, and the extension-point design | Admin panel |
| A8 | **Catalog cacheability** — sales channel in the path, single-channel opt-in, or per-key caching accepted | edge caching. The repo already argued the default answer in the GraphQL handler | Storefront speed |
| A9 | **Customer identity: ADR 0008 stands or is superseded** | customer login, passkeys, a customer-facing assistant, A/B assignment, and it is why the address book is unauthenticated | Storefront speed |
| A10 | **pgvector: reopen ADR 0015 or not** | semantic search, visual search, embedding recommendations | AI subsystem |
| A11 | **Where translated content lives** | multi-language, and it touches the search index, the panel and the invoice | Platform features |
| A12 | **JWT TTL policy** — there is no refresh flow, so the TTL is an unstated trade | session design | Observability and security |
| A13 | **Metrics posture** — OTLP-only, or expose a scrape endpoint | the "measurable speed" claim | Observability and security |
| A14 | **Price history: promote the accidental retention or drop it** | forecasting | AI-powered features |

### B. Foundations — each unblocks several features

| # | foundation | unblocks | section |
| --- | --- | --- | --- |
| B1 | **A guarded inbound-callback class** — signature verification, replay window, body limit, rate limit, audit | four carriers, e-invoice, every payment provider. **Build once; today `/paytr/callback` is the only example and it is unguarded** | Turkey-specific |
| B2 | **Storefront filter surface** — price, category, tag, option value, in-stock, sort | NL search, the panel, every "find me" feature. **Highest leverage item on the list** | AI-powered features |
| B3 | **Storefront vocabulary endpoints** — collections, categories, tags | NL search; today there is no public way to resolve a word to an id | AI-powered features |
| B4 | **Review module** | moderation (the AI brief's first use case), summaries, Q&A | AI subsystem |
| B5 | **Order ↔ fulfillment link, and something that creates a fulfillment** | the order timeline, carrier tracking, "where is the parcel". The link definition was assigned to a module that never declared it | Platform features |
| B6 | **A money-event read surface** — `payments.captured_at`, refunds | the timeline's two most-asked facts, today unreachable through the read layer | Platform features |
| B7 | **Inventory movement ledger + inventory events** | forecasting, real-time stock, and an audit trail stock does not have | AI-powered features, Storefront speed |
| B8 | **Customer module events** (`customer.deleted` at minimum) | erasure — today deleting a customer notifies nobody and Principle 2.2 forbids the cascade | Turkey-specific |
| B9 | **Stored payment instrument** — provider contract + table | saved cards AND subscriptions, in one change | Storefront speed, Commerce models |
| B10 | **Carrier-capable quote input and a tolerant shipment state machine** — district, dimensions/desi, more statuses (including iade), and out-of-order webhook tolerance | any real carrier | Turkey-specific |
| B11 | **Order addresses** — the cart's addresses never reach the order | invoicing, shipping labels, B2B | Storefront speed |
| B12 | **Outbound delivery machinery** — retry and a dead-letter queue on the outbox relay | webhooks, ERP/Slack integration. The bus deliberately has neither | Platform features |
| B13 | **Plugin host: let a plugin register a job** | any plugin needing a retry pass, including outbound delivery | Platform features |
| B14 | **Order line-item entity in the read layer + date filter + index** | demand analytics and forecasting | AI-powered features |
| B15 | **File read-back, file events, product-image ↔ upload link** | anything that looks at a photo | AI-powered features |
| B16 | **A suggestion store** — system proposes, human applies | forecast and category suggestions. The pattern exists (`sagawatch`, ADR 0017); the storage does not | AI-powered features |
| B17 | **KVKK erasure, export and retention** | a legal requirement; A2 and A4 come first | Turkey-specific |
| B18 | **A per-column round-trip test for every module** | nothing — and it prevents the defect class that shipped twice in one change | Common Go mistakes |

### C. Features — after the above

| # | feature | waits on |
| --- | --- | --- |
| C1 | **Back-in-stock waitlist** — the cheapest real feature; every part exists and only a table is missing | — |
| C2 | **Order timeline** — the support view; mostly a read-side composition over facts that already exist | B5, B6 |
| C3 | **Operator assistant in the panel** — sixty-one primitive interop methods are already a tool catalogue, and identity exists inside the panel | a return-creation surface |
| C4 | **Consent records and data-subject endpoints** | A2, B17 |
| C5 | **Outbound webhooks** (NATS after, if anyone asks) | B12, B13 |
| C6 | **Carrier plugins** (Yurtici, Aras, MNG, PTT) | B1, B10 |
| C7 | **Installment table** + iyzico/Param providers | A3 |
| C8 | **Digital product delivery** — entitlement, expiring link, re-download policy | — |
| C9 | **B2B: quotes, terms, minimum order** | A5 |
| C10 | **NL search layer** | B2, B3 |
| C11 | **Review summaries and Q&A** | B4 |
| C12 | **Subscriptions** — a second axis on the order, not a fifth status | B9 |
| C13 | **Feature flags, then A/B** | A9 for the assignment key |
| C14 | **Panel: extension points, then the SPA if A7 says so** | A7 |
| C15 | **Multi-language** | A11 |
| C16 | **Real-time stock** | B7, plus a fan-out the bus cannot do today |
| C17 | **Edge caching** | A8 |
| C18 | **Multi-vendor marketplace** — changes what every money path means; last, deliberately | A3 and most of the above |

### D. Corrections — small, and some are live defects

- **D1** `/paytr/callback` sits outside every guarded prefix: **no auth, no rate
  limit, no idempotency, no audit, no CORS, and no body-size limit anywhere in
  the core.** Its only protection is the HMAC inside the handler. (Folded into
  B1.)
- **D2** `allow_backorder` is published and does nothing (see A6).
- **D3** The address book's storefront endpoints are unauthenticated and keyed by
  a path id — personal data anyone can read and change. A known consequence of
  ADR 0008, in its sharpest form.
- **D4** `order_exchanges.completed_at` and `canceled_at` exist and are never
  written; there is no Complete or Cancel query for an exchange.
- **D5** Archiving an order leaves no timestamp — the status flips and nothing
  records when.
- **D6** Two repository-internal transactions (tax, region) cannot compose into a
  service transaction.
- **D7** ~~The OpenAPI text claimed `q` searches title and handle; it searches
  title only.~~ Fixed 2026-09-05.

### E. Out of framework scope — written, not forgotten

- **e-Fatura transmission** needs the merchant's certificate and an integrator
  contract. The framework owes the document, the numbering and the slot; the
  first two are built (ADR 0024).
- **Customer identity** is the embedding application's job (ADR 0008), unless A9
  supersedes it.
- **A/B assignment** likewise, if A9 stands: the framework has no visitor.

### F. Standing work

- **Translation ledger: 260 files.** ADR 0012 lets it only shrink.
- **Panel: twelve of sixteen modules have no screen**, nothing can be created or
  deleted from it, and there is no extension point for a plugin to add one.

## Data layer — measured 2026-09-04

The stack matches the shape you would specify for this workload, with one
substitution and three genuine gaps.

### Matches

| Expected | In this repository |
|---|---|
| PostgreSQL + pgx, no ORM | `jackc/pgx/v5 v5.10.0`; no gorm, no ent |
| sqlc for typed queries | `internal/modules/*/sqlc.yaml`, regenerated by `make gen` |
| Money never in float | `int64` minor units throughout |
| Row locking against oversell | `SELECT … FOR UPDATE` on the inventory rows |
| Postgres full-text search | `plugins/searchpg` |

**Money.** No monetary field is a float anywhere. More than that, the repository
treats float as a hazard at every JSON boundary and says so where the risk
lives: `fulfillment/service/interop.go`, `order/service/interop.go`,
`payment/service/interop.go`, `promotion/service/interop.go`. The
argument is always the same — an event payload decoded through `float64` loses
cents silently — so amounts cross module boundaries as strings and are parsed to
`int64` explicitly.

**Oversell.** Guarded by row locks, not by optimistic retry:
`inventory/queries/inventory_levels.sql`,
`inventory/queries/inventory_items.sql`,
`inventory/queries/inventory_reservations.sql`. The lock ORDER is written
down as a contract rather than left to chance (`payment/service/service.go`
package doc: collection → session → capture), because two flows taking the same
two rows in opposite orders is how a deadlock is manufactured.

### Substitution

**Migrations run on `golang-migrate`, not goose or atlas.**
`golang-migrate/migrate/v4 v4.19.1`, per-module version tracking, embedded via
`go:embed` in each `module.go`.

This is not a gap, and it has a consequence worth knowing before anyone swaps
it: golang-migrate takes a PostgreSQL advisory lock in key class 0 — the WHOLE
uint32 range — and waits for it on `context.Background()`, a wait that is
unbounded and uncancellable. Every other advisory lock in this repository picks
a key class to stay out of its way (ADR 0019: 1 = order spending,
2 = scheduled jobs, `0x6C696E6B` = link). Replacing the migration runner means
re-checking that key-space agreement.

### Gaps

1. ~~**No read cache. Nothing is cached, anywhere.**~~ **NOT WARRANTED —
   measured 2026-09-05.** The premise was wrong, and the measurement says so.

   Measured with pgbench against the load fixture (52,004 products), 16 clients,
   on this machine:

   | query | latency | throughput |
   | --- | --- | --- |
   | storefront listing, 20 rows | 0.47 ms | 33,830 /s |
   | single product by handle | 0.36 ms | 44,976 /s |
   | `count(*)` over the catalog | 3.03 ms | 5,273 /s |

   Against that, the Go side of one storefront GraphQL request was benchmarked
   at 374 us and 8,421 allocations — about 2,700 requests per second per core.

   **The database is roughly twelve times faster than the code that formats its
   answer.** A read cache relieves the side that is already ahead. The honest
   next lever, if a ceiling is ever measured, is the response path — and even
   that is worth touching only once somebody has a real ceiling rather than a
   worry.

   The count query is the one expensive read, and it is already optional
   (`with_count=false`), which is what the original finding pointed at without
   the throughput number to weigh it against.

   **What would reopen this.** A cache stops being a trade against correctness
   and starts being necessary when the database is the constraint: a catalog
   large enough that the listing's index scan stops being a scan of twenty rows,
   an enrichment leg that turns one request into many queries, or a deployment
   where the database is a network hop away and the latency is the round trip
   rather than the work. None of those is today's shape, and the measurement
   above is what a future one has to beat.

   The original finding: Redis is present and used for three things — the event
   bus, rate limiting and idempotency — and for nothing else. There is no
   product-detail cache, no category-listing cache, and therefore no
   invalidation path on write. (One cache does exist and was missed: the
   GraphQL endpoint caches PARSED DOCUMENTS, with its own admission rule — see
   `product/graph/handler.go`. It caches queries, not data.)

2. ~~**Sessions are not in Redis, and there are two different session stories.**~~
   **WRONG — corrected 2026-09-05.** The decision IS written down, and
   revocation works.

   `auth/service/session.go` holds it in full: there is no session record on
   purpose, and instead each identity carries a SESSION ANCHOR — a token issued
   before the anchor is refused. `AuthenticateAdmin` reads the anchor on every
   verification and rejects on it, so a logout or a password change drops the
   user's tokens IMMEDIATELY, on every device, without touching the signing
   secret. Six tests cover it, including that a token obtained after the logout
   still works and that an anchor on a second provider drops the token.

   So an admin session is revocable and the claim that "there is nothing to
   revoke them in" was simply false. What is absent is PER-DEVICE revocation —
   dropping one token and leaving the others — and that absence is a written
   refusal rather than an oversight: it needs a `jti` in the token and a
   blacklist every request reads, which gives up statelessness for a capability
   nothing has asked for.

   The original finding, kept because the shape of the mistake is worth having:
   "Whether this is a gap depends on a decision nobody has written down yet: are
   admin sessions meant to be revocable before their token expires? Today they
   are not, because there is nothing to revoke them in." The reading looked for
   a session TABLE, did not find one, and concluded there was no mechanism; the
   mechanism is a timestamp on a row that already existed.

3. **Carts live in PostgreSQL, not Redis** (`cart/migrations/000001_cart_init.up.sql`).
   Listed here for completeness rather than as a defect: a cart holds inventory
   reservations and must be transactional with them, so moving it to Redis would
   trade a correctness property for a latency one. If cart-write latency ever
   becomes the complaint, the answer is a cache in front, not a move.

### Search

`plugins/searchpg` implements search on PostgreSQL full-text — a plugin, so an
installation that outgrows it replaces it without touching the core. No
Meilisearch, Typesense, Elastic or OpenSearch dependency exists.

Two things measured about it are worth carrying forward: the search index was
found to silently not work for Turkish because the cluster had been created with
`--locale=C` (now a startup check and ADR 0015), and a godoc claiming an index
was used turned out to be wrong under the planner — which is why index claims in
this repository are now asserted by reading `EXPLAIN` in an integration test.

---

## Must-have commerce features — measured 2026-09-04

Measured against the checklist a shop operator would hand you. Six of eight are
covered; the two that are not are the same shape of defect, and one of them has
correctness consequences.

### Covered

**Product and variant model — complete.** The combination model is a real one,
not a flattened list: `product_option`, `product_option_value` and the join
`product_variant_option_value` (`product/migrations/000001_product_init.up.sql`)
express colour × size properly. SKU sits on the variant, and
`inventory_items.sku` carries a UNIQUE index
(`inventory/migrations/000001_inventory_init.up.sql`) so stock is per SKU.
Collections, categories, tags and images are all modelled.

**Idempotency — two independent layers.** An HTTP middleware
(`core/http/idempotency.go`, Redis-backed) and, underneath it, the payment
module's own key with a UNIQUE index as the last line of defence
(`payment_sessions_provider_idempotency_uniq`). The second is what makes a saga
step safe to retry when the first is not in play.

**Payment behind an interface.** `coreprovider.PaymentProvider`, resolved from a
registry by id. `plugins/paymentpaytr` proves the callback-driven case end to
end; `plugins/paymentstripe` is a deliberate skeleton. Reconciliation against
the provider's own ledger landed as `internal/jobs/paymentrecon` (ADR 0020).

**RBAC.** Forty scopes across the modules (`product:write`, `order:read`,
`order_returns:write`, `payment_refunds:write`, …), enforced by
`corehttp.RequireScope` on the admin routes.

**Returns, exchanges and claims** exist as records with services behind them:
`order/service/aftersales.go` — `CreateReturn`, `CreateExchange`, `CreateClaim`,
each guarded by `requireLiveOrder`.

### Covered, but not the way the checklist assumes

**The order state machine is SPLIT, and the split is the architecture.**

`models.OrderStatus` has four values — `pending`, `completed`, `archived`,
`canceled` — and transitions are enforced in code, not by convention:
`order/service/order.go` goes through `s.transition(...)`, and an illegal
one produces `transitionError(action, orderID, required, actual)`.

The states the checklist calls `paid`, `fulfilled`, `shipped` and `delivered`
are not missing — they live in the modules that own those facts, each with its
own enforced transition table: `payment/models/status.go` (`AuthorizeAction`,
`CaptureAction`, `CancelAction`, all pure functions over the current status) and
`fulfillment/models/status.go` (`Action`). That follows from module isolation:
the order module cannot know whether money moved, so it must not carry a `paid`
state it would have to be told about.

The consequence is a real one and it is unowned: **no single place answers
"where is this order right now"**. Assembling that view means reading three
modules. It is the question an operator asks first and a support agent asks
every time.

What it would touch: not a new state column — that would put the same fact in
two places and guarantee they drift. A read-side projection, in the Query layer
or a workflow, that composes the three.

### Gaps

1. ~~**No outbox. An order can be committed and its event lost, with nothing to
   notice.**~~ **CLOSED 2026-09-05, ADR 0023.** The event is written inside the
   transaction that promises it and a relay publishes it every minute; the
   direct publish stays as the fast path and the two share one id, so a
   subscriber idempotent on it cannot tell them apart.

   Only `order.placed` writes through it today — the one event with a real
   subscriber. Converting publishers with no subscriber would be ADR 0009's
   error class.

   The original finding:

   `order/service/order.go` states the ordering honestly: the order and
   everything belonging to it commit in a single transaction, and only then is
   `order.placed` published — *"a publishing failure does not drop the order"*.
   The event bus documents its own guarantee just as honestly
   (`core/eventbus/eventbus.go`): in-memory is at-most-once and
   loses events when the process dies; Redis is at-least-once and resumes.

   Neither statement covers the window between the two. If the process dies
   after the commit and before the publish, the order exists and the event never
   happened — so no confirmation mail is sent, and nothing anywhere records that
   one is owed. This is the SAME shape as the payment hole ADR 0020 closed: a
   committed local fact whose downstream effect silently did not occur.

   The word "outbox" appears exactly once in the repository, as a hypothetical
   in a comment (`cmd/server/migrate.go`).

   What it would touch: core, as a table plus a publisher that writes the event
   in the SAME transaction as the business write and hands it to the bus
   afterwards. The worker half already exists — `internal/core/job` shipped with
   ADR 0019, so this no longer needs new machinery, only the table and the
   discipline of writing through it. It also has a real consumer today
   (`order.placed` → notification), which is what ADR 0009 requires before
   building anything.

2. ~~**No audit log.**~~ **CLOSED 2026-09-05.** Every admin write — including a
   REFUSED one — records who called what and what came back, in `audit_log`.

   It records the REQUEST rather than the change, which is the answer to the
   "hard half" this finding named: a diff would be a contract in fifteen
   modules and a cost on every request, and a bare "updated product X" would be
   worth nothing. What is stored is what an incident starts from — who touched
   this surface, when, did it succeed — and the WHAT is read from the record,
   which carries its own `updated_at`.

   The original finding: Nothing records who changed what. The admin API
   authenticates and authorises every write (forty scopes), and then forgets it
   happened. The only durable trace of any change is the row's `updated_at`.

   The words "audit record" do appear — but about workflow executions
   (`internal/core/workflow/store.go`), which is a different thing: it tells
   you a saga ran, not that a person changed a price.

   What it would touch: core, as a middleware plus a table, because the actor
   and the scope are already on the request. The hard half is deciding what a
   change record contains — a diff is expensive and a bare "updated product X"
   is not worth writing.

3. ~~**Guest cart cannot be adopted by a customer who logs in.**~~
   **WRONG — corrected 2026-09-05.** Adoption exists and is guarded. `UpdateCart`
   writes a `customer_id` onto a guest cart, and the rule next to it refuses
   handing an OWNED cart to a different customer
   (`cart_customer_mismatch`); the integration test covers both directions,
   including that a refused handover writes nothing. What the earlier reading
   missed is that the capability is a field on the update rather than a method
   with "claim" or "adopt" in its name, which is what the search looked for.

   The narrower thing that really is missing: MERGING a guest cart into a cart
   the customer already has. Adoption gives the customer a second cart; nothing
   folds the two into one. That is a policy question (whose quantities win,
   which cart's promotions survive) rather than a plumbing gap, which is why it
   is left rather than guessed.

   The original finding, kept because the shape of the mistake is worth having:
   "Line-item merge works, but there is no `AssignCustomer`, `ClaimCart` or
   equivalent: a guest who signs in loses their cart. This is entangled with the
   storefront-identity decision (ADR 0008)." The ADR 0008 argument was the part
   that made it sound settled; it is not relevant here, because the customer id
   arrives from the embedding application exactly as ADR 0008 says it should.

4. **No carrier integration.** `fulfillment` ships with the manual/test provider
   only. The provider interface and registry exist and are proven by the payment
   slot, but no plugin fills this one, so there is no rate calculation, no label
   and no tracking from a real carrier.

5. ~~**No invoicing, and nothing for the Turkish e-invoice regimes**~~
   **PARTLY CLOSED 2026-09-05, ADR 0024.** There is an invoice module: the
   document, its lines, its parties, its status, and the numbering.

   **What a framework can close here, and what it cannot.** It cannot file an
   invoice on a merchant's behalf — that needs the merchant's own certificate
   and a contract with an integrator — so no amount of work in this repository
   produces a filed e-fatura. What it owes is the document, its numbering, and a
   place for the transmission to plug in. The first two are done.

   The numbering is the part with a decision in it. An invoice number is
   allocated by an UPDATE on a series ROW inside the same transaction that
   writes the document, NOT by a database sequence — which is the opposite of
   what the order module does for its order numbers, and for a legal rather than
   a technical reason. A sequence advances outside the transaction, so a
   rollback burns its number; for an order number that hole is harmless, and for
   an invoice serial it is what a tax authority reads as a document that was
   issued and then hidden. See ADR 0024.

   Three consequences follow and each is enforced: there is no draft status (a
   draft would need a number, and a number given to a draft that is abandoned is
   the hole itself), a canceled document keeps its number and stays in the table
   (deleting it puts the hole in from the other end), and an issued document is
   immutable.

   The concurrency test found a real defect while this was being written: the
   look-then-create arrangement for opening a new year's series cannot recover
   from its own race, because a unique violation POISONS the transaction in
   PostgreSQL and the fallback read has nothing left to run in. It is one
   `INSERT ... ON CONFLICT` statement now.

   **The order path landed too (2026-09-05).** `POST /admin/v1/orders/{id}/invoice`
   assembles the document from the order and issues it; `GET` on the same path
   says which document the order has. The assembling is a WORKFLOW, because the
   invoice module knows no orders and the order module knows no documents.

   Two parties come from the request body and the lines come from the order, and
   the split is not arbitrary: the seller's legal details are the shop's own
   configuration, and the buyer's tax number is not in this repository's
   customer model at all. A framework that guessed them would produce a document
   wrong in the one way a document must not be. The buyer's e-mail is the single
   field the order does know, so it is filled in.

   Issuing twice does NOT spend a second number: the order-to-invoice link is
   read first and the existing document is returned, with a 200 instead of a 201
   so a client that retried after a timeout can tell whether its first attempt
   landed. The residual is written down rather than claimed away — two operators
   pressing at the same instant can both issue, and the second binding is then
   REFUSED as a cardinality conflict, so the shop is told, with both identifiers,
   that it has a document to cancel.

   **Still open:** the transmission itself. That is a plugin's job — it needs the
   merchant's certificate and an integrator contract — and none ships.

   The original finding: every "fatura"
   in the codebase is a billing ADDRESS. For a shop selling in Turkey this is a
   legal requirement, not a feature — and it is the one item on the checklist
   that no part of the framework currently touches.

6. **No iyzico plugin.** `plugins/` holds paytr, stripe (skeleton), smtp, s3,
   webpush, searchpg, and two error-reporting plugins. Adding iyzico is plugin
   work, not framework work — the PayTR plugin is the template, and it is the
   callback-driven shape iyzico also uses.

---

## Performance and operations — measured 2026-09-04

Three of five are in place. The two that are not are the two that decide how
this behaves at scale, and one of them has already shown up as a workaround.

### In place

**Connection pool is fully configurable** — `MaxConns`, `MinConns`,
`MaxConnLifetime`, `MaxConnIdleTime` with defaults of 10/2/1h/30m
(`core/db/db.go`). Worth knowing why the settings are verified by
READING THEM BACK from `pool.Pool().Config()` rather than from the config
struct: a mutation that deleted `pgCfg.MaxConns = cfg.MaxConns` outright passed
every test, because the startup log kept printing the CONFIGURED number while
the pool ran on the library default. The operator would have read a limit that
was not in force.

**N+1 is treated as a named defect, not a habit.** Set fetches are single
queries by contract, and the godoc says so where it matters
(`payment/service/service.go`: *"PaymentCollectionsByIDs fetches the identifier
set in a SINGLE query (no N+1)"*). Joins are written out explicitly in the
`.sql` files rather than assembled in Go.

**Images: no in-application resizing, and the CDN is the documented posture.**
Nothing decodes or resizes an image anywhere. The file module states the split
plainly (`file/service/service.go`: *"in an object store the file is served
by the CDN, the application never…"*), `plugins/files3` says the same about the
bucket, and `file/api/serve.go` sets cache headers so a CDN or reverse proxy
may legitimately store the response. The local disk provider exists for
development and says so.

**Graceful shutdown and health endpoints** — `/health` and `/ready` as separate
answers, with `ShutdownTimeout` letting open requests finish
(`core/http/server.go`). The split is load-bearing: Postgres GATES
traffic (`/ready` 503s without it) while Redis only DEGRADES, and which side a
dependency lands on is decided by what its loss does to a request.

### Gaps

1. ~~**No pprof and no benchmarks. Zero `func Benchmark` in the repository.**~~
   **CLOSED 2026-09-05.** Five benchmarks on the paths that run per request,
   and a pprof listener of its own that is off unless configured.

   The first numbers, on an 8845HS:

   | benchmark | per op | allocs |
   | --- | --- | --- |
   | `StorefrontQuery` (24 products, 3 variants each) | 374 us | 8,421 |
   | `ComputeDiscounts` (20 lines, 4 promotions) | 4.5 us | 18 |
   | `AllocateAcross` (20 lines) | 1.3 us | 2 |
   | `AssembleTotals` (20 lines) | 124 ns | 0 |
   | `ApplyTaxResponse` (20 lines) | 89 ns | 0 |

   The finding is the spread. The cart arithmetic — the part that was written
   most carefully, with the remainder rules argued line by line — allocates
   NOTHING and costs about a tenth of a microsecond. The GraphQL read surface
   costs three thousand times as much and allocates 8,421 times per request,
   which is the same order as the database work it was compared against
   (67 ms -> 0.65 ms on the count query). It is the obvious first place to look,
   and nothing before this could have said so.

   The listener is separate rather than a route on the API, because a profile
   takes as long as it was asked to take and `WRITE_TIMEOUT=30s` cuts the
   30-second CPU profile exactly in half. It is unauthenticated, so a
   non-loopback bind is REFUSED at startup outside development, and an arch
   test keeps `net/http/pprof` — which publishes itself through a package-level
   global on import alone — out of every other file.

   The original finding: this is the sharpest inconsistency in the codebase,
   because measurement discipline is otherwise strong: the SQL side is measured
   with `EXPLAIN` read inside integration tests, load fixtures run to 52,000
   rows, and godocs carry real numbers (2.9 ms, 0.56 ms, 67 ms → 0.65 ms). All
   of it is DATABASE-side.

   Nothing measures the Go side. There is no profile endpoint to attach to a
   running process and no benchmark to catch a regression in a hot path — so an
   allocation regression in pricing, promotion computation or JSON encoding
   would be invisible until it showed up as latency in production.

2. ~~**Pagination is offset-based everywhere.**~~ **CLOSED 2026-09-05.**
   The four listings whose tables grow without bound take a cursor: products
   (admin and storefront, REST and GraphQL), orders, customers and carts.

   Measured on 52,000 products with the listing index in place:

   | page | offset | keyset |
   | --- | --- | --- |
   | first | 0.31 ms | 0.06 ms |
   | ~5,000 in | 4.63 ms | — |
   | ~50,000 in | 34.71 ms | 0.08 ms |

   Offset is linear in depth because the database walks and DISCARDS every
   skipped row; keyset is flat because the ordering key goes into the index
   condition. The 423x at the deep end matters less than the SHAPE: a catalog
   that grows makes offset worse and leaves keyset where it was.

   Offset is NOT removed and the change is NOT breaking. A page-numbered admin
   screen needs to jump to page seven, which a cursor cannot do, and at the
   depths such a screen reaches offset is cheap. `after` is additive, and the
   two are refused together because they name different positions.

   The finding worth carrying forward is the SQL shape. Writing the bound as
   `@after IS NULL OR (created_at, id) < (...)` measures perfectly and then
   degrades: Postgres plans a statement per call for its first five executions
   and folds the OR away, so a test sees an Index Cond; on the sixth it switches
   to a generic plan, the OR survives into a Filter, and the seek becomes a full
   index walk — 50,001 rows removed by filter, 4.3 ms instead of 0.065 ms, with
   no code change at that moment. The sentinel form
   (`COALESCE(@after, 'infinity')`) has no OR left to survive and holds under
   both plans. An integration test reads the plan rather than a timing, because
   a timing cannot tell the two apart on a small table.

   **The rest of the listings keep offset alone, and that is a decision rather
   than a remainder.** Offset only costs anything at DEPTH — the table above is
   the whole argument — and a listing whose table is configuration-sized never
   goes there. Tax rates, shipping options, regions, currencies, countries,
   sales channels and customer groups are counted in hundreds; a cursor on them
   would be ceremony, and every parameter that exists has to be documented,
   tested and honored forever. The rule to apply to the next listing is
   therefore: **a cursor where the rows grow with the shop's trade, offset alone
   where they grow with its configuration.** Listings scoped to one parent — a
   product's variants, an order's returns, a company's employees — are bounded
   by the parent and fall on the offset side too.

   The original finding: 101 `limit` and 96 `offset`
   occurrences across the module APIs; no cursor, no `after`, no `before`.

   The cost is already visible rather than theoretical. Offset pagination needs
   a total count for the UI to render page numbers, and that count query was
   measured at 67 ms on the storefront listing and made OPTIONAL to get it to
   0.65 ms. Making the count optional is a workaround for offset pagination, not
   a fix — and deep pages still make Postgres walk and discard every skipped
   row, which gets worse exactly as the catalog grows.

   What it would touch: every list endpoint's response envelope, which is a
   published API shape, so it is a breaking change and belongs in one deliberate
   pass rather than module by module. The ordering columns already exist
   (`created_at DESC` with an id tiebreak in most indexes), which is the part
   that is usually missing.

---

## Observability and security — measured 2026-09-05

The checklist: slog structured logging, OpenTelemetry traces, Prometheus
metrics, JWT + refresh or opaque sessions, argon2id, rate limiting, input
validation, CSRF, PII in logs, and a KVKK retention/erasure flow designed from
the start.

### In place

1. **Structured logging with slog**, everywhere, with a request id on every line
   and a level chosen from the status (5xx error, 4xx warn, else info). An arch
   test audits the log assertions themselves.

2. **OpenTelemetry traces** over OTLP gRPC. There is no default endpoint on
   purpose — left empty, tracing is off and the process opens no connection —
   and `OTEL_EXPORTER_OTLP_INSECURE=true` is REFUSED at startup outside
   development, because traces carry paths, identities and error messages.

3. **Rate limiting**, in memory and in Redis, scoped per surface.

4. **Input validation**: unknown JSON fields are refused rather than ignored,
   bodies are size-bounded, and the errors are typed so the status is chosen by
   the error class rather than by the handler.

5. **CSRF**, in two layers and only where a cookie exists: the panel's cookie is
   SameSite=Strict AND its state-changing requests check the Origin header,
   which is what closes the subdomain case SameSite leaves open. The admin API
   needs neither, because it takes identity from a header only (ADR 0011) — a
   browser cannot be made to send one cross-site.

6. **PII in logs is better than the checklist assumes.** Request and response
   BODIES are never logged. The access log masks the query string against a
   deny-list that already carries the personal markers — mail/posta, phone/
   telefon/gsm/msisdn, iban, tckn, vkn, cvv/cvc, pin — as well as the credential
   ones, matched case-insensitively and by substring. The deny-list's cost is
   written down where it is defined: a parameter named tomorrow is logged
   unmasked until the name is added, and an allow-list was impossible because
   core cannot know a module's filter names (Principle 2.4).

   Error reports go further: `errorreport/policy.go` splits attributes into
   those that may travel and those that may not, and REPORTS which were removed
   rather than dropping them silently.

7. **Sensitive configuration is refused rather than warned about**: an
   unencrypted collector, a short signing secret and a non-loopback profiling
   listener all stop the startup in a shared environment.

### Present, but not the way the checklist names it

1. **Metrics are OTLP, not Prometheus.** There is a meter provider and an
   exporter (`otlpmetricgrpc`) and an export interval, but no `/metrics`
   endpoint for Prometheus to scrape. An installation that wants Prometheus
   points a collector at the OTLP endpoint and scrapes the collector; one that
   wants gobit to expose the endpoint itself does not have that today.

2. **There is no refresh token, and the reason is a design that already
   exists.** The admin token is a JWT, and revocation works through a SESSION
   ANCHOR: a token issued before the identity's anchor is refused, and a logout
   or a password change moves the anchor. So the token is stateless AND
   revocable, which is the property a refresh token is usually introduced to
   buy. What is missing is the other property — per-device revocation — and that
   is a written refusal (`auth/service/session.go`), not an oversight.

   The open question is the TTL: without a refresh flow, `JWT_TTL` is a straight
   trade between how long a leaked token lives and how often an operator signs
   in. Nothing in the repository states what that trade should be.

3. **Passwords are bcrypt, not argon2id.** The cost is configurable and the
   verification path is constant-time against a dummy hash so a missing user and
   a wrong password take the same time. bcrypt is not a defect — it is the
   weaker of the two against GPU attack, and the module's own reasoning about
   hashing is written for API keys (SHA-256, and why NOT bcrypt there) rather
   than for passwords.

### Gaps

1. **There is no KVKK/GDPR erasure or export flow, and deletion is SOFT
   everywhere.** `DeleteCustomer` stamps `deleted_at`; nothing hard-deletes and
   nothing anonymises. The word KVKK appears exactly once in the repository, in
   a notification test, observing that not storing a second copy of the
   recipient address keeps the number of places to clean small — which is the
   right instinct and the only trace of the requirement anywhere.

   Personal data is currently in at least seven places: `customer` (e-mail,
   names, phone), `customer_address`, `orders.email`, `carts.email`,
   `auth_user`, `notification_deliveries` (by reference, not address), and
   `invoices` (buyer name, e-mail, address, tax number).

   What it would touch: a core capability modules opt into — the same shape the
   link registry and the Query layer already have — because the alternative is
   an erasure that each module implements its own way and one of them forgets.

2. **An invoice is a RETENTION OBLIGATION that conflicts with erasure, and
   nothing writes the conflict down.** The invoice module's whole design says
   the document is immutable and its number may never leave a hole (ADR 0024),
   and the document carries the buyer's name, address and tax number. A KVKK
   erasure request cannot delete it: the legal retention period wins. That is
   almost certainly the correct answer — and it has to be a written decision,
   because the alternative is somebody implementing erasure later, deleting an
   invoice row, and putting the hole in the series that ADR 0024 exists to
   prevent.

3. **No guard keeps personal data out of a log line.** No line carries an e-mail
   or a name today — that was checked, not assumed — but it holds by discipline
   rather than by construction. The query-string deny-list is enforced in code;
   what an individual module writes into its own log is not checked by anything,
   and this repository already has an arch test for the shape of log assertions,
   so the machinery to check it exists.

4. **Nothing states how long anything is kept.** Idempotency records and the
   audit log have retention settings; carts, orders, notification deliveries and
   the audit log itself have no stated lifetime. A retention policy is the half
   of KVKK that is not erasure, and it is the half that has to be designed
   before there is data to apply it to.

---

## Common Go mistakes — measured 2026-09-05

The checklist: goroutine leaks (an exit path for every `go`, use errgroup),
transaction boundaries in the service and not the repository, no global state,
and repository tests against a real Postgres rather than mocks.

**This list is mostly already satisfied**, and the value of measuring it is the
three places where the answer is "yes, but".

### Goroutines: all fourteen have an exit path

There are fourteen `go` statements in production code and every one of them
terminates: eleven are tracked by a `sync.WaitGroup`, two exit when a channel is
closed (`for event := range r.queue`), and the Redis consumer loop returns on
`b.ctx.Err()`.

**errgroup is not used and is not in go.mod.** That is not the gap it looks
like: errgroup buys first-error propagation and cancellation, and the places
that fan out here want the OPPOSITE — the parallel saga branch runner collects
every branch's result including the failures, because a compensation has to know
which branches ran. Where a first error should stop the rest, the code cancels a
context instead.

One nuance worth having: the event bus's shutdown starts a goroutine that waits
on the handler WaitGroup and closes a channel, and the caller selects between
that channel and the shutdown budget. If a handler never returns, the SHUTDOWN
returns on the budget — correctly, because the process must be able to stop —
and that one waiter goroutine stays for the life of the process. It is bounded
(one per Shutdown, and Shutdown runs once) and it is the right trade, but it is
the one goroutine in the repository whose exit depends on somebody else's code.

`migrate.go` shows the detail that makes this class safe: `done` is buffered to
1, so when the context runs out the migration goroutine still completes its send
and exits rather than blocking forever on an unread channel.

### Transactions: the boundary is in the service, with a real nuance

Six of sixteen modules carry an open transaction through the CONTEXT — cart,
fulfillment, inventory, invoice, order, payment — and in all six the boundary is
decided by the service, which passes a closure to `repo.WithTx`. The repository
supplies the mechanism; the service says what is inside it.

The other ten have no multi-method transaction and therefore no plumbing, which
is the correct amount of machinery for what they do. Two of them (tax, region)
open a transaction INSIDE a single repository method to make that one method
atomic — a different thing from deciding a business boundary, and defensible.

The nuance that will matter one day: those two methods cannot be composed into a
larger transaction. Called from inside a service transaction they would open a
SECOND, independent one, and a rollback of the outer would not undo them. Today
no caller does that. The day one does, the fix is the ambient-transaction
plumbing the other six already have, not a bigger method.

### Global state: there is none

Zero `init()` functions. Zero package-level mutexes, maps or singletons. Every
package-level `var` in the repository is effectively immutable — an embedded
filesystem, a compiled Redis script, or a SQL string assembled from constants.
Dependencies are struct fields, resolved from the container BY NAME, and the
composition root is the only place that knows what is wired to what.

### Repository tests: real Postgres, and no mocking library at all

There is no gomock, no mockery, no testify/mock in go.mod. Twenty-eight test
files bring up a real PostgreSQL or Redis with testcontainers, and **fifteen of
sixteen modules construct their REAL repository over a real pool** in their
integration tests.

### The one real gap on this list

**A repository behaviour that no service path reaches is untested, and the
service-level fakes make that invisible.** The eleven hand-written
`fakes_test.go` files stand in for the repository so the service's DECISIONS can
be tested without a database — which is right — but it means a field dropped
between the model and the INSERT compiles, passes every unit test, and is caught
only if an integration test happens to assert that field.

That is not hypothetical. It happened twice in one change this session: the
order line's `tax_rate_bps` was left out of BOTH the INSERT parameters and the
row conversion, the build was clean, every service test passed against the fake
store, and zero is a legitimate tax rate — so nothing downstream looked wrong.
The first sign would have been an invoice printing 0% VAT on a taxed line. An
integration test writing two different non-default rates and reading them back
is what caught it, and a mutation of each half now fails that test.

The general shape: for every column a module writes, something has to read it
back FROM THE DATABASE. Fifteen modules have the harness to do it; what is
missing is the habit of adding the assertion when the column is added.

---

## An AI subsystem — measured against the brief, 2026-09-05

The brief: put the LLM in the platform as a SUBSYSTEM rather than a tool called
from outside. A core `ai` package with a `Provider` interface and a typed
`Task[In, Out]` layer above it; review moderation as the first use case, driven
off the outbox rather than the HTTP path; then summaries, description drafting,
query rewriting, embeddings, recommendations, a support assistant. With
versioned prompts, token budgets, an input-hash cache, PII masking, prompt-
injection separation, an eval set, and graceful degradation.

**Nothing about AI exists in the repository today.** What follows is what the
brief assumes, measured.

### Two assumptions that do not hold

1. **There is no review module.** The sixteen modules are product, pricing,
   inventory, region, customer, cart, payment, order, fulfillment, promotion,
   tax, auth, file, notification, b2b and invoice. A customer cannot leave a
   review, so `review.created` has nothing to fire from and moderation has
   nothing to moderate.

   The first use case is therefore two pieces of work, not one: the review
   module (schema, service, storefront write, admin read, moderation state) and
   the AI subsystem. They are separable and the review module is the one that
   has to come first — a moderation flow with no reviews is untestable in the
   way that matters.

2. **pgvector is not available and is not free to add.** The cluster today has
   `pg_trgm` and `unaccent` available; `vector` is not. More to the point,
   ADR 0015 fixes the contract at **zero required extensions** and says in
   writing that adding one is a change to what an operator must provide — a
   decision with a date on it, not the side effect of a feature.

   `pg_trgm` is already the standing candidate there, with a measured case
   (search `?q=` 58.9 ms to 2.2 ms) that was still not taken. Embeddings would
   make `vector` the second candidate, and the honest reading is that semantic
   search reopens ADR 0015 rather than sitting on top of it.

### What the brief needs and this repository already has

The shape the brief describes is, unusually, the shape this codebase already
uses. Seven pieces exist:

1. **The provider slot.** Payment, tax, notification and file all resolve a
   provider from a registry BY NAME, with the selection in configuration and an
   unknown name stopping the startup. `ai.Provider` is that pattern again, and
   "swap Anthropic for a local model in one line" is what the registry already
   delivers for payment.

2. **The outbox.** `review.created` reaching a worker without the HTTP path
   waiting is exactly what ADR 0023 built this session: the event is written
   inside the transaction that promises it, so a review that was saved cannot
   have an unfired event. The brief's "a review must never be lost" is that
   guarantee, already there.

3. **The job runner.** A worker draining a queue with an interval and a run
   budget is `internal/core/job` (ADR 0019), including the rule that `MaxRun`
   must be shorter than `Every` — refused at startup, which is how the outbox
   relay was caught before it shipped.

4. **Versioned files embedded in the binary.** Migrations, panel templates and
   the panel stylesheet all do it, the last with an ETag derived from the bytes.
   "Prompts are versioned files, not strings in code" is the same mechanism, and
   the stylesheet's stamp is the model for deriving a prompt version from its
   content rather than trusting somebody to bump a constant.

5. **Metrics.** An OTel meter provider is wired; token counts per task are a
   counter on it. (It exports over OTLP, not Prometheus — see the observability
   section.)

6. **Redaction.** `errorreport/policy.go` already splits attributes into what
   may travel and what may not, and REPORTS what it removed rather than dropping
   it silently. Masking a review before it leaves the process is that policy
   generalised, and "report what was removed" is the property worth keeping:
   a masker that silently ate the whole comment would look like a working
   masker.

7. **An eval harness.** `go test` with testcontainers is how every module is
   tested; a labelled set of reviews with an accuracy assertion is a test like
   any other. What it needs that does not exist is a way to run it WITHOUT
   calling a paid provider on every `make test` — a recorded-response fixture,
   with the live run behind a tag, the way `integration` and `smoke` already
   separate the expensive suites.

### The decisions the brief implies that this repository would have to make

- **"Core module" is two different things here.** `internal/core` holds
  capabilities modules opt into; `internal/modules` holds owners of data.
  an `ai` package belongs in the core tree (it owns no commerce data and every
  module may use it); the review data belongs in a module; and anything that reads a review AND
  calls the AI is a workflow (ADR 0001/0006). The brief's `Task[In, Out]` with
  `Build` and `Parse` is core; `review.moderate` is the review module's task
  definition, registered into core's registry the way a payment provider is.

- **The model's decision must not be the last word, and this codebase already
  argues that case elsewhere.** The returns flow refuses to refund automatically
  on receipt because that is the shop's decision; the same reasoning gives the
  moderation thresholds to configuration and the low-confidence cases to a human
  queue. Recording the human's decision is then not only an audit trail — it is
  the eval set growing itself.

- **An LLM cache is a different argument from a read cache.** The read-cache
  section above measures that caching database reads is not warranted: the
  database is twelve times faster than the code formatting its answers. A
  content-addressed cache over LLM calls is not that. It saves money and
  removes non-determinism, and neither of those was on the scale being measured
  there. The two must not be conflated.

- **Prompt injection has a structural answer, not a prompt-worded one.** The
  brief is right that the review text travels in its own block. The stronger
  half is what this repository would insist on anyway: the model returns a
  decision, the decision passes a threshold, and a human sees anything below it
  — so a successful injection changes a confidence score, not what gets
  published.

### What would come first

The review module, then an `ai` package under core with one task and one
provider, then
the moderation flow on the outbox that already exists. Embeddings, semantic
search and recommendations all sit behind the ADR 0015 reopening and should be
costed as that, not as features.

---

## Importable core, thin application — measured against the brief, 2026-09-05

> **Acted on 2026-09-05.** The twelve packages an extension author needs were
> promoted out of `internal/` to `core/` and the surface was fixed by ADR 0026.
> The measurement below is what led to that decision; the part still true is the
> composition root — `cmd/server` is a program, so an out-of-tree APPLICATION is
> still not possible. An out-of-tree PLUGIN now is.

The brief: ship the core as an IMPORTABLE Go module the way PocketBase does —
`go get`, `app := New()`, bind hooks, compile one binary — with a thin starter
repository per customer project. Stay away from the fork model. Open the
extension points that make forking unnecessary: provider interfaces, hooks over
the domain events, router access, merged migrations, a `metadata` jsonb on the
models. Compile-time plugin registration, Caddy-style. Keep the public API
small; take semver seriously.

### The finding that governs everything else

**gobit is a fork-model repository today, structurally, and not by choice —
100% of the framework is under `internal/`.** 531 files and 143,872 lines of
non-test code, all of it in a tree Go forbids any other module from importing.
There is not one importable package outside `internal/` and `cmd/`.

So the current answer to "how does a customer project use gobit" is: clone it
and edit `cmd/server`. That is the fork model, with every consequence the brief
names — a fix found in one project carried by hand to the others, and no
answer to "which one is current".

Moving to the PocketBase shape is therefore **not a matter of adding an `App`
struct.** The struct is the easy part. The work is deciding which packages stop
being internal, because that decision is the one this repository already knows
it cannot walk back: its own recurring sentence is that a field which enters a
contract can never be taken out again. The brief says the same thing — keep the
public API small — and the two agree completely.

### The good news: the mechanisms all exist, in the wrong place

Every extension point the brief asks for is already built. None of it needs
inventing; it needs relocating and naming.

| the brief asks for | what exists today | where it lives |
| --- | --- | --- |
| `PaymentProvider`, `TaxCalculator`, … | provider registries resolved BY NAME, selection in config, unknown name stops the startup | `core/provider`, per-module registries |
| hooks over domain events | an event bus with `Subscribe`, domain events, and an outbox so a promised event cannot be lost (ADR 0023) | `core/eventbus` |
| router access | chi; every module registers its own full paths, and the panel proves a fourth tree can mount its own | `core/http`, `Module.Routes` |
| merged migrations | the registry already merges per-owner migration sources and refuses two owners claiming one table | `core/module`, `cmd/server/migrate.go` |
| a project can add its own module | `Registry.Add(mod Module)` — the exact method the brief needs | `core/module` |
| `Product.Metadata` jsonb | present on product, variant, taxonomy, order, cart, customer, payment, invoice, fulfillment, tax and auth | eleven modules |
| compile-time plugin registration | a plugin host with an Install phase, selected by configuration | `core/plugin`, `plugins/` |

### The plugin tree is not the extension point it looks like

`plugins/` sits outside `internal/` and is importable — and **every plugin in it
imports `core/plugin` to satisfy the host contract.** An out-of-tree
plugin cannot do that. So the plugin system works for plugins that live in this
repository and for no others, which means the Caddy model the brief names
(`import _ "github.com/user/ecom-iyzico"`, register in `init()`) is not
currently possible — not because it was rejected, but because the contract it
would implement is unreachable.

That is the sharpest single consequence of the internal-only tree, and it is
worth stating plainly: **the repository has a plugin system that no third party
can write a plugin for.**

### What semver means today, and what it would mean after

There are tags through `v0.8.0` and the CHANGELOG is kept properly. But with no
importable package, those tags version an APPLICATION: nothing downstream can
break, because nothing downstream can compile against it. The moment a package
becomes public the tags start meaning what the brief wants them to mean, and
`/v2` becomes a real cost rather than a hypothetical one.

### The question this repository would have to answer first

Not "how do we build hooks" — they exist. It is: **which packages become the
public surface, and what is the smallest set that makes forking unnecessary?**

The brief's own warning is the right guide and matches this repository's
instincts: too-early generalisation is as harmful as forking. The honest first
step is small and testable — take ONE customer-shaped need, express it against
a public surface, and see what that surface had to expose. The candidates, in
the order the brief implies:

1. An `app` package with the lifecycle `cmd/server` already performs: build the
   registry, install plugins, bootstrap, serve. Today that logic is 1,200 lines
   of `cmd/server/setup.go` that no external program can call.
2. The module contract (`Module`, `Registry.Add`) so a project can add a module
   of its own.
3. The provider contracts, so `ecom-iyzico` can exist out of tree.
4. The event surface for hooks.
5. The domain models — the largest and the most permanent decision, because a
   published struct field is forever.

Everything below that stays internal. The measurement to keep: what fraction of
143,872 lines has to leave `internal/`. If the answer is more than a few
percent, the surface is too big.

---

## Commerce models — measured against the brief, 2026-09-05

The brief: subscriptions and recurring orders, multi-vendor marketplace, B2B
(group price lists, quotes, terms, minimum order), pre-order with a
back-in-stock waitlist, and digital products with licence delivery. With one
claim attached — a subscription dimension is cheap in the core and expensive
later.

Measured, the five are in three different states.

### Nothing at all: subscriptions and multi-vendor

**Subscriptions and recurring orders: zero.** No schedule, no renewal, no
recurring capture. The order state machine is four states — pending, completed,
archived, canceled — and every one of them describes a SINGLE sale that
happened once.

The brief's claim about cost is right, and it is worth being precise about why.
A subscription is not a fifth status; it is a second axis. The order keeps
`placed_at`, `completed_at` and `canceled_at` — moments, not a period — and a
recurring sale needs a next-run, a cadence, a pause and an end. That axis
touches the checkout saga (a renewal is a capture with no cart), payment (a
stored mandate rather than a session), fulfillment (a shipment per period) and
invoicing (a document per period, from a series that must stay gap-free).

The one piece that is genuinely ready is the scheduler: `internal/core/job`
already runs recurring work with an occurrence elected by a row and liveness by
an advisory lock (ADR 0019), which is exactly the shape a renewal run needs.

**Multi-vendor: zero.** No vendor, no commission, no payout. This is the
largest of the five by some distance, because it is not a feature but a change
to what a line means: every money path assumes one seller. The order totals, the
payment collection, the refund spread and the invoice all resolve to one party
today.

The nearest existing concept is the SALES CHANNEL, which already scopes what a
storefront may see and is enforced in SQL rather than in Go. A marketplace needs
the same discipline applied to money instead of visibility.

### Partly there, with the blocker already named: B2B

The b2b module exists and holds **companies and their employees**, plus a
spending rule the order module resolves by name at request time — so an
employee's order can be refused against a company limit, and a shop without the
module counts every customer as unlimited.

What the brief asks for beyond that:

- **Group price lists: the engine supports it, the cart deliberately does not
  send the group.** `pricing`'s rule matcher takes an arbitrary
  `map[string]string` context, so a rule keyed on `customer_group_id` would
  already work. The cart sends only `region_id`, and the reason is written down
  rather than forgotten: the rule context carries ONE value per attribute, a
  customer may belong to more than one group, and picking one silently would tie
  the price to map iteration order. The unfilled decision is pricing's — "the
  best price the customer is entitled to" — and the same gap is left open in the
  discount context for the same reason.

  So this item is not "build group pricing". It is "decide the selection rule
  for a customer in several groups", after which the plumbing is a context key.

- **Quotes, terms/deferred payment, minimum order: nothing.** Deferred payment
  is the interesting one, because it is the first case where an order is
  completed with money NOT captured — and the repository has just spent a whole
  round making the order say what was actually collected (ADR 0022) and
  reconciling it against the provider (ADR 0020). Terms fit that machinery
  rather than fighting it.

### A flag with no reader: pre-order

**`allow_backorder` exists on the variant, is editable through the admin API,
and is published by the query provider — and nothing reads it.** Every reference
outside the product module's own CRUD and DTO is absent; the inventory
reservation refuses on `CodeInsufficientStock` without consulting it.

That is this repository's named second error class (ADR 0009) in its purest
form: a capability whose consumer was never written, sitting in a published API
where a client can set it and reasonably expect it to mean something. A shop
that ticks "allow backorder" today gets the same refusal it got before.

Pre-order proper needs a little more — a promised date, and stock that may go
negative in a controlled way — but the first move is smaller than the feature:
either the flag gets its reader or it stops being published.

**The waitlist ("tell me when it is back") is nothing today and is the cheapest
item on this entire list.** The parts are all built: an inventory level change
is a fact the module already knows, the outbox guarantees the event survives the
commit (ADR 0023), and the notification module with its provider slot already
sends. What is missing is a table of who is waiting for what.

### Two flags and no delivery: digital products

`inventory_items.requires_shipping` and `products.is_giftcard` both exist, so
the model can already say "this does not ship". Nothing delivers anything: no
entitlement, no download token, no licence.

The storage half is built — the `file` module has an S3 provider — so what is
missing is the commerce half: what a paid order entitles the buyer to, a link
that expires and is bound to that entitlement, and a re-download policy. It is
also the one item on this list with a tax dimension in Turkey that the framework
would not decide for the shop.

### The order the measurement suggests

1. **The waitlist**, because every part exists and it is a table plus a
   subscriber.
2. **The backorder flag's reader**, because a published flag that does nothing
   is worse than an absent one.
3. **The group-price selection rule**, because it is a decision rather than a
   build, and B2B is the segment the brief calls a real gap in Turkey.
4. **Subscriptions**, because the brief is right that the second axis is cheaper
   before the order model has more consumers, and the scheduler is ready.
5. **Multi-vendor**, last, because it changes what every money path means and
   should not be attempted while any of the above is still moving.

---

---

## AI-powered commerce features — measured against the brief, 2026-09-05

The brief: natural-language search ("winter, under 500 TL, dark colour"),
automatic review summaries and Q&A, attribute extraction from product photos, a
chat assistant with tool-use that can read an order and start a return, and
price/stock forecast SUGGESTIONS an operator applies rather than the system.

Five areas measured in parallel against the tree. One is genuinely close; the
rest are blocked by something more basic than the AI.

### Natural-language search: the filters it would translate INTO do not exist

The layer the brief describes turns a sentence into filters. **The entire
structured filter surface of the storefront is one collection id plus free
text** — `collection_id`, `q`, `limit`, `offset`, `after`, `with_count`, on REST
and GraphQL alike, with a test pinning the two to each other.

So there is no price filter, no category filter, no tag filter, no option-value
filter, no in-stock filter and no sort. "Under 500 TL" and "dark colour" have
nowhere to land, and "winter" has no first-class home either: season is not a
column, and of its natural carriers — collection, category, tag — only
collection is filterable.

Colour and size ARE modelled (`product_option`, `product_option_value`, and the
variant join) and neither the listing nor the search index reads them. There is
also **no storefront endpoint that enumerates collections, categories or tags**,
so an NL layer has no public vocabulary to resolve a word to an id.

Two findings worth carrying:

- **There are two independent search paths, not one.** The product module's own
  `?q=` is `title ILIKE '%…%'` — a leading wildcard, no index, a full scan that
  ADR 0015 measured at 58.9 ms on the 52k fixture. The searchpg plugin is a
  separate endpoint with a real weighted `tsvector` and a GIN index, its own
  module and its own migration ledger. They share no contract, and searchpg
  accepts no filters at all.
- **Search is not a provider slot.** Payment, fulfillment, notification and file
  have registries; search does not. Swapping the engine means replacing a
  package rather than registering an implementation.

The honest order: the filters first, then a vocabulary endpoint, then the layer
that maps a sentence onto them. An NL layer built before the filters would be a
translator with no target language.

### Review summaries and Q&A: still blocked on the review module

The absence of review data is already recorded in the AI-subsystem section. The
measurement adds two details that decide where a summary could live once reviews
exist.

- **`product.metadata` is a whole-value REPLACE, not a merge.** The update is
  `metadata = COALESCE(@metadata, metadata)` and there is no jsonb `||` anywhere
  in the repository. A summariser writing there would clobber whatever else a
  shop had put in it, and two writers would clobber each other.
- **`product.metadata` is publicly readable on the storefront**, in REST and in
  GraphQL. A summary placed there is published by construction, including its
  intermediate states.

The precedent to copy instead is `searchpg`: a per-product derived row in its
OWN table, with its own migration ledger, no cross-module foreign key, and a
rebuild driven by events. Note the constraint that comes with it — **only four
domain events exist repo-wide** (`product.created`, `product.updated`,
`product.deleted`, `order.placed`), so a summary invalidated by a new review
needs a fifth, which is the review module's to publish.

### Attribute extraction from photos: the image cannot be read back

Blocked below the AI, and in a way that is easy to miss.

- **`FileProvider` has only `Upload` and `Delete`.** On any real object-store
  deployment the application cannot read an uploaded image back at all. A vision
  pipeline has no bytes to look at.
- **The file module publishes no events**, so nothing can react to a photo
  arriving.
- **A product image and its upload record are not linked.** `product_image.url`
  is free text, there is no `upload_id`, and a cross-module foreign key is
  forbidden (Principle 2.2). Given an image row there is no way to reach its
  storage key.
- **Images are write-once at product create** — no per-image endpoint and no
  `Images` field on the update input, so a pipeline could not write back what it
  found.
- **There is nowhere to put a suggestion.** `product_category_map` is a bare
  `(product_id, category_id)` with no confidence, no source and no pending
  state, and the setter replaces the whole set atomically.

Not one of these is about models or prompts. Four are ordinary plumbing
decisions that would each be worth making on their own merits.

### The chat assistant: the tools already exist, the caller does not

**This is the area that is genuinely close, and for a reason nobody planned.**

There are **fifteen `*.interop` container surfaces carrying sixty-one methods**,
and by written rule (ADR 0001/0006) every one takes and returns primitives,
slices of primitives, or `json.RawMessage`, with composite schemas documented
next to the method. That is a tool catalogue, built for module isolation and
arriving fit for tool-use by accident. The container can even enumerate the
names at runtime.

What is missing is not the tools but three things around them:

1. **There is no customer to be.** ADR 0008 decided the framework will not build
   customer identity: the storefront key identifies the STORE, not the person.
   A customer-facing assistant has nobody to act as, and "show me my order"
   cannot be authorised. That is a decision rather than a gap — and it means the
   assistant's first honest form is an OPERATOR's assistant inside the panel,
   where identity already exists.
2. **A return cannot be started through any surface.** `order.interop` offers
   `ReturnDetailJSON`, `ReceiveReturn`, `ClaimDetailJSON`, `CompleteClaim`, and
   `workflows.returns.interop` offers `RefundReturn`, `SettleClaim` — every one
   acts on a return that ALREADY EXISTS. The only creation entry point is
   domain-typed and unreachable from outside the module.
3. **Tool schemas cannot be generated.** The container returns names, not method
   metadata, and the OpenAPI document is missing a body schema for exactly the
   endpoints this feature needs. Hand-writing a schema per method is fine for
   ten tools and not for sixty-one.

### Forecast suggestions: there is no history to forecast from

- **Stock is a current-value column, not a ledger.** `inventory_levels`
  overwrites `stocked_quantity` and `reserved_quantity` in place, so the
  database cannot answer "how much stock did this item have last Tuesday" and a
  depletion rate is not derivable at all.
- **Demand history exists and is unreachable.** `orders` and `order_line_items`
  carry quantity, price and `created_at` — a real time series — but there is no
  aggregate query for it, no date-range filter on the order query provider, no
  `order_line_item` entity in the read layer, and no supporting index.
- **Price history exists only by accident.** `ReplacePrices` soft-deletes and
  reinserts, so old rows survive — with regenerated ids, no reader and no index.
  Promoting that into a real record is a decision nobody has taken.
- **The audit log cannot reconstruct a change, by design.** It records the
  REQUEST, not the diff, and says so in its own header.

The SHAPE the brief asks for — the system proposes, a human applies — **already
exists here with an ADR behind it.** `internal/jobs/sagawatch` is a job that
measures, reports and deliberately never acts, and ADR 0017 is the written
argument for why. A forecast job is that pattern again. What it lacks is
anywhere to store a suggestion: pricing's and inventory's tables have no
metadata column between them.

### What the five have in common

Four are blocked by something that is not AI — filters that do not exist,
reviews that do not exist, images that cannot be read back, history that was
never kept. The fifth is blocked by a decision (ADR 0008) rather than by
machinery, and its machinery is unusually ready.

The cheapest real move on this list is therefore not a model call. It is the
storefront filter surface: the NL layer needs it, the panel would use it, and
nothing else on the roadmap has to wait for it.

---

## Storefront speed and checkout — measured against the brief, 2026-09-05

The brief: product and category endpoints carrying ETag/Cache-Control so a CDN
can serve them, with cart and price personalisation moved to separate endpoints;
real-time stock and price over SSE or WebSocket; single-page checkout, guest
checkout, saved cards, passkey login; and visual/semantic search on pgvector in
the same database.

### Edge cache: the repository already argued against it, in writing

**No JSON response anywhere carries `Cache-Control`, `ETag`, `Vary`,
`Last-Modified` or `Age`.** `corehttp.WriteJSON` writes exactly two things — a
content type and a status — and takes no ETag parameter, so no caller could
supply one. Only two responses in the whole tree are cacheable: the admin
panel's stylesheet (`WriteAsset`, `immutable`) and `GET /files/{key}`
(`public, max-age=3600`, identity-free, and its godoc is the only place that
reasons about shared caches at all).

**And the premise does not hold as stated: the product body already varies by
caller.** `RequireStore` reads `x-publishable-api-key`, resolves it to a
principal carrying sales-channel ids, and the storefront listing and detail
filter on those channels in SQL. Two keys on the SAME URL get different bodies —
covered by four integration tests — and no `Vary` is emitted for that header.
`Vary` appears in exactly one place in the repository, for `Origin`, and only
when CORS is configured, which by default it is not.

The codebase has already written this argument down. From the GraphQL handler:
*"The GET transport was DELIBERATELY not added. GET's only real gain is
intermediate caches and that gain does NOT EXIST here: the response varies with
the request's publishable key, that is, with the sales channel. A shared cache
would either have to vary by the key header (that is, cache almost nothing) or
serve one storefront's catalog to another — which is exactly why the channel
filter exists."*

So the brief's instinct — separate the personalised part — is right, and the
personalised part is not the cart. **It is the sales channel, and it sits on the
catalog endpoint itself, in a header.** Making the catalog edge-cacheable means
deciding one of: the channel moves into the PATH (a cache key a CDN can see), or
single-channel installations opt in explicitly, or the cache is per-key and the
hit rate is accepted for what it is.

Two further facts a cache design has to survive:

- **Price does not vary per caller on the product endpoint** — every rule-bearing
  price is dropped by the provider, with the reason written where it happens:
  a rule needs a context (region, customer group) the provider does not carry.
  So the catalog body is genuinely impersonal apart from the channel.
- **The body is time-varying with no write behind it.** A price-list window
  opening or closing changes the product response with no product write, because
  the provider evaluates against the clock. That defeats an ETag computed from
  the record and it defeats a long `max-age` — it is the fact that decides
  whether the answer is a short TTL or an explicit purge.

There are 42 routes under `/store/v1`, and the GraphQL endpoint is POST-only.

### Real-time stock: nothing runs, and more of it is present than expected

There is no SSE, WebSocket, long-poll or streaming endpoint. `text/event-stream`
appears once in the tree, in a test.

But the groundwork is unusually far along, and by accident:

- **The shared response-writer wrapper is already streaming-correct and
  tested.** It forwards `Unwrap`, `Hijack` and `Flush`, and its godoc names
  WebSocket explicitly. A streaming handler would not have to fight the
  middleware.
- **`github.com/coder/websocket` is already in `go.mod`** (indirect, pulled by
  gqlgen), and gqlgen's transport package on disk already ships `sse.go` and
  `websocket.go`. The transports are in the module graph and simply never added
  — the storefront schema has a `Query` type and no `Subscription`.
- **The storefront product response already carries live stock**:
  `inventory_item.available_quantity` reaches the client through the link
  expansion, because the expansion requests no field list and therefore gets the
  provider's full set.

Four blockers, and each is a decision rather than a library choice:

1. **Inventory publishes no events at all.** `SetInventoryLevel`,
   `AdjustInventory`, `Reserve`, `ReleaseReservation` and `ConfirmReservation`
   are silent. There is nothing to push.
2. **The event bus cannot fan out to N web processes as configured.** Redis
   Streams consumer groups deliver each message to exactly ONE consumer in the
   group, and every subscriber joins the same group. Pushing the same change to
   every connected browser needs a second delivery shape.
3. **`WRITE_TIMEOUT` is 30 seconds, mandatory, and unexempted.** A long-lived
   connection dies at thirty seconds. The pprof listener hit exactly this and
   the answer was a second listener with no write budget — the precedent exists
   and its cost is known.
4. **A browser `EventSource` cannot send `x-publishable-api-key`.** The store
   guard requires that header on every request. Real-time on the storefront
   needs an auth shape the browser can actually produce.

### Checkout: better than the brief assumes, with one real hole

- **Guest checkout works and is the DEFAULT path**, not a mode. `Cart.Guest()`
  is a first-class state, `CompleteCartInput` has no customer field at all, and
  an ADR 0008 conformance test asserts a guest order is never asked for the
  spending rule. **Email is not required anywhere.**
- **A single "complete checkout" call already exists.**
  `POST /store/v1/carts/{id}/complete` runs the whole five-step saga server-side
  — reserve, create order, authorize, capture, clear cart. The storefront never
  touches a payment collection or a session.
- **Minimum cart to paid order is four HTTP calls**, proven end to end over the
  production guard stack. The third is a `GET` and it is forced by a deliberate
  rule: `expected_total` is mandatory on complete, and nothing in the
  add-line-item response carries the total.
- A full-feature checkout (email, addresses, chosen shipping and provider) is
  about ten calls, and **email cannot be passed on the complete call** — it needs
  a separate write to the cart.

**The real hole: the cart's addresses never reach the order.** `cart_addresses`
exists; the order module has no address table, column or model field. This is
also why the invoicing flow has to take the buyer's address from its caller —
the order does not have one.

### Saved cards and passkeys: neither exists, and saved cards share a
prerequisite with subscriptions

- **`PaymentProvider` has five methods — CreateSession, Authorize, Capture,
  Refund, Cancel — all addressed by a per-transaction session id.** There is no
  customer parameter, no tokenize/attach/detach, no mandate, no payment-method
  type; the payment schema has no customer column and no instrument table. A
  second payment cannot reuse a first one's instrument. Both shipped payment
  plugins confirm it: PayTR keys on a per-session iframe token, Stripe is a
  skeleton.

  **This is the same prerequisite subscriptions need** — a stored instrument
  rather than a session — so the two features are one contract change apart, not
  two.

- **Passkey/WebAuthn: zero occurrences**, no library. The auth module supports
  exactly three methods and all are admin-facing; the customer-facing surface
  authenticates the STORE, not a person (ADR 0008).

- **An address book exists and is a leaf.** `customer_address` is a full table
  with DB-enforced single default shipping and billing. But **its storefront
  endpoints are unauthenticated and identify the customer from the path** — the
  module's own package doc says anyone reaching them can read and change a name,
  email and address — and **nothing outside the customer module reads it.** No
  path pre-fills a cart address from a saved one, and the order has no address
  at all.

  That is worth reading next to the KVKK note: it is personal data on an
  unauthenticated endpoint, and it is a known consequence of ADR 0008 rather
  than an oversight — but it is the sharpest form the consequence takes.

### Visual and semantic search

Already measured in the AI-features section and unchanged: `pgvector` is not
available on the cluster, and ADR 0015 fixes the contract at zero required
extensions, so this item reopens that ADR rather than sitting on top of it.

---

## Turkey-specific — measured against the brief, 2026-09-05

The brief: e-Fatura/e-Arsiv in the core; the domestic carriers (Yurtici, Aras,
MNG, PTT) behind one interface with tracking webhooks; iyzico/PayTR/Param plus
installment-table calculation; and KVKK consent records with ready-made
download/delete endpoints.

**e-Fatura is the one already answered.** The document, its lines, its parties
and its gap-free numbering landed this session (ADR 0024); what remains is the
transmission, which needs the merchant's own certificate and an integrator
contract and is therefore a plugin's job, not the framework's. The provider slot
is the shape it plugs into.

### Carriers: the contract is four methods and tracking is not one of them

`FulfillmentProvider` has exactly `ID`, `Quote`, `Create`, `Cancel`. There is no
label method and no label type; there is **no `Track()` and no status poll** —
`TrackingNumber` and `TrackingURL` exist only as OUTPUT FIELDS of `Create`.

Three things a Turkish carrier integration would hit immediately:

- **`QuoteInput` cannot express what a Turkish carrier prices on.** It carries
  `OptionID`, `CurrencyCode`, `CountryCode`, `TotalWeight` in grams, `ItemCount`
  and an untyped `Data`. There is no destination postal code, no il/ilce (province/district), no
  origin address and no dimensions — and domestic carriers price on **desi**
  (volumetric) and on district. All of it would have to travel through the
  untyped bag.
- **The status vocabulary is four values** — pending, shipped, delivered,
  canceled — pinned by a database CHECK. There is no "in transit", no "at
  branch", no "delivery attempt failed", and no **"returned to sender" (iade)**,
  which is a real carrier state a shop must act on.
- **The state machine is strict and one-way**, and a carrier's event stream is
  not. `DeliverAction` from `pending` is a CONFLICT — delivered may not skip
  shipped — so a webhook that arrives out of order or twice hits an error rather
  than converging.

One provider ships: the manual one, which makes no network call and returns
whatever tracking number the caller passed it. There is no carrier plugin.

`MarkShipped` and `MarkDelivered` exist and are admin-scoped, and their godoc
already names the intended source: *"THE PROVIDER IS NOT CALLED: this method
records the fact the carrier REPORTED (a webhook or an administrator action)."*
**The word "webhook" appears exactly once in the entire non-test codebase — in
that comment.**

### There is no inbound webhook machinery, and the one working callback is unguarded

No webhook registry, no signature middleware, no replay/dedup store, no delivery
receipts. The PayTR callback is the only working example and **none of it is
reusable**: its path is a plugin constant, its handler is a method on the
plugin's module, its HMAC lives in the plugin package, and its response protocol
is a bare text token because PayTR reads the body rather than the status.

More importantly, that path sits OUTSIDE the guarded prefixes — deliberately,
because PayTR carries neither a publishable key nor a bearer token. The
consequence, measured: `/paytr/callback` gets **no auth, no rate limit, no
idempotency middleware, no audit and no CORS**. Its only protection is the HMAC
check inside the handler, and there is no global request-body size limit
anywhere in the core.

Four carriers plus e-invoice plus payment callbacks means five more such paths.
**A guarded inbound-callback class — signature verification, replay window, body
limit, rate limit, audit — is the thing to build once**, and it is the same
machinery every item in this brief needs.

### Installments: there is nowhere for a second amount to exist

No installment, BIN, card-bank or per-option-total concept exists. The only
occurrences of the word are PayTR's own wire fields.

The contract blocks it in two places at once:

- **There is no return path for an installment quote.** `Session`, `AuthResult`
  and `SessionInspection` each carry a single scalar amount. There is no list
  type, so "these are the 3/6/9/12-month options and their totals" cannot come
  back from a provider.
- **The customer-paid amount is forced equal to the order total by four
  independent guards**, including database CHECK constraints. A vade farki — an
  installment surcharge the customer pays and the merchant does not receive — has
  nowhere to live in the money model.

That second point is the real one, and it is shared with the marketplace item:
both need the model to admit that **what the customer pays and what the merchant
receives can differ.** Today it cannot, and that is enforced in the schema.

No iyzico and no Param provider exist; Stripe is a declared skeleton.

### KVKK: zero consent records, and no ADR covers privacy at all

- **No consent record of any kind.** No table, no column, no timestamp of
  agreement — for marketing, for terms, for anything.
- **None of the twenty-five ADRs covers privacy.** ADR 0008 governs the customer
  identity trust boundary, which is authentication, not data protection.
- **No export endpoint, and a customer cannot request their own deletion** — the
  store surface has no delete handler; the only delete is admin-only.
- **`DeleteCustomer` is a pure soft delete**, and deliberately partial: group
  memberships are left in place with a comment saying they will cascade "when the
  record is one day really deleted" — a real delete that nothing performs.
- **Deleting a customer notifies nobody.** The customer module publishes no
  events at all, so no other module can learn it must clean its own copy — and
  Principle 2.2 forbids the foreign keys that would cascade.

Personal data sits in ten tables across eight modules and a plugin.

Two decisions come before any schema:

1. **The invoice retention conflict** (already recorded): the document carries
   the buyer's name, tax number and address, and its whole design says it is
   immutable and its numbering may never have a hole.
2. **What gobit IS, legally.** A data controller, or a library whose embedder is
   the controller? ADR 0025 makes gobit a library, which points at the second —
   and that answer changes what the framework owes from "implement consent" to
   "give the embedder the hooks and the erasure contract".

---

## Platform features — measured against the brief, 2026-09-05

The brief: an outbound event stream (webhook + NATS) so a customer can wire
their own ERP or Slack; feature flags and A/B testing in the core; multi-store,
multi-currency and multi-language in one installation; and an audit log with a
"what happened on this order" timeline.

### Outbound events: four events, no retry, and no multi-process fan-out

**Exactly four domain events exist repo-wide** — `order.placed`,
`product.created`, `product.updated`, `product.deleted` — published from three
call sites. Payloads are deliberately narrow and every value is a string,
including money and counts.

For an outbound stream, three properties decide the design and all three are
already written down:

- **There is no retry and no dead-letter queue, by explicit decision.** A
  handler error is logged and the event counts as processed; the ADR-grade
  reasoning is the poison pill — redelivery without a DLQ lets one broken event
  lock the consumer. An outbound sender must therefore build its own retry.
- **Redis cannot fan out to N processes as configured.** Every subscriber joins
  one consumer group, and a group delivers each message to exactly ONE consumer.
  Different group names fan out; nothing in the repository uses different names.
- **Under Redis the handler context carries nothing** — no request id, no logger,
  no identity, no tracing span. Only the event id and its data cross the process
  boundary, so an outbound sender must read everything from the payload.

No NATS, Kafka or RabbitMQ appears anywhere, including `go.mod`.

And one structural blocker for shipping this as a plugin: **the plugin host
cannot register a job.** Its surface is Container, Logger, Setting, AddModule,
AddRoutes and four provider registrations — an outbound-delivery plugin could
mount a route but could not schedule its own retry pass.

The outbox is the right foundation and it is already there (ADR 0023): the event
is written inside the transaction that promises it, and a relay publishes it.
An external subscriber would hang off that relay.

### Feature flags: there is no substrate, not even a settings table

**Zero flags, and no place to put one.** Eighty-two tables and not one is a
settings or configuration table. Behaviour varies only by environment variables
read once at process start, and **nothing reloads** — no signal handler, no file
watcher, no polling. The plugin set is fixed at startup too.

What CAN change without a restart is DATA, and there are exactly three live
axes: `product.status`, the price-list window evaluated against the clock, and
`sales_channel.is_disabled`, which is applied inside the key-to-channel
resolution query itself. Those are the existing proof that a per-request
database read is affordable on this path.

Two things a flag design has to settle:

- **The evaluation point.** A database-backed flag means a per-request read; the
  sales-channel resolution is the precedent for what that costs.
- **A/B testing needs a stable per-visitor key, and there is no visitor.** The
  storefront identity is a publishable key representing a CHANNEL, not a person
  (ADR 0008). Assignment would have to come from the embedding application —
  which is consistent with ADR 0025 making gobit a library.

The measurement also names `allow_backorder` again, as the live proof of what a
flag without a reader becomes.

### Multi-store, multi-currency, multi-language: two of three

- **Multi-store works today** through sales channels: a publishable key binds to
  channels, and the catalog is filtered on them in SQL — with the visibility
  rule that an unassigned product is visible everywhere and an assigned one only
  in its channels.
- **Multi-currency works**: currencies are a table with their decimal digits,
  regions carry a currency, and prices are per currency.
- **Multi-language does not exist.** There is no locale column, no translation
  table, no `Accept-Language` handling anywhere. A product has one title, one
  subtitle and one description.

That third one is the honest gap, and it is bigger than it looks: translated
content is not a column on the product, it is a decision about where translated
values live and how the storefront selects one — and it touches the search
index, the panel and the invoice.

### Order timeline: the facts exist, scattered, and two of them are unreachable

What an order itself records: `placed_at`, `completed_at`, `canceled_at` with a
reason, plus database CHECKs tying each stamp to its status. Returns carry
`received_at` and `canceled_at`; claims carry both transitions.

Four measured holes:

- **Archiving leaves no timestamp.** The status flips to `archived`,
  `completed_at` is deliberately untouched, and there is no `archived_at`. When
  an order was archived is not recorded.
- **`order_exchanges.completed_at` and `canceled_at` exist and are never
  written** — there is no Complete or Cancel query for an exchange at all.
- **The money timeline is unreachable through the read layer.**
  `payments.captured_at` and the refund rows are the two facts a support team
  asks for first — "when was it paid", "when did the refund go out" — and there
  is no query provider that exposes them.
- **There is no order↔fulfillment link, and nothing creates a fulfillment for an
  order.** The link definition was assigned to the fulfillment module, which
  never declared it. So "where is the parcel" cannot be answered from an order
  at all.

The audit log built this session does not close this: it records the REQUEST —
who called what and what came back — and says in its own header that it does not
record the change. It answers "who touched this" and not "what happened".

So the timeline is a read-side composition over facts that mostly exist, plus
two that do not: the order↔fulfillment binding, and a money-event surface.

---

## The embedded admin panel — measured against the brief, 2026-09-05

The brief: the panel must ship, but as a CLIENT of the admin API rather than as
part of the core — `/admin/api/*` with RBAC first, then an SPA over it, embedded
in the binary with `embed.FS`. Extension points so a project can add its own
page, a JSON-schema-driven form for metadata fields, and slots on the critical
screens.

**The panel exists and its structure is the opposite of the brief's, by a
written decision.**

Today the panel resolves `core.query` and the three narrow admin surfaces
(`product.admin`, `pricing.admin`, `inventory.admin`) **from the container, in
process. It makes no HTTP call to the admin API at all.** It is a fourth tree
(ADR 0011) of server-rendered Go templates.

ADR 0011's reasons are on the record and they are not incidental:

- The panel's cookie stays inside the panel's tree and is deliberately NOT
  accepted by the admin API, **so the admin API's CSRF immunity survives** — it
  takes identity from a header only, and a browser cannot be made to send one
  cross-site.
- "The panel ships in one binary: **no separate toolchain, no separate
  deployment and no CORS surface.**"

So the brief's structure is not a refinement of what exists; **adopting it means
superseding ADR 0011, and the crux is authentication.** An SPA calling
`/admin/v1` needs either the cookie accepted there — which is exactly what the
ADR refused, because it destroys the CSRF property — or a bearer token held in
JavaScript, which is the XSS exposure the cookie avoids. Both are solvable
(same-origin with a short-lived token, or cookie plus a CSRF token) but the
choice is an ADR, not a detail.

The brief's strongest point stands regardless: **an SPA that is an API client is
a test of whether the admin API is complete.** Today three of the panel's writes
go through in-process surfaces rather than HTTP, so that test would fail before
it began — and finding out exactly where is worth doing whether or not the SPA
follows.

What is already true of the brief's other asks:

- **Embedding is already the posture.** Templates and the stylesheet are in the
  binary via `embed.FS`, with the stylesheet's ETag derived from its own bytes.
  An SPA would use the same mechanism.
- **RBAC exists**: the admin API is scope-guarded per endpoint.
- **The metadata slot exists**: a `metadata` jsonb sits on eleven modules'
  models, including product, variant and taxonomy. What is missing is the form
  generator, not the field.
- **The extension points do not exist.** There is no `AddPage`, no widget slot,
  and the plugin host cannot add a panel page.
- **Coverage is four of sixteen modules** — catalog, orders, customers,
  inventory — which is what an operator looks at daily. The remaining twelve are
  configuration.

One precedent worth carrying into the extension design: ADR 0011 deferred
WRITING entirely, because "no module has a narrow surface open to its admin
side — the price module has none at all — so writing means opening new contracts
to three modules, and every contract is a commitment without a compiler." That
gap was later closed by opening exactly three narrow `*.admin` surfaces. **That
is the pattern a project-added page should follow**: a named narrow surface, not
access to the module.

## Capability inventory — measured 2026-09-04

Ten axes, 139 capabilities: 83 gaps and 22 things this repository refuses in
writing. The findings below were produced by a measurement pass and then
RE-VERIFIED by hand against the code, because a gap list nobody checked is a
list that gets acted on wrongly.

### The pattern underneath most of them

Almost every blocking gap has the same origin, and it is not neglect. Each one
was deliberately deferred to a phase that no longer exists. `aftersales.go`
is the clearest statement of it:

> The status transitions, the line-based return, taking the stock back and
> refunding the payment are the job of the NEXT PHASES; that is why there is no
> transition method here.

The roadmap that contained those phases closed. So the skeletons stayed
skeletons, and each one reads — correctly, at the time it was written — as
"coming soon". This is the same defect class recorded elsewhere in this
repository as a promise written against a phase name: when the phase ships or
the roadmap ends, the promise expires silently and nothing tells the reader.

Any work planned from this list should start by deleting the phase language, so
the next reader sees a decision instead of a wait.

### Blocking — verified by hand

1. ~~**The shopper sets their own shipping price.**~~ **CLOSED 2026-09-05,
   ADR 0021.** The storefront now names WHICH option; the price is quoted by the
   fulfillment module from the cart's own facts, `AddShippingMethod` is off the
   module's HTTP surface, and `amount` is gone from the request body. Left in
   place here because the shape of the defect is the useful record: the engine
   that produced the right number was already built with zero consumers, and the
   rule forbidding this was already written about the LINE price.

   The original finding:
   `POST /store/v1/carts/{id}/shipping-methods` (`cart/api/routes.go`) is a
   STOREFRONT route, and `AddShippingMethod` (`cart/service/shipping.go`)
   stores `in.Amount` verbatim — the only check is `checkAmount` against
   `models.MaxAmount`. Nothing re-quotes it against the shipping option. Post a
   real `shipping_option_id` with `amount: 0` and the order is created and
   captured at that price. The quote engine that would produce the right number
   is fully built and never consulted.

   Not in the README's known-limits list and not refused by any ADR — this one
   is simply unnoticed, which makes it the most urgent item in this file.

2. ~~**No order knows what was paid on it.**~~ **HALF CLOSED 2026-09-05,
   ADR 0022.** The checkout saga now reads the payment collection after the
   capture and records its amounts on the order, so `paid_total` is right from
   the moment of checkout.

   ~~**The refund half is still open.**~~ **CLOSED 2026-09-05.** The return
   flow's refund writes both sides: it refunds against the collection and
   records the collection's running refunded total on the order. The B2B
   consequence below is fixed with it.

   What remains unwritten is a refund made OUTSIDE a return — through the
   payment module's own admin API — which still has no order-side caller. That
   needs either payment events (the module publishes none) or the same
   two-sided discipline on that endpoint.

   One prerequisite for that flow landed on 2026-09-05: there was no path from
   an order to its payment at all, because the collection's `Reference` carries
   the CART id and the `order_payment` link both godocs named was never
   declared. The payment module now declares it, the checkout saga writes it,
   and `GET /admin/v1/orders/{id}/payment` reads it.

   The original finding: `SetOrderSummaryTotals`
   (`order/service/summary.go`) has a service method, a repository method and
   a generated query — and NO production caller. The checkout saga never calls
   it (`grep` over `internal/workflows/` returns nothing). Every real order
   therefore reports `paid_total: 0` and `outstanding: <full total>` on both the
   admin and the storefront read.

   The API package already documents the intended owner
   (`order/api/api.go`: "both of them are the workflow['s job]") — so the
   wiring is not undecided, it is unfinished. It also has a second consequence:
   the B2B spending window subtracts `order_summaries.refunded_total`, so a
   refunded B2B order never returns the employee's budget.

3. ~~**Returns, exchanges and claims cannot move.**~~ **PARTLY CLOSED
   2026-09-05.** All three record types now have transition tables
   (`order/models/aftersales_status.go`) and returns carry the lines coming back
   (`order_return_items`), with the across-returns quantity rule enforced under
   the order's lock.

   **Restocking landed on 2026-09-05** (`internal/workflows/returns`): receiving
   a return records where the goods arrived and puts their stock back, through
   a flow the admin endpoint is bound to.

   **Refunding landed the same day**, as a separate action for the reason above
   (goods can arrive damaged, so paying back is an operator decision). It also
   closes the refund half ADR 0022 left open: the amount is recorded on the
   order, so a refunded B2B order now returns the employee's budget.

   **Claims settle with money** as of the same day, and a claim to be settled
   with a REPLACEMENT is refused rather than stamped — shipping goods against
   an existing order is a capability the framework does not have.

   **A customer can open a return request** as of the same day
   (`POST /store/v1/orders/{id}/returns`), under the same trust boundary its
   sibling read declares: verifying the order belongs to the requester is the
   embedding application's job (ADR 0008). The cost is bounded — a request moves
   nothing until an operator receives it — and the customer names LINES, never
   an amount.

   What is left on this axis is a DECISION rather than an oversight: exchange
   completion is not built, because it needs goods shipped out AND a positive
   difference collected against an existing order, and the one-to-one
   `order_payment` cardinality forbids the second today. A cancellation request
   is likewise absent: cancelling reaches money and stock, and a paid order
   cannot be canceled at all.

   The original finding: Zero `UPDATE` statements
   across `order_returns.sql`, `order_exchanges.sql` and `order_claims.sql` —
   verified by count. A record is born `requested` and stays there forever;
   `received_at`, `completed_at` and `canceled_at` can only ever be NULL. There
   is no line-level record, so a partial return cannot be expressed, nothing
   restocks and nothing refunds.

   Sharpened by a refusal: order editing is refused on purpose
   (`order/models/models.go`) IN FAVOUR of this skeleton. So today there
   is no correction path of any kind.

4. ~~**Admin cancellation is a stamp.**~~ **CLOSED 2026-09-05** — but not the
   way this finding proposed. Measurement inverted it: making the cancel release
   stock would have been worse, because it would restock a PAID order without
   refunding it. What the endpoint had to do was REFUSE.

   The finding's own framing missed why the case was reachable at all: the saga
   never completes the order it places, so a paid, stock-deducted order sits at
   `pending`, and the existing "a completed order cannot be canceled" rule read
   the STATUS as a proxy for "money was collected". The guard is now anchored to
   the money.

   The original finding: `CancelOrder` (`order/service/order.go`)
   is documented as a SAGA COMPENSATION and does exactly that job: it writes
   `canceled_at` under a row lock. Reached from the admin route it releases no
   reservation, voids no payment and publishes no event — the order module's
   only `Publish` is `order.placed`. Stock release lives on the saga's
   compensation path, which this endpoint does not go through.

5. ~~**Tax is computed from inputs that were thrown away.**~~ **PARTLY CLOSED
   2026-09-05.** The cart now resolves each line's product from the catalog in
   one batch read and sends it, so tax rules match per product instead of every
   line falling through to the region's default rate.

   **The rate is no longer discarded (2026-09-05).** Both tax paths — the
   region's flat rate and the tax module's per-product rules — now write the
   rate they applied onto the line, and it travels through the checkout plan and
   the JSON interop into `order_line_items.tax_rate_bps`. It is stored rather
   than derived because it cannot be derived: the tax is rounded DOWN per line,
   so 1899 kurus is what 20% and 19.99% both produce, and an invoice has to
   print the rate the customer was CHARGED under. In Turkey the KDV rate is a
   required field on an e-fatura rather than a computed one, which is why this
   column exists before any invoicing does.

   The rate crossed four hands — input, model, INSERT parameters, row
   conversion — and leaving it out of any one of them still compiled and still
   passed every unit test that uses a fake store. Two of the four were in fact
   written without it. Zero is also a legitimate rate, so nothing downstream
   would have looked wrong; the first sign would have been an invoice printing
   0% on a taxed line. An integration test now writes two DIFFERENT non-default
   rates on one order and reads them back, and the end-to-end test asserts that
   the stored rate is the one that PRODUCES the stored tax.

   Still open on this axis: `ProvinceCode` is never sent (the cart carries no
   province), a region holding more than one country still resolves to none, and
   shipping tax is hard-wired off. `product_type_id` stays
   empty legitimately — gobit has no product type.

   The original finding: The tax module is
   complete; the cart seam starves it. `workflows/cart/tax.go` builds
   each item with only `ID` and `Amount` — no `product_id`, no
   `product_type_id` — so every line falls through to the region's DEFAULT rate.
   A basket mixing 1% / 8% / 20% is charged 20% throughout. `ProvinceCode` is
   never sent, so US state and Canadian provincial tax never apply, and the
   applied rate is discarded at the boundary, so no order can answer "which VAT
   rate was charged on this line" — which is what an invoice needs.

6. ~~**No CORS.**~~ **CLOSED 2026-09-05.** The store surface answers preflights
   from configured origins (`CORS_ALLOWED_ORIGINS`, closed by default).
   Credentials are never allowed, which keeps the header-only CSRF immunity
   intact, and the admin surface still gets none — ADR 0011's rejection was
   about shipping the PANEL separately, and that reasoning is untouched.

   The original finding: No `Access-Control` handling and no OPTIONS responder in the
   middleware chain (`core/http/router.go`). The publishable
   key exists precisely so it can sit in a browser
   (`core/http/auth.go`: "NOT A SECRET; it is expected to be
   visible") and the preflight dies before that key is ever read. ADR 0011
   rejects CORS only as a way to ship the admin panel separately, and says so
   "because it buys nothing today, not because it is impossible" — so this is a
   gap, not the refusal it looks like.

7. **The panel is one screen.** **PARTLY CLOSED 2026-09-05.** It has a frame and
   a second section now: a stylesheet, a menu, a sign-out control, and the order
   list and order page next to the catalog.

   `corehttp.WriteAsset` has its first caller. It was built for panel styling in
   ADR 0011 and had never been called — the capability-without-a-consumer class
   this repository has a name for (ADR 0009). The stylesheet is embedded in the
   binary, stamped with an ETag derived from its own bytes (so a release
   refetches exactly when the file changed), and it is the only path besides the
   login that opens without an identity: the login page needs it, and a sign-in
   screen rendering unstyled because its stylesheet sat behind the sign-in is a
   poor first impression.

   The menu is built from a list the Go side supplies rather than from markup,
   so a section added to the panel enters the menu next to the route that serves
   it. The current section is marked with `aria-current`, which is what the
   stylesheet keys on AND what a screen reader announces — one fact rather than
   two that can drift.

   **A live run found a real defect.** The order page asked the read layer for
   one order by id and got a 422: the order Query provider offered
   `customer_id`, `region_id` and `status` but not `id`, while the product
   provider did offer it. The capability existed (`FetchByIDs`, the expansion
   path); the filter did not. It is there now, with the batch shapes and a
   refusal to combine it with another filter — the short-circuit answers from
   the batch read, so a second filter would be silently ignored.

   **Customers and inventory landed too (2026-09-05).** The panel now covers the
   four things a shop operator actually looks at: catalog, orders, customers and
   inventory. The customer screen tells a registered account from a guest, which
   is the first thing an operator needs to know before anything else on the row
   means what they think it means; the inventory list says "unknown" rather than
   0 when a total cannot be read as an integer, because a zero that is really an
   unread value sends somebody looking for stock that is on the shelf.

   Inventory has a list and NO detail page, and that is a decision: an item's
   detail is its per-location levels, which the panel already shows on the
   variant page, and reaching one item by identity would need a filter the
   inventory provider does not offer. Widening a module's published contract for
   a screen that duplicates another one is not a trade worth making.

   Still open: twelve of the sixteen modules have no screen — all of them configuration
   (regions, tax rates, shipping options, promotions, keys) rather than daily
   work — nothing can be created or deleted from the panel, and there is no
   extension point for a plugin to add a screen.

### The rest

83 gaps in total; the full measurement is at
`.claude/jobs/*/tasks/wgday14jh.output` for as long as that job lives. The 22
written refusals are listed there too and must be read as decisions: customer
identity (ADR 0008), scheduled compensation (ADR 0017), order editing
(`order/models/models.go`), and a capability with no consumer (ADR 0009).
