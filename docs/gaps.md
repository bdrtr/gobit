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
| A1 | ~~**Which packages become public**~~ **Answered 2026-09-05: ADR 0026 + 0027.** Fourteen packages under `core/` plus a four-method facade at the module root; 7.8% of the codebase, no commerce model among them. Six audits enforce it, one of them by compiling an out-of-tree module | ~~the library transition~~ — done. An out-of-tree plugin is proved; an out-of-tree application is proved too — `examples/starter` is compiled AND RUN by the audit | Importable core |
| A2 | **What gobit is legally** — data controller, or a library whose embedder is the controller | every KVKK item; ADR 0025 points at the second answer, which changes the obligation from "implement consent" to "publish the hooks and the erasure contract" | Turkey-specific |
| A3 | **May the customer pay a different amount than the merchant receives?** | installments (vade farki) AND multi-vendor commission. Today four guards including DB CHECKs forbid it | Turkey-specific, Commerce models |
| A4 | ~~**Invoice retention vs KVKK erasure**~~ **Named as a question on 2026-09-06.** **When a person asks to be erased and they are the buyer on an ISSUED invoice, what does the invoice module do with what it holds about them — refuse, blank it in place, or let it expire on a clock — and does the answer bind the two FREE-TEXT fields as well as the six named ones?** Measured: `invoices` carries six `buyer_*` columns and, on the same row, a `metadata` jsonb the caller fills and nothing validates; every `invoice_lines.description` is caller text too. Nothing can delete an invoice today and the protection is the ABSENCE of code rather than a constraint — the module's queries, its repository and its five routes contain no DELETE at all, so an erasure implementation issuing one would succeed. ADR 0024 says a canceled document keeps its number and stays in the table. Candidates: **(1) refuse, and leave the refusal a policy** — costs nothing in the module, and pushes a third outcome onto whatever contract B17 builds; **(2) refuse, and put the refusal in the SCHEMA** — the only candidate that closes the live hazard, because it stops the statement rather than the caller; **(3) keep the number and the amounts, redact the person in place** — supersedes ADR 0024's "no update path for its parties" clause and must say what happens to the two free-text fields; **(4) keep it whole for a window, then expire the whole (prefix, year) cohort** — the only one that needs a retention setting, and this repository has none outside the two idempotency stores. Every candidate needs B8 as its mechanism: `DeleteCustomer` is a soft delete and the customer module publishes NO event, so nothing can learn an erasure happened | all erasure work; and the hazard is live rather than hypothetical — nothing in the schema stops a `DELETE FROM invoices` | Observability and security |
| A5 | ~~**Group-price selection for a customer in several groups**~~ **Named as a question on 2026-09-06, and the measurement moved it.** **The cart tells the rule engines nothing about any group today. Should it send NONE, exactly ONE chosen by a precedence somebody writes, or ALL of them — and if all, what breaks a tie between two group prices that both match?** Measured: `customer_group_customer` is keyed `(customer_id, customer_group_id)`, so several groups per customer is what the schema already allows; and the cart's rule attributes carry the variant id and nothing else, so the situation the old row described is not reachable from the storefront at all — it is reachable from the admin price-calculation endpoint, which builds an arbitrary attribute map from `attr_`-prefixed query parameters. Pricing already HAS a tie-break: `better` in the pricing service ranks candidates, starting at the list tier (override over sale over base). So the tie question only exists once "all of them" is chosen, and it is pricing-only — the other two engines do not pick a winner. Candidates: **(1) send nothing** — today's behaviour, made explicit, and group pricing stays an operator-side feature; **(2) one group decides** — a precedence stored on the customer, and the context stays single-valued; **(3) send the SET and let the existing ladder decide unchanged**; **(4) the set plus a cheapest-wins rule inside the ladder**; **(5) the set, with the MERCHANT ranking price lists rather than groups** — which is the ladder's existing shape, so it costs the least new mechanism | B2B group pricing | Commerce models |
| A6 | **`allow_backorder`: give it a reader or stop publishing it** — still open, and **swept on 2026-09-06: it is one of FOUR, not one, and the four are one module's.** Every published boolean in the repository (35) was measured against every boolean column (17, in ten modules): `manage_inventory`, `allow_backorder`, `discountable` and `is_giftcard` are carried and never decided upon, and all four belong to the PRODUCT module. Every other stored boolean has a real reader — a Go branch, a SQL predicate, or both. So this row is not a stray flag needing attention, it is one DTO carrying four promises it does not keep, and the four want one answer rather than four. Two of them cannot acquire a reader inside the product module at all: the storefront carries the inventory record through UNINTERPRETED by ADR 0004, so the only possible reader of the stock pair is the checkout saga. What changed while the decision waits is the value's trustworthiness, not its meaning — see D2 | pre-order; today a published flag does nothing, and the flags measurement names it as the live proof of ADR 0009's second error class | Commerce models |
| A7 | **Does the panel become an admin-API client?** (supersedes ADR 0011) — and if so, the auth story, because a cookie on `/admin/v1` destroys its CSRF immunity and a token in JS is the exposure the cookie avoids | the SPA, and the extension-point design | Admin panel |
| A8 | **Catalog cacheability** — sales channel in the path, single-channel opt-in, or per-key caching accepted | edge caching. The repo already argued the default answer in the GraphQL handler | Storefront speed |
| A9 | **Customer identity: ADR 0008 stands or is superseded** | customer login, passkeys, a customer-facing assistant, A/B assignment, and it is why the address book is unauthenticated | Storefront speed |
| A10 | **pgvector: reopen ADR 0015 or not** | semantic search, visual search, embedding recommendations | AI subsystem |
| A11 | ~~**Where translated content lives**~~ **Named as a question on 2026-09-06, and one finding reorders it.** **When one shop sells the same catalog in two languages, WHERE does the second-language product title live — a sibling column, a translation side table per module, one translation module addressed over the read layer, or nowhere in gobit?** Measured, and it is the finding that matters: **the storefront has no notion of a shopper's language at all.** There is no `Accept-Language` handling and no language parameter; every occurrence of "locale" in the tree is about the PostgreSQL cluster's own collation (ADR 0015), not a reader. So "where does the translation live" is the SECOND question — the first is how a request says which language it wants, and nothing answers it. Candidates for the storage half: **(1) a sibling column per language** — cheapest to read, and it changes a published shape every time a language is added; **(2) a side table inside each module that owns the text** — keeps ownership where Principle 2.3 puts it and multiplies the same table by the number of such modules; **(3) one translation module keyed by (entity, id, field, locale)** — one table, and every read crosses a module boundary, which Principle 2.2 allows only through `core/link` or the read layer; **(4) gobit stores ONE language and the embedder supplies the second** — the answer most consistent with ADR 0025, and it makes the search index the embedder's problem too | multi-language, and it touches the search index, the panel and the invoice | Platform features |
| A12 | ~~**JWT TTL policy** — there is no refresh flow, so the TTL is an unstated trade~~ **Named as a question on 2026-09-06, and the measurement inverted its cost.** **Does gobit RATIFY the fixed lifetime (publishing the trade and bounding the value), or add RENEWAL — and if renewal, is the renewal credential a separate revocable row, does the live token renew itself at one identity endpoint, or does only the panel renew?** Measured: the lifetime is `JWT_TTL`, twelve hours by default, and `Config.Validate` accepts any positive value at all. The half everyone expects to be missing is already BUILT: verification is not merely cryptographic — it reads the user and then a session anchor on every request, and refuses a token issued before a logout or a password change. So revocation exists and is coarse, which makes "a long lifetime plus revocation" a configuration act rather than a build, and makes the refresh answer the expensive one. Candidates: **(1) ratify the clock** — no table, no endpoint, no ADR superseded; the price is that the mid-task logout stays; **(2) a separate refresh credential** — the only candidate needing both a new table and a new route, in a module whose one migration already holds five tables; **(3) re-issue the LIVE token at one identity endpoint** — no table, and the token becomes its own renewal credential; **(4) the panel renews silently and the JSON surface keeps the raw clock** — no published surface touched, and it leaves two session models in one product. Whichever is chosen, the answer has to carry a NUMBER, because A7's auth story turns on the words "short-lived" | session design; and A7 cannot be written until this carries a number | Observability and security |
| A13 | **Metrics posture** — OTLP-only, or expose a scrape endpoint | the "measurable speed" claim | Observability and security |
| A14 | **Price history: promote the accidental retention or drop it** | forecasting | AI-powered features |
| A15 | ~~**May the storefront take an address it cannot verify, in order to mail it later?**~~ **The question was too narrow and it was widened on 2026-09-06: may the storefront accept content from a party it CANNOT IDENTIFY, for this framework to act on later?** The address measurement of 2026-09-05 stands unchanged — no customer identity, no verification, no double opt-in, no unsubscribe, no per-address throttle, and a notification module that deliberately stores no recipient address — but it describes a CLASS rather than the waitlist. Re-measured: the storefront's only principal is the publishable key, and every storefront write takes its subject from a path identifier the client chooses (the address book, and `POST /store/v1/orders/{id}/returns`). **The repository has already answered one instance of this question and its argument is written down**, in the order module's store handler: an unauthenticated write is acceptable there because a request is a REQUEST — it moves no stock and no money, an operator has to receive it before anything happens, and every endpoint that ACTS is admin-only and scoped. That argument hands the decision its discriminator, and it is a question somebody can answer per feature: **does a human stand between the write and its effect?** The waitlist fails it outright — the effect of a waitlist row is an outbound message and nobody approves it. A customer-written REVIEW (B4) has the same shape and the same discriminator settles it: published on APPROVAL, the return-request argument covers it unchanged and A15 is already answered for B4; published on SUBMISSION, it is not covered and B4 needs this sentence before its first table. Either way what is stored is personal data, so A2 sits upstream of both. **B4 took the first answer and shipped on 2026-09-06, so this decision now has a SECOND worked example — and it is worth reading for what it adds rather than for the fact that it agrees.** (i) The discriminator has to be held by the TYPE or it is only a promise. The return request's argument lives in prose; the review module's lives in two SQL literals and a method set where nothing on the storefront side accepts a status at all, which is the difference between passing this test today and passing it after the next refactor. (ii) **The discriminator constrains the SCHEMA, not only the flow** — the first example never showed this because a return request stores no author. Because the writer cannot be identified, three columns were refused: an order id (it would narrow spam and authenticate nobody, and a verified-purchase badge rendered from it would be a false statement made by the schema — a column that cannot mean what it will be read to mean is worse stored than absent), an email address (an unverified mailing list with no unsubscribe, the exact property that makes the waitlist fail this decision), and an IP address (it would be the only network identifier of a shopper stored anywhere here, and the quota that would use it already exists one layer up). What is left is one identifying field, and it is the one the author typed in order to have it printed. (iii) It does NOT dispose of A2, and the module does not pretend otherwise; it narrows what A2 has to reach to a single column. (iv) The waitlist still fails, and now for a sharper reason: a review's approval step IS the feature, while a waitlist's would be an operator approving each outbound message one at a time — the human cannot be inserted there without deleting the point of it | the back-in-stock waitlist (C1), ~~the review module (B4)~~ built, and every future "tell me when" or "write us something" feature; A2 sits upstream of it | Commerce models |
| A16 | **What amount would a price filter compare against?** — measured 2026-09-05, and it came out of B2 as a DECISION rather than the build it was filed as. A product has no price. A price belongs to a `price_set`, the set reaches a VARIANT through a link, and one set holds many rows — currency × quantity tier × price list. "The price" is a selection FUNCTION of (currency, quantity, rule attributes, instant) resolved by five ordered tie-breakers (list tier, rule count, tier width, amount, id), not a column. The storefront is worse off than merely undefined: prices are fetched in a SECOND round trip made AFTER the page was cut by LIMIT/OFFSET, and what comes back is only the UNCONDITIONAL subset in every currency, with no currency, region or customer-group input — so there is not even a well-defined "the amount" on the page to filter on. Denormalising one into the catalog has NO invalidation signal: the pricing module publishes no events at all | the price filter AND the price sort in B2; "under 500 TL" in C10; a price column in any search index | AI-powered features |
| A17 | **What does "in stock" mean for a PRODUCT?** — measured 2026-09-05, out of the same B2 row and the same kind of answer: nowhere in this repository is it defined. Availability is defined at two granularities and BOTH sit below the product. The product module may legally read a link table (there is an argued precedent), so its SQL can answer "has an inventory item" but NOT "has stock" — the quantity is `inventory_levels.stocked_quantity`, the inventory module's own column, and ADR 0001 stops there. A REGION-correct answer is a THREE-module question: `stock_locations` carries no region column at all, and the region-to-location map is `shipping_location_regions` — in FULFILLMENT, keyed (location_id, region_id), with the region→warehouse direction deliberately unindexed because nothing reads it. There is no denormalised stock column and the event-driven route to one is blocked by B7 | the in-stock filter in B2; real-time stock (C16) reads the same absence; the waitlist (C1) needs the same definition to know when to fire | AI-powered features |
| A18 | **When a shopper filters by "Color: red", what counts as a match — the text EXACTLY as the catalog stores it, the same text case-insensitively, or the text folded to ASCII the way a handle already is?** Measured 2026-09-06, out of B2's option-value half, which called itself "a build behind one decision" and turned out to have this one at its head. **(1) Exact** — `value = $1`. It needs no new machinery and the ordinary client never notices, because the vocabulary endpoint hands back the value VERBATIM and a client filtering by what it was just given always matches; the cost lands on a hand-typed or hand-built URL, where "Kirmizi" finds nothing while "kirmizi" finds everything, with no way for the caller to tell that from an empty catalog. **(2) Case-insensitive** — the shape the catalog's `q` search already uses. It costs an expression index, which this repository has NONE of today, and it inherits ADR 0015's live hazard: on a cluster initialised `--locale=C` the fold does nothing for non-ASCII and the filter returns nothing, silently — the same failure that ADR exists for. **(3) Fold to ASCII** — the normalisation `slugify` and `turkishASCII` already perform for handles, so the rule would be one this repository already trusts and it is INDEPENDENT of the cluster's locale, which is the property (2) lacks. Its price is a stored normalised column (an expression index would put the cluster back in the loop) plus the admission that it MERGES values a merchant may have meant to keep apart. Whichever is chosen decides the index too, so the index is not a separate question | B2's option-value filter, and C10 through it | AI-powered features |

**Are these stated in a form somebody could answer? Read end to end on
2026-09-06, and the answer is mostly yes, with four exceptions and one
correction.** Twelve of the sixteen open rows named a CHOICE between alternatives
and say what each one costs — A2 names two legal positions, A6 the two exits
from a published flag that does nothing, A8 three cacheability answers, A13 two
metrics postures, A3 and A7 are yes/no questions with the consequence of each
answer written out, and A16 and A17 end in a definition somebody can put on
paper. Those can be answered in a sentence by somebody who has not read the
code, which is the property this group needs — and since 2026-09-06 all of them
have it, because the four below were given theirs. The group has grown to
seventeen open rows since: A18 was written the same day, out of B2's option-value
half, which called itself a build behind one decision and had that decision at
its head.

~~Four cannot, and the defect is the same in all four: **A4, A5, A11 and A12
name a TOPIC, not a question.**~~ **The four were measured and named on
2026-09-06, and each now carries its question and its candidates above.** The
reading job was done rather than guessed at, and the warning this paragraph
carried turned out to be the useful part: the first pass produced four sets of
candidates and an adversarial re-measurement found **twenty-four false claims**
across them — an auth module credited with four tables when its one migration
holds five, a price-rule context said to have a single production caller when an
admin endpoint builds an arbitrary one, an invoice described as holding the
person in six named columns when a seventh, free-form column sits on the same
row. A second pass fixed those and left smaller ones. So the rows above were
written to a deliberate rule: they carry the question, the distinct candidates
and what each costs, and nothing else. The inventories that kept coming back
wrong were dropped rather than repaired, because every extra measured sentence
in a decision row is another chance for the record to be wrong about the tree.

Two of the four moved when measured, which is the argument for measuring before
writing options. A5's situation is not reachable from the storefront at all —
the cart sends the rule engines a variant id and no group — so the row is about
what the cart SHOULD send before it is about breaking a tie. And A12's expensive
half turned out to be already built: verification reads a session anchor on
every request and refuses a token issued before a logout or a password change,
so revocation exists and the refresh answer is the costly one, not the cheap one.

The correction is A15, and it is recorded above: the question was named after
ONE feature while its subject is a class, so a second feature (B4) was about to
rediscover the same blocker from scratch. That is worth stating as a rule for
this table — **a decision named after the feature that found it will be read as
belonging to that feature**, and the next round pays for the question twice.

### B. Foundations — each unblocks several features

| # | foundation | unblocks | section |
| --- | --- | --- | --- |
| B1 | ~~**A guarded inbound-callback class**~~ **Built 2026-09-05: ADR 0028.** `core/http.CallbackRegistry` — per-route quota, body limit, timeout, enforced signature check and a derived replay window; PayTR converted onto it at the same URL. Audit deliberately left out (see D1) | four carriers, e-invoice, every payment provider — the plumbing is there; a carrier still waits on B10 | Turkey-specific |
| B2 | **Storefront filter surface** — ~~category, tag~~ **built 2026-09-05** (`category_id`, `tag_id`, in REST and GraphQL, EXISTS not joins so a product in several categories is returned once). ~~Still missing: price, in-stock, option value, sort~~ — **one bag holding four different kinds of work; measured and split 2026-09-05.** ~~SORT is a straightforward build with one trap (the cursor).~~ **SORT was built 2026-09-06, and the trap turned out to be already solved by the contract it rides.** `sort=newest|oldest` on the REST listing and a bound `ProductOrder` enum on the GraphQL one; the set is CLOSED and price is not in it, because price is not a column of this table and what it would mean is A16. Neither order costs a migration: `product_created_at_idx` is declared `(created_at DESC, id DESC)` and a b-tree is readable in either direction, so older-first is the same index walked backwards. The trap was that a cursor minted under one order is a valid position in the other's key space and would silently serve the wrong page — and `internal/core/page` already refuses that, because a cursor carries the NAME of its listing and the name is checked on the way back in. Making the order part of that name was the whole fix; the newest-first order keeps the bare name so cursors minted before the parameter existed still decode. Proved by three mutations, and the third is the one worth keeping: dropping the order on the way to the repository leaves every SQL test GREEN and fails only against a real database, which is what the integration half is for. OPTION VALUE is a build behind one decision, entirely inside the product module. PRICE and IN-STOCK are NOT builds — they are decisions, and they are now **A16** and **A17**. ~~Also measured: the built half never reached the read layer, so the panel cannot filter by category while the storefront can~~ — **that half reached the read layer the same day.** The `product` provider takes `category_id` and `tag_id` too now (two switch cases, NO new SQL — the EXISTS subqueries were already in the listing and the count), it REFUSES either of them combined with `id`/`ids` rather than answering from relation slices it never loads, and the module offers a second read-layer entity, `category`, so a consumer can turn a name into an identifier. ~~What is still behind is exactly one filter: the storefront's text search~~ — **that one closed too, later the same day.** The provider answers `q` (the storefront's own spelling), trims it, treats an empty or whitespace term as NO filter, and REFUSES it beside `id`/`ids` on a measured argument rather than a taste. The panel grew the search box that consumes it. The cost was measured on the 52,004-product rig instead of guessed (`docs/catalog-search-cost.md`) and the finding inverts the expectation the missing index creates: a term matching 52,000 titles is answered in 0.03 ms, a term matching ONE product costs 9.1 ms and reads the whole table — **the rare search is the expensive one, and that is the one a search box receives** | ~~NL search~~ **C10 does not exist in code** — but `category_id` has a real named consumer since 2026-09-05: the panel's product list narrows the catalog through the read layer and offers a dropdown of category names. `tag_id` has none (see A16, A17 and the section below) | AI-powered features |
| B3 | ~~**Storefront vocabulary endpoints**~~ **Built 2026-09-05.** `GET /store/v1/{collections,categories,tags}`. The category listing applies `is_active`/`is_internal` — two columns that existed since the first migration and that nothing read | NL search — the word→id half is done; the FILTER half is B2 | AI-powered features |
| B4 | ~~**Review module**~~ **Built 2026-09-06 — and A15 was applied rather than quoted, which is why the answer is in the SQL and not in a comment.** The discriminator settles the first table: a review is born `submitted`, the only path out is an admin endpoint, and the storefront's argument is therefore the return request's own, unchanged. The invisibility is a property of the TYPE, not of a call site — `ListApprovedReviews` and `SummarizeApprovedReviews` carry the status as a SQL LITERAL, the repository and the service expose them as separate methods that take no status at all, the storefront handler interface names them separately, and there is no storefront read of a single review by id, so the id a submission returns is an acknowledgement rather than a handle to an unapproved row. A shared query with a status parameter was refused for the reason that it would leave the whole design one missing assignment from publishing everything ever submitted. Four transition edges, each argued: submitted→approved, submitted→rejected, approved→rejected as the only way to unpublish, and rejected→approved as the only repair, because the author cannot resubmit. Self-edges are refused so the moderation moment never lies about when a human looked, and a FULL mirror CHECK ties `moderated_at` to the status in both directions — expressible for the same reason fulfillment's `returned_at` is, that nothing moves back out of the state it mirrors. Proved where the guards are real: `TestAnUnapprovedReviewIsInvisibleOnTheStorefront` and `TestTheStorefrontCannotPublishItsOwnReview` run through the production publishable-key check, prefix quota and idempotency ring, and the second one shows the door is CLOSED rather than merely ignored — a `status` field in the submission body is refused with 422, and a status query parameter on the storefront listing widens nothing. NOT built, each argued in the files rather than left as an apparent omission: no order id and so no "verified purchase" (it would narrow spam and authenticate nobody — it is the very credential the return request runs on — and a badge rendered from it would be a false statement made by the schema); no email and no IP, only the byline the author typed in order to have it printed; no read-layer provider, no interop surface and no event, because each would be a published capability with no consumer, which is what the audits refuse and what C11 will bring its own reader for. The AVERAGE is computed on read and that was MEASURED, not preferred — the numbers and the crossing point are in the review-summaries section | ~~moderation (the AI brief's first use case), summaries, Q&A — and it is blocked by A15, not only by its own absence~~ — the data exists. Moderation is a human's today and the AI half is still the `ai` package; C11 needs two hooks the module deliberately withheld, and its row says which | AI subsystem |
| B5 | ~~**Order ↔ fulfillment link, and something that creates a fulfillment**~~ **Built 2026-09-05.** The fulfillment module declares `order_fulfillment` (one to many); `internal/workflows/fulfilling` opens a shipment for an order and binds the two; the order gets `POST`/`GET /admin/v1/orders/{id}/fulfillments`. NOT built: a shipment created at checkout — shipping stays a decision | the order timeline, carrier tracking, "where is the parcel" — answerable now. the link is expandable by the read layer too (D8) | Platform features |
| B6 | ~~**A money-event read surface**~~ **Built 2026-09-05.** The payment collection entity offers `first_captured_at` and `last_refunded_at`, loaded by a SECOND batch query and only when asked for; the order's payment view and `GET /admin/v1/orders/{id}/payment` carry both. NOT added: `authorized_at` and `refunded_at` columns — neither exists in the schema at all | the timeline's two most-asked facts — answerable now | Platform features |
| B7 | **Inventory movement ledger + inventory events.** Measured 2026-09-05: the EVENT cannot be landed on its own. `TestTheEventTopicsHaveASubscriber` refuses a topic no production file subscribes to, its exemption map is empty by policy, and the one plugin that reads the catalog indexes no stock — so there is no subscriber to give it today. The event and its first consumer are one package or neither | forecasting, real-time stock, and an audit trail stock does not have | AI-powered features, Storefront speed |
| B8 | **Customer module events** (`customer.deleted` at minimum) — and it carries B7's blocker, which this row did not say until 2026-09-06. `DeleteCustomer` is a soft delete and the customer module publishes NO event at all, so nothing can learn an erasure happened; but the event cannot land ALONE either, for the same reason B7 records — `TestTheEventTopicsHaveASubscriber` refuses a topic no production file subscribes to, and its exemption map is empty by policy with the price of a line in it written down. The subscriber `customer.deleted` wants is the erasure coordinator, which is B17, which waits on A2 and A4. So the event and its first consumer are one package or neither | erasure — and A4 names this as the mechanism every one of its candidates needs | Turkey-specific |
| B9 | **Stored payment instrument** — provider contract + table | saved cards AND subscriptions, in one change | Storefront speed, Commerce models |
| B10 | **Carrier-capable quote input and a tolerant shipment state machine** — ~~district, dimensions/desi, more statuses (including iade), and out-of-order webhook tolerance~~ **the state-machine half was built 2026-09-06; the quote-input half was measured the same day and is larger than this row said.** BUILT: `returned` is the fifth status (migration 000004), with `returned_at` under a FULL MIRROR check — expressible only because the status is terminal, unlike the three one-directional stamps of 000001 — a `POST /admin/v1/fulfillments/{id}/returned` route, and a transition table that now distinguishes a carrier's REPORT from a command. The tolerance is a fourth outcome rather than a looser second table: a collection reported after a delivery is ACCEPTED, the status does not move backwards, the tracking number is taken (often it is the only message carrying it) and no moment is stamped, because the only clock available would date a dispatch after its own delivery. STILL MISSING, and it is not this module's to close alone: `core/provider.QuoteInput` carries country, weight, item count and the option's own data, and nothing else — so neither of the two numbers a domestic carrier's tariff is a function of, the district and the desi, can be expressed. Widening it is a decision about a PUBLISHED contract (ADR 0026), and two more things have to move with it: no address table in this repository has a district column (the cart's and the order's carry city and province), and parcel dimensions are a catalog fact the product module owns | any real carrier | Turkey-specific |
| B11 | ~~**Order addresses**~~ **Built 2026-09-05.** `order_addresses` (one shipping, one billing, enforced by a unique index), written in the SAME transaction as the order's header and lines; carried cart → interop → checkout plan → order snapshot. The cart's own schema comment already named the order as the thing its copy protects — and the order had no address at all | invoicing, shipping labels, B2B — unblocked | Storefront speed |
| B12 | ~~**Outbound delivery machinery** — retry and a dead-letter queue on the outbox relay~~ **Built 2026-09-06, and the "explicit decision" it rested on turned out to describe a different layer.** The outbox gains `next_attempt_at` and `dead_lettered_at`; a failed publish waits out a doubling delay (1, 2, 4 … capped at 60 minutes) and after ten attempts — four hours and three minutes of trying — is given up on and leaves the relay's index. Giving up is a WRITE, not a drop: the instant, the attempt count and the last error stay on the row, the relay job reads the pile on every pass, and a non-empty pile FAILS the run, which is the one channel that reaches the `gobit jobs` listing. Measured before it was built, and it is why the ceiling is not optional: a batch limit's worth of permanently failing rows fills every pass, so five consecutive passes published NOTHING while a healthy event written behind them finished with `attempts = 0` — never attempted once. Not degraded delivery, NO delivery. ~~Still missing, and it is the operator half: `Redrive` and `Discard` exist, are tested, and have NO production caller — no command, no route — so today the alarm has no off switch a human can reach without SQL~~ **The operator half was built the same day.** `gobit deadletters` lists the pile and `gobit deadletters redrive <id> -confirm <id>` and `gobit deadletters discard <id> -confirm <id>` are its two exits, so the alarm now has an off switch that is not psql. The listing carries what a decision needs — the whole pile's count and not the page's, the event name, the attempt count, the last error, both instants and how long the row tried — and says out loud that the payload was WITHHELD rather than absent; the act path re-reads the pile and closes with whether the `gobit jobs` alarm will clear, which is the question the operator arrived with. One id per verb, and refusing a bulk flag was argued rather than assumed: measured against a real PostgreSQL a single discard is a primary-key delete at 0.047 ms and a redrive 0.183 ms, so the refusal costs the database nothing and buys the reading of the row that a one-keystroke mute would skip | webhooks, ERP/Slack integration — the delivery machinery is there, a plugin can now own a periodic pass (B13, built the same day), and the one thing still missing is the SENDER itself (C5) | Platform features |
| B13 | ~~**Plugin host: let a plugin register a job**~~ **Built 2026-09-06, and it arrived WITH its first consumer rather than before one.** `plugin.Host.RegisterJob` takes a four-field `plugin.Job` — name, interval, per-run bound, work — collected during Setup the way routes are, and drained by the composition root's `addPluginJobs` at the moment the job registry exists. ADR 0019 deferred this surface with a condition rather than a refusal ("it arrives with the first plugin that brings a job") and ADR 0026 had priced it as publishing the whole scheduler package; that price was refused, because a plugin needs four VALUES while publishing the package would freeze the runner, the store contract, the advisory-lock class and the key algorithm into the compatibility promise — publishing the machine in order to hand out a form. The copy is paid for rather than hoped away: `TestEveryJobDefinitionFieldReachesAPluginJob` reflects over the scheduler's own definition and fails the day it grows a field the published struct does not carry. Nothing is validated twice and nothing is SKIPPED: a plugin's definition goes through the same `Registry.Add` as the core's three, so a missing name, a `MaxRun` longer than its interval, a nil body or a name already taken all refuse the BOOT — because a job dropped quietly would leave `gobit jobs` printing a listing with nothing missing from it, and an operator reads that absence as "that pass had nothing to do". The plugins are admitted LAST so that a name clash fails on the side whose name is the easier one to change. The first consumer is in the same change: `paymentpaytr` now runs an hourly `pendingWatch` for payments PayTR never called back about — a class that does not arrive at startup but ACCUMULATES, and which until now was reported exactly once, at boot, by a process that then watched it grow in silence | ~~any plugin needing a retry pass~~ — done; the outbound sender (C5) is the next thing that would use it | Platform features |
| B14 | ~~**Order line-item entity in the read layer + date filter + index**~~ **Built 2026-09-05.** `order_line_item` is the order module's SECOND read-layer entity, offered next to `order` the way fulfillment offers the shipment next to the shipping option. It carries what was sold and for how much, and takes `placed_from`/`placed_to` — half-open, matched against the ORDER's `placed_at` through a join, because a line's own `created_at` is the day its ROW was written and would date an exchange as a fresh sale. Migration 000006 brings the two indexes that keep the range and the variant filter off a full scan. It has a consumer: the panel's Sales screen. NOT built: any aggregation (the provider returns records, never sums), no line ↔ variant link so nothing expands from a sold line to its product, and forecasting itself | demand analytics and forecasting — the READ is there, the analytics are not; the other half of a forecast is still B7 | AI-powered features |
| B15 | **File read-back, ~~product-image ↔ upload link~~ built 2026-09-05**, file events still missing. `product_image.upload_id` plus the `upload_product_image` link (declared by PRODUCT — it writes the record the binding carries; the file module's own doc says it does not know what a file belongs to). The file module gained the interop surface it deliberately lacked, and the reasoning behind that absence turned out to be half wrong: the address shows the file and says nothing ABOUT it | anything that looks at a photo — the id half is answerable now; file EVENTS are not built | AI-powered features |
| B16 | **A suggestion store** — system proposes, human applies | forecast and category suggestions. The pattern exists (`sagawatch`, ADR 0017); the storage does not | AI-powered features |
| B17 | **KVKK erasure, export and retention** | a legal requirement; A2 and A4 come first | Turkey-specific |
| B18 | ~~**A per-column round-trip test for every module**~~ **Built 2026-09-05** as `TestEveryColumnIsWrittenBySomething`: the schema and the queries are read TOGETHER, and a column no INSERT names and no UPDATE sets fails. DEFAULT and GENERATED columns are out of scope — the database supplies those | nothing — and it caught a standing finding on its first run (see D9). ~~**What it does NOT catch was measured on 2026-09-06 and it is three holes wide, one of them the very finding its own godoc names as the example — see D16**~~ **Two of those three closed on 2026-09-06, a fourth hole was found while closing them, and the third — text, not the call graph — is measured and deliberately open (D16). The fix surfaced NINE live findings on its first run, and EIGHT of the nine were answered later the same day — four by writing a delete that had never existed, four by DROPPING a column that was wrong. The ninth is not a placeholder any more: its exemption states the question (D18). And a hole of a DIFFERENT kind was found later the same day, from the other end: all of the above is about what the audit sees inside its scope, and the scope itself is 17 of the repository's 25 schemas — ten tables owned outside the module tree have no column audit at all. It was deliberately not widened and the reason is in D23** | Common Go mistakes |

### C. Features — after the above

| # | feature | waits on |
| --- | --- | --- |
| C1 | **Back-in-stock waitlist.** ~~The cheapest real feature; every part exists and only a table is missing~~ — **that claim was wrong, measured 2026-09-05.** Three parts are missing, not one: the table, an inventory EVENT (the module publishes nothing at all, so there is no "it is back" to react to), and a subscriber to turn it into a message. The notification side does exist — `Service.Notify` — but it is reached by SUBSCRIBING, so the event is the load-bearing half. **A SECOND blocker was measured on the same day and it is a DECISION, not a gap: A15.** The table would hold an address this repository cannot verify, cannot let anyone unsubscribe from, and does not throttle per address | A15 first, then B7 (inventory events) |
| C2 | ~~**Order timeline**~~ **Built 2026-09-05.** `GET /admin/v1/orders/{id}/timeline` — composed, not a table; every entry names the CLOCK that stamped it, because the capture and a parcel's transitions are on the application clock while everything else is on the database's. ~~Undated facts (an exchange that finished) come back last rather than being dropped~~ **Corrected 2026-09-06 (D4): that sentence was false the day it was written.** The undated machinery stays — `sortTimeline` puts a nil `At` last and `TimelineEntry.At` is a pointer for that reason — but nothing produces an undated entry any more, and a finished exchange is not merely undated, it is unrepresentable: the order module's migration `000008_order_exchange_cancel` drops `order_exchanges.completed_at` and narrows the status CHECK to `('requested', 'canceled')`, the completed-exchange constant and the timeline's unreachable branch are deleted, and `TestExchangeStatusHasNoCompletedValue` keeps them gone. A withdrawn exchange is a DATED `exchange.canceled` entry | ~~B5, B6~~ done |
| C3 | **Operator assistant in the panel** — sixty-four primitive interop methods are already a tool catalogue, and identity exists inside the panel | a return-creation surface |
| C4 | **Consent records and data-subject endpoints** | A2, B17 |
| C5 | **Outbound webhooks** (NATS after, if anyone asks) — **the sender was BUILT on 2026-09-06 as `plugins/webhookout`, and it is NOT INSTALLABLE. That was measured, not assumed.** What exists is whole and proved: a receiver is registered over `POST /admin/v1/webhooks`, which mints its signing secret server-side and returns it exactly ONCE; four topics are subscribed BY NAME, written out rather than ranged over, because the reverse topic gate resolves a subscription statically and a loop variable is a name it cannot resolve; an event on the bus writes one delivery row PER RECEIVER and does nothing else, since the bus logs a handler's error and counts the event processed, so anything the handler does not finish is lost for good; a minute-by-minute job claims what is due by moving `next_attempt_at` forward — a LEASE, not a held transaction, because an attempt is an HTTP request to a third party and a transaction spanning a pass of them would hold a pool connection for most of a minute exactly when the receiver has stopped answering; and every attempt is signed HMAC-SHA256 over a LENGTH-PREFIXED join of six values, the same rule ADR 0028's inbound ring uses, so a body cannot be moved onto another event's signature. The queue is deliberately NOT `event_outbox`, and the schema is the refusal: that table is keyed on the EVENT and carries one attempt counter, one next attempt and one dead-letter stamp, so an event owed to three receivers with one of them down has no expressible state in it — and putting webhook deliveries in the relay's pile would make a third party's decommissioned endpoint indistinguishable from gobit's own bus failing to accept an event, in the one listing an operator reads to tell those apart. A pile given up on FAILS the delivery run, and `GET /admin/v1/webhooks/deliveries?state=dead` plus redrive and discard are the two ways out. Plain http is refused unless the host is loopback; resolving the host to refuse a private address is NOT done and the refusal is argued — a resolve-then-connect check is not a check, and closing it properly means a dialer that refuses at connect time. Proved from a real cart through a real order to a receiver that holds nothing but the issued secret (`TestARealOrderReachesARealWebhookReceiver` in `internal/e2e`, observed passing 2026-09-06), which is the only place that can catch the order module and the plugin disagreeing about the event. **What is missing is ONE MAP ENTRY.** `webhook-out` is absent from the composition root's plugin catalog, so `PLUGINS=webhook-out` stops the boot with "unknown plugin". Measured with `go list -deps ./cmd/server`: the closure names eight plugins and not this one, so the package is not compiled into the binary at all and its migration is outside `gobit migrate` too. The plugin's own package doc says it is "named nowhere except one line in the composition root's catalog" — that line does not exist. Why no gate caught it is D22. **The catalog line landed 2026-09-06 and C5 is now WHOLE: `webhookout.Name` is in the installer's map, `go list -deps ./cmd/server` names the package, and its migration is inside the migrate surface.** | ~~B12~~ ~~B13~~ both done — retry, the dead letter and a plugin-registrable periodic pass are all built. The SENDER is built too; what is left is the catalog line, and nothing else |
| C6 | **Carrier plugins** (Yurtici, Aras, MNG, PTT) | ~~B1~~ built (ADR 0028); B10's state machine is built and its quote input is not; **and a third blocker was measured 2026-09-06 that neither row named: a carrier has NOWHERE TO DELIVER what it receives.** The fulfillment module's cross-module write surface is five primitive methods and none of them can move a shipment to shipped, delivered or returned. The admin routes can, but a carrier's webhook holds no admin credential — that is the premise the callback ring is built on — and a plugin cannot call the service directly, because the plugin-import gate refuses the import and a structural interface cannot be written either while the method returns a module type the plugin's package cannot name. The method is deliberately NOT added ahead of the plugin (a cross-module surface with no consumer is a contract nothing can check), and the shape it should take is written down where it will be needed: ONE method carrying the carrier's OWN instant, because the tolerance is a property of the sequence rather than of any single transition, and because that instant is the one thing an admin route cannot supply and a carrier always can |
| C7 | **Installment table** + iyzico/Param providers | A3 |
| C8 | **Digital product delivery** — entitlement, expiring link, re-download policy | — |
| C9 | **B2B: quotes, terms, minimum order** | A5 |
| C10 | **NL search layer** | B2; ~~B3~~ **built 2026-09-05** — the word→id half is done (`GET /store/v1/{collections,categories,tags}`), so what NL search still waits on is the FILTER half, B2, of which sort and option value are builds while price and in-stock became A16 and A17 |
| C11 | **Review summaries and Q&A** | ~~B4~~ **built 2026-09-06, so the DATA exists** — and the two hooks a summariser needs are deliberately absent, named in the review module's own package doc as arriving WITH their first reader. It publishes no read-layer provider, so nothing outside the module can read a review without importing it, which ADR 0001 forbids; and it publishes no event, so a stored summary has nothing to invalidate it. Both were withheld rather than forgotten: an interop surface no production file resolves fails the consumer audit, and a topic no production file subscribes to fails the topic gate whose exemption map is empty by policy. C11 is the reader that makes both landable, in one package with them. The `product.metadata` measurement below still says where the summary may NOT live |
| C12 | **Subscriptions** — a second axis on the order, not a fifth status | B9 |
| C13 | **Feature flags, then A/B** | A9 for the assignment key |
| C14 | **Panel: extension points, then the SPA if A7 says so** | A7 |
| C15 | **Multi-language** | A11 |
| C16 | **Real-time stock** | B7, plus a fan-out the bus cannot do today |
| C17 | **Edge caching** | A8 |
| C18 | **Multi-vendor marketplace** — changes what every money path means; last, deliberately | A3 and most of the above |

### D. Corrections — small, and some are live defects

- **D1** ~~`/paytr/callback` sits outside every guarded prefix~~ **Fixed
  2026-09-05 (ADR 0028).** It now goes through the callback ring: quota, 64 KiB
  body limit, 10s timeout, signature verified before anything reads the payload,
  and a replay window keyed on the signed fields. `TestEveryStateChangingRouteIsGuarded`
  keeps the class closed — a write bound outside the guarded prefixes now fails
  the build. Still NOT audited: writing a row for an actor this repository only
  asserts contradicts the audit contract in four places, and nothing reads
  `audit_log` yet. That is decision 1 of ADR 0028.
- **D2** `allow_backorder` is published and does nothing (see A6). **Measured
  2026-09-06 and this line was three flags too narrow:** `manage_inventory`,
  `discountable` and `is_giftcard` are in exactly the same state, all four in
  the product module. Giving them a reader is still A6's decision, but the two
  ways the stored values could rot while it waits are now closed, and the
  closing was measured first. **The defaults were unpinned:** all three flags
  that have a defaulting block were flipped one at a time
  (`manage_inventory` true to false, `allow_backorder` false to true,
  `discountable` true to false) and the whole product suite — unit AND
  integration against a real PostgreSQL — stayed green on every flip.
  `TestCreateVariantDefaultsToManagedStockWithoutBackorder` and
  `TestCreateProductDefaultsToDiscountableAndNotAGiftcard` pin them now, with
  `TestCreateVariantHonorsExplicitFlagValues` and
  `TestCreateProductHonorsExplicitFlagValues` covering the other direction —
  the flags are pointers precisely so a sent `false` differs from "not sent",
  and a defaulting block that overwrites instead of filling in passes the
  default tests untouched. **And a partial update could have reset them:**
  `TestPartialVariantUpdateDoesNotResetTheFlags` proves against a real database
  that an update naming neither flag preserves both. It cannot be a unit test —
  the preservation is not written in Go at all, it is one `COALESCE` per column
  in the product module's variant query, and the service package's fake store
  models neither column.

  The reasoning is the part worth keeping: **a flag nothing reads has no second
  line of defence.** A flag that IS read fails some downstream test when its
  default moves; a carried-only flag fails nothing, while the column keeps
  accumulating a value per row. The damage surfaces on the day A6 is finally
  answered and a reader acts on every row written in the meantime — with no
  migration able to tell the intended values from the accidental ones. For a
  carried-only flag the default IS the whole contract, because it is the only
  part of the flag anything in the repository actually produces.
- **D3** The address book's storefront endpoints are unauthenticated and keyed by
  a path id — personal data anyone can read and change. A known consequence of
  ADR 0008, in its sharpest form.
- **D4** ~~`order_exchanges.completed_at` and `canceled_at` exist and are never
  written; there is no Complete or Cancel query for an exchange.~~ **Fixed
  2026-09-06, and not by writing both — one column got a writer and the other
  was DROPPED.** `CancelOrderExchange` is the first `UPDATE` the
  `order_exchanges` table has ever had, and it is bound to
  `POST /admin/v1/orders/{id}/exchanges/{exchangeId}/cancel`, because a write
  query no route reaches would have turned the audit green while leaving the
  column exactly as unwritable as before. Migration 000008 drops `completed_at`
  and removes `completed` from the status CHECK: exchange completion needs
  goods shipped out against an existing order — a capability the framework does
  not have anywhere — AND a positive difference collected against it, which the
  one-to-one `order_payment` cardinality forbids. Three separate places in this
  repository had already written that down, including the section on
  after-sales below. A state nothing can enter and no moment can date is not a
  gap; it is a state that should not exist.

  `canceled_at` got the FULL mirror CHECK — `(status = 'canceled') =
  (canceled_at IS NOT NULL)` — and it could only be added because the column
  was dead: no existing row holds `canceled`, so every row satisfies both
  directions. D5's `archived_at` could not have this, and the two entries
  should be read together.

  Three dead constructs went with it rather than being left looking alive: the
  completed exchange status constant, the transition-table entry that named its
  complete action and that nothing could call, and the timeline's unreachable
  unfinished-exchange branch. All three are deleted, so none of them resolves
  any more and none of them can be cited here — which is the audit working.
  If a hand-written row somewhere holds `completed`, the migration
  FAILS rather than rewriting it — no code here could have produced that row,
  and quietly resetting it would destroy the only evidence of it.

  **The entry sat open for a reason worth recording: the audit built to catch
  exactly this never caught it.** See D16.
- **D9** **Neither the order nor the payment module ever soft-deletes.** Ten
  `deleted_at` columns are never written by anything, while every read in both
  modules carries `deleted_at IS NULL` — a predicate that has never once been
  false. Found by B18's audit on its first run and recorded in its exemption
  list. Dropping the columns is a schema decision; taking the deletes on is a
  product one.
- **D8** ~~The link's far side names a `fulfillment` entity that has NO Query
  provider.~~ **Fixed 2026-09-05.** The fulfillment module offers a SECOND
  entity, `fulfillment`, with the twelve fields a timeline asks for — including
  the three transition moments, which are null rather than a zero time. The
  `order_fulfillment` link is now walkable by a Graph request, proved end to end
  with two parcels on one order so the batch read and the one-to-many
  cardinality both bite.
- **D5** ~~Archiving an order leaves no timestamp — the status flips and nothing
  records when.~~ **Fixed 2026-09-06.** Migration 000007 adds
  `orders.archived_at` — nullable, no default — and `ArchiveOrder` writes it
  from the DATABASE clock in the same statement that flips the status. It
  reaches the model, the admin DTO, the read layer's `order` provider
  (`FieldArchivedAt`) and the timeline, which now emits a dated `order.archived`
  entry. `completed_at` is provably untouched by archiving.

  The SHAPE was decided by measurement rather than by taste: three of the four
  order statuses already carry their own moment (`placed_at`, `completed_at`,
  `canceled_at`) and two of the three are held to their status by mirror
  CHECKs. Archiving was the one transition of four that flipped a status and
  wrote nothing. A dated-transitions side table would have left three stamps on
  the row and a fourth elsewhere with no rule saying which is authoritative,
  and it would have made the two existing mirror CHECKs unenforceable — a CHECK
  cannot see another table's rows.

  Two refusals are recorded in the migration. A `DEFAULT now()` was refused:
  it would stamp the column when the ROW is written, so every order would claim
  to have been archived the instant it was placed — and it would also move the
  column out of the column audit's scope by that audit's own rule, buying
  silence instead of safety. And the CHECK holds in ONE direction only,
  `archived_at IS NULL OR status = 'archived'`, where the siblings get the
  mirror form: orders archived before the column existed carry the status and
  no moment, and the only way to add the other direction would be to backfill a
  moment nobody recorded. Migration 000003 answered that exact question the
  same way for `received_location_id`. The consequence is pinned by a test
  rather than hidden.

  The rejected alternative was `updated_at`, and it looks free — on an archived
  order the two hold the same instant today, because archiving is terminal so
  no later write moves it. Rejected because that equality is an accident of the
  current query set rather than a property of the row, and because `updated_at`
  cannot say WHICH write it timed even while it is correct.
- **D6** ~~Two repository-internal transactions (tax, region) cannot compose into a
  service transaction.~~ **Fixed 2026-09-06 — and the entry named the wrong
  second module.** Tax had the defect and now has the plumbing. Region was
  MEASURED and does not have it: the entry paired the two on a shape they share
  (a transaction opened inside a repository method) rather than on the defect,
  which is a service method that reads, decides, and then writes as two separate
  autocommit statements. The tax repository's private
  `inTx(ctx, func(q *taxdb.Queries) error)` is gone. In its place is an
  exported `WithTx(ctx, func(ctx context.Context) error)` that carries the
  transaction in the CONTEXT — the same ambient plumbing the other six modules
  already had — so the service decides the boundary and two repository calls
  can share one transaction. The signature had to come down to types both sides
  share: ~~ADR 0001 forbids the service importing the repository package, so no
  type of that package can appear in the interface the service declares.~~
  **Corrected 2026-09-06: no rule forbids it, and the attribution was wrong
  twice over.** ADR 0001 decides inter-MODULE communication — a consumer-side
  narrow interface with the implementation resolved from the container by name —
  and its enforcement is depguard's cross-module deny list; it says nothing
  about a repository package or about the boundary inside a module. The
  in-module boundary that IS enforced is `layerRules` in `internal/arch`, cited
  to ADR 0012, and it forbids a service importing `net/http`, chi and pgx —
  `/repository` is forbidden to the `api` layer, not to the service. The tree
  proves the point: the product service imports its own repository package in
  five production files and takes `repository.Store` as a field and as a
  parameter of the exported `NewCategoryProvider`. What the tax service does is
  a CONVENTION its own godoc names as the IN-MODULE counterpart of ADR 0001's
  pattern, and the pricing, auth, customer and b2b services say it in the same
  words. The convention is what made the signature come down to shared types; a
  prohibition would have made the product module a violation, and it is not.

  **And the entry was understating the problem in the way that matters: the day
  a caller needed this had already arrived, and a godoc was claiming it had
  been handled.** `DeleteTaxRegion` said its lock "prevents a race with a
  concurrent rate-insertion flow, because that flow also reads the region under
  a shared lock." Measured: the tax module contained ZERO `FOR SHARE` queries.
  Rate insertion read the region unlocked and in a SEPARATE transaction, so a
  delete landing between the check and the write was invisible to it. The
  foreign key cannot catch that, because the delete is SOFT — the parent row
  stays. The result is not merely an orphan: `ResolveTaxRegions` matches a
  province row on its own and the chain walks most-specific first, so every
  basket in that province would keep being taxed from a region the operator
  believes deleted, even after a fresh root was opened for the country. The
  sentence is true today: `GetTaxRegionForShare` is the module's first shared
  lock, `LockTaxRegion` refuses to run outside a transaction (a `FOR SHARE`
  taken without one is released immediately and protects nothing while looking
  like it does), and both the province-insert and the rate-insert paths take it
  inside the same transaction as their write.

  **Region, measured 2026-09-06: the defect is not there.** Every region service
  method that writes makes exactly ONE repository call, so there is no service
  method to span; the read-decide-write compositions live a layer down inside
  the repository's own transaction, under a documented lock order. The soft
  delete hazard that made tax dangerous is real here too — the country's foreign
  key references the region row and cannot see `deleted_at` — but a shared lock
  taken before the write already closes it, and deleting that one line makes an
  existing test fail with the orphan it was written to prevent.

  What region was actually missing was PROOF, and that is now closed: the
  transaction frame around the region delete could be removed outright, turning
  its two writes into two autocommit statements — the exact tax shape — and the
  module's whole integration suite stayed GREEN. Nothing held the frame in place.
  A test now pins the intermediate state a third connection can observe while the
  delete is blocked, and it fails under that mutation.

  **The four modules this entry named as unmeasured were measured on
  2026-09-06, and the answer split two and two.** The question put to each was
  the one this entry had to learn to ask — not "does a repository open its own
  transaction" but "does a SERVICE method read, decide, and then write as two
  separate autocommit statements". A module where the defect is ABSENT is a
  result, and it is written down here as one:

  | module | defect | what the measurement found |
  | --- | --- | --- |
  | `pricing` | **no** | every writing service method makes exactly ONE repository call. The only multi-call methods — `ListPrices`, `ListStorePrices`, `ListPriceRules` — are read-only, and their extra read exists to return a 404 rather than an empty list. The repository already had the finished shape: `ReplacePrices` opens its transaction with `GetPriceSetForUpdate`. No behaviour was changed |
  | `promotion` | **yes, twice** | `AddPromotionRule` and `SetApplicationMethod`. Both races were PRODUCED against the original code, not argued. Fixed; the consequence is weaker than tax's and the entry says so — D20 |
  | `auth` | **yes, and the damage outlives the request** | `Service.SetPassword`. Produced, and the orphan it leaves burns an e-mail address in a unique index forever. Fixed — D19 |
  | `customer` | **shape yes, defect no** | `AddToGroup` runs its two existence checks unlocked, so the race is REAL: under READ COMMITTED a transaction without a lock protects nothing, and an intervening soft delete is simply not seen by the decision already made. Its consequence is zero, and that was measured rather than assumed — a deleted group's membership rows are already LEFT BEHIND by the ordinary delete path, so the row the race writes is indistinguishable from one the module produces in normal operation. No lock was added; the godoc now says which lock it would have to be (`GetCustomerForUpdate`, because the customer row is always locked first here) so that the next reader does not have to measure it again |

  Two things follow that are worth keeping. The first is that this entry's
  original error — pairing modules on the shape — would have been made three
  more times: two of the four carry the shape and no defect. The second is that
  finding the defect is only half the work. In `promotion` it turned up a godoc
  claiming a protection that did not exist, which is the exact failure tax was
  recorded for; in `pricing` it turned up another, on a method with no race at
  all. Both were corrected. Neither module was given machinery it did not need.
- **D7** ~~The OpenAPI text claimed `q` searches title and handle; it searches
  title only.~~ Fixed 2026-09-05.
- **D10** ~~Nothing stopped a module's SQL from naming another module's
  table.~~ **Closed 2026-09-05.** `TestModuleSQLNamesOnlyItsOwnTables` derives
  the ownership map from the migrations — 72 tables over 17 modules, and
  `sales_channel` turns out to be owned by AUTH, not by product — then reads USE
  from THREE surfaces: the query files, the module's OWN migrations (a data
  backfill would otherwise have a whole directory to hide in) and Go string
  constants, folded through literal + same-package-constant concatenation so
  that the hand-written channel-scoped query is actually seen. The tree passes
  today with an EMPTY exemption ledger: there is no cross-module SQL read in
  this repository, and the rule was not weakened to be able to say so. The
  violation was invisible by construction because it WORKS — every module is
  handed the same pool — and would only surface the day a module moved to its
  own database. Two holes were found while proving the gate, before it ever ran
  in anger: the table extractor treated `INSERT INTO price (…)` as a function
  call and swallowed the target, so it would have been blind to cross-module
  WRITES, the loudest half; and its constant-fold depth limit was guessed at 12
  while the deepest real chain is 23, so it had been truncating expressions and
  reading queries with the end cut off. Reading a link table stays legal by
  construction rather than by exemption — `core/link` creates those at runtime,
  so no migration owns them, and a control test asserts the link table is NOT
  reported. Remaining hole, named and not closed: plugin-owned tables are owned
  by nobody, so a module reading one would pass.
- **D11** ~~`make load-test` brought up the containers, printed a green line and
  measured NOTHING.~~ **Fixed 2026-09-05.** The recipe's `-run` selector named
  `TestTemelYukAltindaDogruKalir`, a name that exists nowhere in the repository:
  the test was translated, the Makefile was not. `go test` answers a selector
  that matches nothing with "no tests to run" and **exit code 0**, so the target
  was green, slow and empty — reproduced verbatim before the fix. The selector
  now names `TestStaysCorrectUnderBaselineLoad`, and the class is closed by
  `TestEveryRunPatternInABuildFileNamesARealTest`: every `-run` pattern in the
  Makefile and in the CI workflows has to name a real test, benchmark, fuzz
  target or example. The one accepted exception is the empty selector paired
  with `-bench`, which is how the benchmark targets say "no tests, only
  benchmarks"; `-benchmem` alone does not buy it.
- **D12** ~~The panel's product list makes "the same Graph call the storefront
  listing uses, so the screen cannot drift".~~ **Corrected 2026-09-05, and the
  false sentence was hiding a live gap.** The two were never the same call: the
  storefront goes to the product module's store listing, the panel goes to the
  read layer's `product` provider, and the provider takes `status`, `handle`,
  `collection_id` and `id`/`ids` while the storefront listing takes
  `collection_id`, `category_id`, `tag_id` and a text search. So the operator
  cannot narrow the catalog by category while the shop's customers can. The
  godoc in `internal/adminui/catalog.go` now says what is true; the gap itself
  is B2's, and the fix is to teach the provider the two taxonomy filters rather
  than to give the panel a module import.

  **And the gap it uncovered is closed too, on the same day.** The fix was the
  one named above and not a module import: the provider learned `category_id`
  and `tag_id`, the product module added a `category` entity so the screen can
  offer NAMES instead of identifiers, and the panel's product list has a
  category dropdown whose choice travels in the query string. The panel still
  offers no tag control and no search box, and those two absences are NOT the
  same thing — the tag control is a decision (a tag is free text, and a
  dropdown is the wrong shape for it), the search box is still blocked, because
  the provider has no text filter while the storefront listing does.

  **The second of those two was closed hours later, on the same day.** The
  provider learned `q`, the panel grew the search box, and the two controls are
  ONE form so that "search inside a category" is a single request. The tag
  control stays absent and stays a decision. What the search cost is no longer a
  guess either; the numbers are in `docs/catalog-search-cost.md` and their
  consequences are in the B2 section below.
- **D13** ~~The catalog every performance sentence in this repository is
  measured against existed as rows in ONE Docker volume, and nothing here could
  rebuild it.~~ **Closed 2026-09-05, and it was a real gap rather than an
  inconvenience.** No seed file, no seed target, no seed program; the compose
  file never creates that database at all, so a fresh machine had no rig and no
  way to get one, while **28 non-test files carry timing figures that rest on
  it.** `docker compose down -v` was the whole distance between a measured claim
  and unfalsifiable prose — that is, the repository's loudest rule, that a
  performance sentence is never written unmeasured, depended on a volume nobody
  had promised to keep.

  The rebuild is a subcommand of the server binary (`internal/rig`, reached as
  `make seed`) and NOT a `.sql` file, because the schema has to come from the
  modules' own migrations: three of the tables the rig needs
  (`link_product_variant_price_set`, `link_product_variant_inventory`,
  `link_product_sales_channel`) appear in no migration at all — `core/link`
  creates them at run time — so a plain `psql -f` would die on the first link
  insert. **13.6 s** for the full catalog out of `generate_series` in one
  transaction, ids derived from the row number so a second run inserts nothing,
  and the acceptance test is a PLAN rather than a stopwatch: a rebuilt rig
  reproduces the count query's recorded plan to the buffer — 52,004 rows, 52,004
  subplan loops, Heap Fetches 0, 156,743 shared hits of which 156,013 the
  subquery's.

  Two findings came out of the rebuild itself. `ANALYZE` alone is not enough:
  without the VACUUM half the visibility map is unset everywhere, the same
  statement reports **52,000 heap fetches and 208,742 buffers** instead of 0 and
  156,743, and every reader comparing a fresh measurement against a godoc would
  have been comparing two different databases without being told. And the
  surviving rig's schema is **five order migrations, one payment migration and
  one product migration behind** the repository, missing the invoice, job,
  outbox and audit schemas entirely — the drift a hand-written seed file would
  have institutionalized.

  Written down rather than hidden: the reset is slow, **4 m 31 s to delete what
  13.6 s wrote**, because a referential-integrity check will not use the
  modules' PARTIAL indexes and falls to a sequential scan per deleted parent.
  Remaining hole: the seeder's bulk SQL names other modules' tables, and neither
  SQL gate reads it — both walk `internal/modules` only (see D10). What catches
  a column a migration renamed is the load fixture below, which runs the same
  generator on every integration run.

  **Used in anger the next day, and it did the two things it was built for.**
  The rebuild seeded in 13.35 s (17.3 s of wall clock including migrations and
  module bootstrap; 11.91 s on the other container), reproduced the recorded
  plan to the digit — 52,004 rows, 52,004 subplan loops, Heap Fetches 0, 156,743
  shared hits of which 156,013 the subquery's — and, because it seeds twenty
  categories and twenty tags, **made the taxonomy filters MEASURABLE for the
  first time.** They had never been measured: the surviving rig has zero rows in
  both map tables, so every category figure this file carried was a floor. The
  numbers replaced that floor, the floor turned out to be wrong in both
  directions, and the filter body changed as a result (see B2 above and
  `docs/catalog-search-cost.md`). No reset was needed for any of it — a fresh
  scratch database beats `rig.Reset` by two orders of magnitude, which is the
  practical form of the 4 m 31 s recorded above.

  **And the rebuild made a class of claim falsifiable that had not been.** Six
  performance sentences written against the unreconstructable volume were re-run
  against a rig anybody can rebuild; two reproduced, one reproduced structurally,
  and three did not (D15). That is the gap D13 was really about, arriving as a
  number: it was not that the rig might be lost, it was that nothing could be
  checked while it was the only copy.

  **The one limit found by using it.** The default shape is PERFECTLY UNIFORM —
  every category holds exactly 2,600 products and so does every tag, by
  construction — so the case that decided the B2 change, a category holding a
  handful of products, cannot be asked for. `rig.Spec` takes a category COUNT
  and has no skew option. The selective categories were hand-built on a scratch
  database and are gone with it.
- **D14** ~~`make load-test` measured a catalog of ZERO products.~~ **Fixed
  2026-09-05, and it is D11 one layer down.** D11 made the target run its test
  again; this is what the test then did. The harness creates regions, tax
  fixtures, an identity and one stock location and NOT ONE PRODUCT, and the
  target selects this test alone, so no other file's fixtures ran either: the
  storefront listing returned an empty page, the count query counted nothing,
  and the target printed a green requests-per-second line. The class is the same
  as D11's — a check that sees nothing is indistinguishable from a check that
  passes — but the emptiness was in the DATA, where no selector gate can look.
  The test now builds 200 products through the same generator that rebuilds the
  rig, mints its own sales channel so the fixture cannot leak into the other
  storefront scenarios, and counts a 200 whose body carries no product as a
  failure.
- **D15** ~~The product module's performance figures were true when they were
  written and nobody could check whether they still were.~~ **Re-measured and
  corrected 2026-09-06: of eight recorded figures, three reproduce and FIVE DO
  NOT.** This is the first correction D13 paid for — a rig anybody can rebuild
  turns a recorded figure into a falsifiable one, and the very first re-run of
  the set found the majority of it had drifted. The three that survive are all
  STRUCTURAL (plan node, loop count, rows removed); every one of the five that
  failed is a millisecond. Checked on the rebuilt rig and on a cluster that
  passes the case-folding probe:

  **Reproduced exactly.** The count's recorded plan — 52,004 rows, 52,004
  subplan loops, Heap Fetches 0, 156,743 shared hits of which 156,013 the
  subquery's — in every run, on both the old and the new statement shape. Its
  millisecond is a machine figure rather than a wrong one and was not struck.
  And the rejected `OR IS NULL` cursor bound's `Rows Removed by Filter: 50001`,
  exact, with only the accompanying millisecond nudged.

  **Direction right, both magnitudes wrong.** "Two EXISTS 26.8 ms against a
  single `bool_or` 0.8 ms" was the argument for the visibility rule's shape. The
  MECHANISM reproduces verbatim — both sublinks become a full sequential scan of
  the link table before the first row leaves the plan — and neither number does:
  measured 21.4 ms against 0.12 ms. The likeliest reading is that the pair was
  never read off one clock, 0.8 ms having the shape of a client round trip and
  26.8 ms the shape of an `EXPLAIN`. That is the defect worth naming, because it
  is the one that let a 0.8 ms stand for months beside a query costing 0.12 ms:
  **both halves of a comparison have to come from the same clock.**

  **Four claims false outright and one reproducing beside them**, all in the
  rejected-alternatives block that argues why the count is not rewritten as a
  hash join. Reproduces: the two-EXISTS band, 43-54 ms, measured at its ceiling.
  Does not: the hash form's band was 33-45 ms and measures 42-48; "twice as fast
  unfiltered" is between 1.35x and 1.6x depending on the batch; its "fixed
  ~30 ms floor" is about 39 ms; and the selective-filter pair "13.8 ms against
  30.0 ms" measures 20.3 against 39.0. The ARGUMENT survives all four, and that
  is why they are corrections rather than a reversal: the claim was that the
  trade CHANGES DIRECTION — the hash shape is faster with nothing filtered and
  slower the moment a criterion is selective — and it still does.

  **Why they were uncheckable, stated so the class does not come back.** Not one
  of the eight carried a date, a cluster, or a `plan_cache_mode` setting, and the
  database they were taken on could not be rebuilt (D13). Two of them were also
  taken on a cluster whose data directory was `initdb`'d with a C locale and
  which therefore FAILS `core/db/casefold.go` — where an uppercase Turkish
  letter does not `ILIKE`-match its own lowercase form, so those figures
  describe a search that returns nothing at all for a word carrying one. The
  same bare `ILIKE` count costs 8.7 ms there and 14.5 ms where the folding
  works, 1.66x. The corrections landed at their source, struck in place rather
  than deleted, with the bench and the plan-cache setting named
  (`internal/modules/product/repository/saleschannel.go`,
  `internal/modules/product/service/service.go`); the full record is
  `docs/catalog-search-cost.md`.
- **D16** ~~**The column audit names D4 as the thing it catches. It never caught
  it.**~~ **Fixed 2026-09-06.** Two of the three holes below are closed and each
  was proved by planting a mutation and running BOTH gate versions against it;
  the third is measured and deliberately left open; and a FOURTH nobody had
  named turned up while the first three were being fixed. The record of the
  finding is kept below because the fix is only as trustworthy as the account of
  what was broken. Measured 2026-09-06 while closing D4, and this is the entry
  on this page with the most credibility riding on it, because the audit is one
  of the six gates the house rules lean on: "adding a column obliges you to
  write it".
  `TestEveryColumnIsWrittenBySomething` was GREEN at the start of that session
  while `order_exchanges.completed_at` and `canceled_at` were provably unwritten
  and NOT in the exemption list. `internal/arch/columns_test.go`'s own godoc
  says, in the paragraph explaining what the defect looks like, that
  order_exchanges keeps both columns and nothing writes either. It says so
  while passing.

  Three holes, each measured rather than reasoned:

  - **The written set is keyed by bare column NAME, module-wide — not by
    table.** `completed_at` counted as written because `CompleteOrderClaim`
    sets it on a DIFFERENT table, and `canceled_at` because three other queries
    do. Every module with a repeated column name has the same blind spot, and
    timestamp names repeat by design.
  - **Only a `CREATE TABLE` body is read for declarations.** A column added by
    `ALTER TABLE ... ADD COLUMN` — how EVERY migration after the first adds one
    — is invisible to the scan. Proved by mutation both ways: an unwritten
    column added by `ALTER` leaves the gate green; the same column moved into
    the initial `CREATE TABLE` fails it with the right message. So the house
    rule holds for the initial schema and not for anything added since,
    including D5's `archived_at`.
  - **It reads `.sql` text, not the call graph.** A write query that no
    reachable route or workflow calls turns the gate green while no operator
    can produce the write. That is why D4's cancel was bound to a route in the
    same change rather than shipped as a service method.

  And a consequence worth stating because it was the recorded plan: the third
  option for D4 — "record them in the exemption list with the reason" — was
  mechanically IMPOSSIBLE. The exemption is consulted only after the
  written-set guard, and the test's closing loop asserts every exemption key
  was actually found unwritten, so the entry would have failed with "is exempt
  in unwrittenColumns but is NOT unwritten any more."

  The gate is green today with no lie in it and no new exemption, but it is
  green for the order module by construction rather than by proof. ~~Fixing the
  audit is `internal/arch`'s own budget and was not touched.~~ It was touched
  the next day, and the rest of this entry is what that found.

  **What closed the first two holes.** The written set is keyed by TABLE and
  column now — an INSERT's column list belongs to the table it inserts into, a
  `SET` to the table the statement updates, and an `ON CONFLICT DO UPDATE SET`
  to the table of the INSERT it hangs off. And the declaration scan REPLAYS the
  migrations in file order: `CREATE TABLE`, `ALTER TABLE ... ADD COLUMN`,
  `DROP COLUMN` and `DROP TABLE` are applied in turn, so the audit sees the
  schema the database ends up with rather than the one the first migration
  described. The visible column count went from 715 to 718 — exactly the four
  columns added by `ALTER` since the initial schemas, minus the one dropped
  (`order_exchanges.completed_at`). Both proofs were run against the old gate as
  well as the new one, on the same planted mutation: a column added by `ALTER`
  and never written, and a column whose name is written on a SIBLING table, each
  leave the old gate green and fail the new one by name. An audit that is only
  shown failing on the thing it was just taught has proved nothing about
  yesterday.

  Two `ALTER` actions the replay does NOT model — `RENAME COLUMN`, and
  `ALTER COLUMN ... SET DEFAULT`, which moves a column out of this audit's scope
  entirely — are REPORTED as findings rather than skipped, and a control plants
  both. Skipping what it does not understand is precisely how this gate came to
  carry its own counter-example in its documentation, so the replay now fails
  loudly and asks to be taught. The order the replay depends on is checked too:
  it holds only while every migration prefix is padded to the same width, and
  one file numbered `10_` beside `000009_` would apply an `ALTER` sequence in
  the wrong order without a sound.

  **The fourth hole, which was on nobody's list.** The `CREATE TABLE` reader
  recognised a column by an ALLOW-LIST of nine types and split the table body by
  LINE. A column declared with any other type — `uuid`, `date`, `varchar`,
  `interval` — would have gone unaudited forever, and a multi-line CHECK
  constraint produced seven column-looking lines that failed to become false
  findings only because their leading words happened not to be in the type list.
  It is a closed deny-list of the seven table-constraint keywords now, with
  depth-aware comma splitting: an unknown type is a COLUMN, not a blind spot.
  Two positive controls pin the reading and the decision, and half of the
  planted cases are REFUSALS — `FOR UPDATE` writes nothing, an INSERT with no
  column list names nothing, SQL inside a comment is prose and SQL inside a
  string literal is data.

  **The third hole is still open, and that is now a measurement rather than an
  omission.** Of the 464 sqlc queries under `internal/modules`, 9 have no
  hand-written caller anywhere in `internal` or `core` — and all nine are
  SELECTs. So reading text instead of the call graph masks exactly ZERO columns
  today: every INSERT and every UPDATE in this repository has a caller. The
  cheap fix, requiring a write statement to have a hand-written caller, moves
  the boundary one hop and stops, because the caller can itself be unreachable —
  which is precisely the shape D17 had. Closing it honestly means reachability
  from the real entry points over the 317k lines of `internal` and `core`, and
  `golang.org/x/tools` is an indirect dependency of this module today. The
  measurement sits in the gate's own godoc, so the test no longer claims more
  than it does.

  **And the fix produced nine live findings on its first run** — columns this
  repository believed were covered and are not. They are D18.
- **D17** ~~**`CancelReturn` and `CancelClaim` have no production caller**~~
  **Fixed 2026-09-06, and the product decision this entry parked turned out to
  have been made already — three times, inside the module.** Measured 2026-09-06
  by grep across every production tree while closing D4. No HTTP route, no
  workflow, no interop method reached either. So `order_returns.canceled_at` and
  `order_claims.canceled_at` were, in production, exactly as unwritten as the
  exchange's was; the column audit is green on them only because the `UPDATE`
  text sits in a `.sql` file (D16, third hole). ~~Same defect class as D4,
  currently undetected, and left open deliberately — it is a product decision
  about who may withdraw a return, not a correction.~~

  **What closed it.** Two admin routes —
  `POST /admin/v1/orders/{id}/returns/{returnId}/cancel` and
  `POST /admin/v1/orders/{id}/claims/{claimId}/cancel` — both on the write
  scope, both going straight to the service rather than through a flow. That
  last part is the same argument the exchange's cancel makes and it holds for a
  narrower reason than it looks: RECEIVING a return reaches inventory, so that
  one goes through a flow, while withdrawing an unreceived request puts no stock
  back and moves no money. The received case is not a hole in the argument, it
  is refused by the transition table.

  **The alternative was deleting the two methods** on the product argument that
  a return is simply never taken back, and three things already in the module
  refused it. The quantity rule has a RELEASE VALVE that could never fire:
  `SumReturnedQuantities` excludes canceled returns on purpose, so one request
  for a whole line consumed that line's returnable quantity FOREVER — and the
  storefront endpoint opens a request knowing nothing but the order id.
  `ReturnStatus.CancelAction` and `ClaimStatus.CancelAction` are transition
  tables whose other rows exist only to guard a transition nothing could reach.
  And the timeline already publishes a return-canceled entry kind that no row
  could ever carry. The claim half was worse than symmetric: settling refuses
  anything but a claim in status requested AND of type refund, so a claim opened
  against the wrong order, opened twice, or withdrawn by the customer had NO
  exit at all and stayed on the list of things the shop still owes.

  **Proved where the rule actually lives.** Two integration tests against a real
  database rather than a fake store, because the release is a `WHERE` clause a
  fake cannot disagree with: a withdrawn return gives its units back — the
  second request for the same line is refused before the withdrawal and accepted
  after — and a withdrawn claim keeps the FIRST moment when the operator clicks
  twice, while a settled claim is refused with a conflict. Both read the stamp
  back with a second query, since a `RETURNING` clause can report a value the
  row does not keep.
- **D18** **Nine columns that nothing has ever written, and they were invisible
  until the audit meant to find them was fixed.** Surfaced 2026-09-06 the moment
  `TestEveryColumnIsWrittenBySomething` began keying its written set by TABLE
  instead of by bare column name (D16, first hole). All nine are `deleted_at`
  and all nine have the same shape: the module soft-deletes SOME of its tables,
  those writes covered this table's column under bare-name matching, and the
  gate stayed green on a column that is NULL on every row that has ever existed.

  | module | tables |
  | --- | --- |
  | product | product_category, product_collection, product_tag, product_option_value |
  | region | country, currency |
  | inventory | stock_locations, inventory_reservations |
  | fulfillment | fulfillments |

  What they cost is not storage. Every read of those tables carries
  `deleted_at IS NULL`, a predicate that has never once been false — the same
  finding D9 records for order and payment, with one difference that is the
  whole point of this entry: D9's ten are a DECISION and these nine are not
  decided at all.

  **Their exemptions were PLACEHOLDERS for unclosed work, not decisions, and the
  gate said so in the entries themselves.** Each of the nine opened with the
  literal words "UNCLOSED FINDING, not a decision" and named what had not been
  answered: whether a category should be deletable at all, whether closing a
  stock location is a delete or a status, whether a reservation released by
  `SetReservationStatus` needs a second way of saying what its status already
  says, and whether the seeded country and currency lists — whose rows are
  re-pointed rather than removed — should carry the column at all. They existed
  only so the gate could be green on the rest of the repository while these were
  worked, and nothing was changed in the four modules on the day they were
  found: a schema change belongs to whoever answers the question, not to the
  audit that asked it.

  **Answered 2026-09-06, and the nine did not have one answer. That is the
  finding.** Eight are closed and the closings go in OPPOSITE directions: four
  columns were missing their WRITE, and four were the wrong column. Treating the
  nine as one batch would have produced either four schema changes that destroy
  a legitimate delete path or four deletes nothing should ever have. The ninth
  is still open, and its exemption no longer merely admits that a question
  exists — it states the question.

  | column | outcome |
  | --- | --- |
  | product `product_collection.deleted_at` | **FIXED — the write was missing.** `Service.DeleteCollection`, bound to `DELETE /admin/v1/product-collections/{id}`. It releases the collection's products in the SAME transaction: a product points at its collection with a column of its own, that column's ON DELETE SET NULL cannot fire against a soft delete, and the storefront listing filters on the id WITHOUT joining the collection — so an old link would keep serving the products of a collection the merchant can no longer see |
  | product `product_category.deleted_at` | **FIXED — the write was missing.** `Service.DeleteCategory`, and a node with children is REFUSED rather than resolved for the merchant. The tree is walked downwards from the root, so orphaned children do not merely dangle: the whole subtree disappears from every listing while its rows stay live. Re-parenting onto the grandparent and clearing the children's parent were both rejected in writing, the second because it promotes a subtree to the TOP LEVEL of the storefront menu |
  | product `product_tag.deleted_at` | **FIXED — the write was missing.** `Service.DeleteTag`, with NO guard, and the difference from the other two is argued rather than assumed: a tag is a label, nothing in the catalog is structured by it, and every read of a product's tags already filters the join. The partial unique index that frees a deleted value for reuse had never once been exercised |
  | product `product_option_value.deleted_at` | **FIXED — the write was missing, in both halves.** `Service.DeleteOptionValue` refuses a value a LIVE variant carries, because the variant read joins the value with a liveness filter: deleting one in use fails silently and shows the variant FEWER options than it has, so two variants differing only in that option become indistinguishable on the page and in the data. And `Service.DeleteOption` now stamps its values in the same transaction — leaving them live is how this column came to be the only child rows in the module that outlived their parent |
  | fulfillment `fulfillments.deleted_at` | **DROPPED — the column was wrong** (`internal/modules/fulfillment/migrations/000003_fulfillments_are_never_deleted.up.sql`). A shipment is the record of something that happened and is retired by a STATUS with its own stamp; the head of the module's first migration already made exactly that argument for the shipment's LINES and never applied it to the shipment |
  | inventory `inventory_reservations.deleted_at` | **DROPPED — the column was wrong** (`internal/modules/inventory/migrations/000002_reservations_are_never_deleted.up.sql`). The table's own rule, written at the head of its first migration and repeated over its query file, is that a reservation is never deleted. The column is a second way of saying what the status says, and the two can disagree; worse, making the release a delete collapses "already released" and "never existed" into one answer, which is precisely the distinction the checkout saga's idempotent compensation stands on |
  | region `country.deleted_at` | **DROPPED — the column was wrong** (`internal/modules/region/migrations/000003_reference_data_is_not_soft_deleted.up.sql`). Reference data whose rows are written by a seed migration; the only writes that exist move a country BETWEEN regions, and that is not a deletion. It would also be actively harmful if anything ever set it: a stamped country resolves to no region forever, and checkout there stops with nothing in the catalog or the region list to explain why |
  | region `currency.deleted_at` | **DROPPED — as country** |
  | inventory `stock_locations.deleted_at` | **STILL OPEN — and the exemption now STATES the question instead of admitting there is one.** Measured 2026-09-06: writing the column is not enough, because the availability sums read the level rows with no join to the location at all, so a soft-deleted location would keep selling its stock while vanishing from the operator's screen and the reserve path would still hand it out. A hard delete is worse — the level and the reservation both reference the location ON DELETE CASCADE, so it would destroy the stock rows and the reservation history the module says must never be deleted. The location also has no UPDATE path, so a warehouse that closes cannot even be renamed. THE QUESTION is therefore not "delete or status" but what a closed location owes: whether its levels move, are zeroed or are only excluded from availability, and what happens to the reservations still active there. The column stays until that is answered because it may yet be the right carrier |

  **The three drop migrations had to rebuild indexes, and one of them was
  load-bearing.** PostgreSQL drops any index whose PREDICATE names a dropped
  column, silently and with no notice, and every index on these tables was
  partial on `deleted_at`. The migrations record measuring that against a real
  PostgreSQL before they were written — a probe table with a UNIQUE partial
  index accepted a duplicate key one statement after the drop. On `fulfillments`
  the index at stake is the idempotency guard that stops a retried saga step
  from producing A SECOND SHIPPING LABEL, so dropping the column without the
  rebuild would have removed the guard and left the schema looking untouched.
  Three tests pin the rebuild against the real database rather than the
  migration text: `TestTheIdempotencyGuardSurvivedTheDroppedColumn`,
  `TestDroppingTheColumnDidNotTakeTheReservationIndexesWithIt` and
  `TestDroppingTheColumnDidNotTakeTheCountryIndexWithIt`. One index was renamed
  rather than recreated under its old name: a name saying "alive" over an index
  that no longer has a liveness predicate is the next reader's wrong assumption.

  **What the exemption list looks like now.** Eleven entries: D9's ten, which
  are a DECISION, and the one open question above. The words "UNCLOSED FINDING"
  appear nowhere in it. The three migrations also say, each in its own words,
  why their conclusion differs from D9's even though the ARGUMENT is the same —
  a record of something that happened is kept, and the retreat from it is a
  status. D9's ten carry no rule that the column can break; `fulfillments`
  carried one, because its uniqueness index was written as "unique among LIVING
  rows", so while the column stood there was a way to write a second live
  shipment against the same idempotency key by stamping the first.
- **D19** ~~`Service.SetPassword` read the user in one statement and wrote its
  identity in another; a delete landing in the gap burned an e-mail address
  permanently.~~ **Fixed 2026-09-06.** Found by asking D6's question of `auth`.
  The service called `GetUser` — its own autocommit statement — and then
  `SetPasswordHash`, its own transaction. A concurrent `DeleteUser` soft-deletes
  the user AND its identities, so the write found no identity and INSERTED one:
  a LIVE identity under a deleted user. The foreign key cannot object, for the
  reason that runs through this whole class — a soft delete is an UPDATE, and
  `auth_user`'s row stays physically in place, so CASCADE never fires.

  **The consequence is not a security consequence, and that was measured rather
  than assumed.** A deleted administrator cannot log in through the orphan:
  `Login` reads through `GetUserByEmail` and token verification through
  `principalFromToken`, and both read the LIVE user first.
  `TestARevivedIdentityCannotLogIn` manufactures the orphan with raw SQL and
  confirms the refusal. What it actually costs is smaller and permanent: the
  deleted user's address stays in `auth_identity_provider_uniq` forever, so
  opening a NEW administrator at that address fails with a conflict while the
  user list shows the address as free — and there is no repair path, because
  `DeleteUser` on an already deleted user returns NotFound.

  A second variant of the same gap was produced with an `UpdateUser` in place of
  the delete: the INSERT then wrote the OLD address into `provider_identity`,
  which is the precise divergence `SyncIdentityProviderIdentity` exists to
  prevent.

  **The fix moved the read INTO the write.** `LockLiveUser` (`FOR SHARE`) is now
  the first statement of `Repo.SetPasswordHash`'s transaction, and the identity's
  `provider_identity` comes from THAT row rather than from a parameter — so
  liveness and address have one source and cannot disagree about the instant they
  describe. The service makes exactly one repository call and the
  `providerIdentity` argument is gone from the signature. Tax's exported
  `WithTx` was rejected with a reason: it is the right answer when the SERVICE
  has to decide something from the locked row, and here it did not — it needed
  only "is it alive" and "what is its address", both of which belong to the
  write. It would also have put transactions on the service's own repository
  interface, so every fake in the module would have had to imitate one.
  Stripping `FOR SHARE` from `LockLiveUser` makes the new test fail with exactly
  the orphan it was written to prevent.

  Pinned in passing: the failed-login counter's `FOR UPDATE` was TRUE and held
  by nothing — no test would have noticed its removal.
  `TestTheFailedAttemptCounterLosesNoIncrement` holds it now: with the lock
  stripped, twelve concurrent increments arrive as eight.

  **Still open, measured on the same day and deliberately not closed:**
  `LinkSalesChannel` locks the CHANNEL and not the KEY. A `DeleteAPIKey` landing
  between the service's `GetAPIKey` and the link write leaves a link row for a
  key that delete had just unlinked; it was produced, and it is unreachable —
  every road to it reads the live key first. Closing it means a second
  `FOR SHARE`, on `api_key`, taken BEFORE the channel's, because `DeleteAPIKey`
  walks the key and then the link rows and any flow taking the channel first
  would close a waiting cycle. That is a new lock-ordering constraint bought for
  a row nobody can read. Named here so it is not re-found as news.
- **D20** ~~Three godocs in `promotion` said orphaning a rule was "structurally
  impossible". It was measurably possible.~~ **Fixed 2026-09-06 — and the
  consequence is the weakest class this file records, which is the honest half
  of the entry.** Found by asking D6's question of `promotion`:
  `AddPromotionRule` and `SetApplicationMethod` each did `GetPromotion`, decided
  the promotion was alive, and then wrote — two separate autocommit statements.
  Both child tables reference `promotion` ON DELETE CASCADE, and a soft delete
  never fires it.

  **The race was produced, not argued.** The harness runs the real soft-delete
  statement in an uncommitted competing transaction and then confirms with
  `pg_blocking_pids` that the write is waiting on THAT session — so the decision
  was provably already made. Against the original code neither write waited, and
  each left one orphan row.

  **What the orphan does is nothing, and measuring that is what keeps this entry
  honest.** `ComputeDiscounts` returns a `DiscountTotal` of zero with the code in
  `UnmatchedCodes`; `LookupStoreCoupon` answers `promotion_not_usable`;
  `ListPromotionRules` 404s — every reader filters the promotion's own
  `deleted_at`. No customer is charged the wrong amount by either race. The two
  readers that DO return the orphan, `Service.GetPromotionRule` and
  `Service.GetApplicationMethod`, have no HTTP route at all. So the defect was
  never the row: it was the SENTENCE, three of them, claiming a protection that
  did not exist — the identical failure this file records against tax in D6. The
  code was made true rather than the sentences softened only because it cost
  about thirty lines of machinery the module already had; had it been expensive,
  rewriting the sentences was the correct answer.

  The fix is `LockPromotionShared` (`FOR SHARE`) plus `requireLivePromotion`, as
  the first step of the repository's own transaction. Shared and not exclusive
  for tax's reason: two administrators adding rules to one promotion must not
  serialize, while a soft delete is a plain UPDATE taking a lock that `FOR SHARE`
  DOES conflict with. The transaction stays inside the repository — there is one
  write per method, so unlike tax no service-visible surface changed. Pushing
  the liveness test into the write itself was rejected in writing: it works for
  the rule insert and CANNOT work for the method's upsert, whose conflict branch
  updates a row without being able to see the promotion table at all — so the
  two siblings would have ended up with different guarantees.

  Three things were found while proving it. The first harness was too weak and
  was thrown away: it used `SELECT … FOR UPDATE` as the adversary, which
  conflicts with every row lock, so the test would have stayed green even if the
  fix had taken the wrong one. Both in-memory fakes were modelling the bug —
  they accepted a rule for a promotion that did not exist, which is why no unit
  test could ever have caught this — and teaching them the contract immediately
  failed `TestHataSiniflandirmasiStatusKodunaCevrilir`, which had been asserting
  404 while the fake returned 200. And `DeletePromotion`'s claim that its
  children "become unreadable" is false independently of any race:
  `GetApplicationMethod` returns the method of a deleted promotion, because that
  query filters only the method's own `deleted_at`.

  **Flagged and not acted on:** `Service.GetPromotionRule` is a published
  capability with no consumer anywhere — no route, no in-module caller, only the
  interface entry and the fakes. That is this repository's named most expensive
  recurring defect (ADR 0009), and deleting a public service method is not a
  call to make inside a concurrency brief.
- **D21** ~~**A job that succeeds cannot say anything.** The detail column of the
  `gobit jobs` listing is reachable only through an ERROR, so the one channel
  that reaches an operator is the one that also raises an alarm.~~ **Fixed
  2026-09-06, and the premise was MEASURED before anything was built rather than
  taken from the brief.** A throwaway probe against the real runner produced all
  three cases: a successful run left the detail blank, a failing run whose error
  carried a detail printed it, and a failing run whose error did not left it
  blank. `Outcome.Detail` was seeded from the error and from nowhere else, under
  a guard that only runs when the error is non-nil. So a pass that had something
  to REPORT and nothing to complain about could only speak to the log.

  The fix is a run-scoped reporter carried on the context: `WithReporter`
  installs one, `Report` writes a line into it, and the runner seeds
  `Outcome.Detail` from it. It is purely ADDITIVE — no signature changed, the
  job definition grew no exported field, and the contract published the day
  before was not touched.

  **Two alternatives were rejected and the first was rejected by measurement.**
  Widening the job body to return a string beside its error is dead on arrival:
  `TestEveryJobDefinitionFieldReachesAPluginJob` requires every exported field
  of the scheduler's own definition to have a CONVERTIBLE twin on the published
  `plugin.Job`, whose work field is a function returning only an error, and a
  reflection probe reports the two function types as non-convertible in BOTH
  directions. The option could therefore only be taken by breaking a contract
  published one day earlier or by weakening the very gate that guards the copy —
  a large bill for handing three in-repo jobs a string. The second, an interface
  a successful result may implement, is unbuildable rather than expensive:
  success is a nil error, and nil implements nothing; making it work means
  returning a non-nil error that is not a failure, after which every error check,
  the outcome column and the listing's failure prefix all have to learn which
  errors are not failures.

  **The cost is paid openly.** A context channel hides itself: nothing in a
  job's signature says it may report. That is mitigated three ways rather than
  denied — the job body's godoc names the function, all three in-repo jobs call
  it so the pattern is readable in the repository, and a call made outside a run
  is a silent no-op, so the cost of not knowing is a missing line and never a
  panic.

  **Precedence keeps the change invisible to everything that already worked.**
  An error carrying its own detail still OVERRIDES anything reported mid-run, so
  a failing run reports exactly what it reported before; a mutation flipping the
  two fails exactly one test. The one genuinely new failing-run behaviour — a
  run that fails WITHOUT a detail of its own keeps whatever it last reported —
  is reachable only from code that calls the new function.

  **It shipped with three consumers, because a capability with no consumer is
  this repository's named most expensive defect.** The outbox relay reports its
  pass throughput, and that is what the blank cell was hiding: an idle minute, a
  relay keeping up, and a relay filling its batch limit every minute with a
  growing backlog were three different facts printed identically. The payment
  reconciliation pass reports what it examined, agreed, found DIVERGENT and
  could not reach; the saga watch reports the abandoned count. All three
  previously had findings that reached the log and nothing else. The relay's
  dead-letter pile still FAILS its run — re-decided rather than inherited, and
  written into the package doc, because outcome is the column an operator scans
  while detail is read afterwards, and anything watching that job's outcome
  would have stopped firing the day a demotion shipped.

  The chain is proved link by link where each link lives: report to outcome
  against the real runner, outcome to the stored row and back against a real
  PostgreSQL, and stored row to printed listing in the composition root.

  **Found while in the files, reported and not built:** `Runner.RunNow` has no
  caller outside its own tests, and its godoc claimed it is what `gobit job run`
  calls. There is no such subcommand — the binary dispatches help, migrate,
  stuck, recover, jobs, deadletters and seed. `Definition.Every` offered the
  same nonexistent command as the escape hatch for calendar scheduling. Both
  sentences now state the gap instead of asserting the feature.

  **Still open, and it arrived the same day:** a PLUGIN cannot report a detail.
  The published job contract's work field takes a context and returns an error,
  and the reporter lives in an internal package a third-party plugin cannot
  import — while the runner's decorated context is already handed to the
  plugin's body, so the value is right there and only the key is unreachable.
  The live example is `plugins/webhookout` (C5): its delivery pass reports its
  throughput and its per-pass limit to the LOG, and reaches the listing only by
  FAILING. The smallest closure is a published leaf package owning the context
  key with the internal one re-exporting it, which adds three identifiers to the
  compatibility promise against ADR 0026's anticipated price of publishing the
  whole scheduler. Rejected in writing: having the scheduler import the plugin
  surface instead, which is legal and inverts the dependency, and wrapping the
  plugin's body at the composition root, which cannot invent a channel the
  plugin can speak into.
- **D22** **A plugin can be written, documented, tested end to end and audited,
  and still be impossible to install.** Measured 2026-09-06 while recording C5.
  `plugins/webhookout` compiles, carries unit, integration and end-to-end tests
  that were observed passing, and is described at length in the environment
  example under the name `webhook-out`. It is absent from the composition root's
  plugin catalog, which is the only map the installer consults, so
  `PLUGINS=webhook-out` stops the boot with "unknown plugin". The measurement is
  `go list -deps ./cmd/server`: the binary's dependency closure names eight
  plugins and not this one, so the package is not compiled in at all and its
  migration is outside the migrate surface too.

  **The gate built for exactly this class is green, and the reason is worth more
  than the finding.** `TestThePluginNamesInTheDocsAreReal` has a reverse
  direction whose godoc says it exists because "a plugin that is written but
  announced nowhere is a capability without a consumer". Both of its directions
  pass here, because it derives the set of registered names by PARSING the name
  constant out of every file under the plugins tree rather than from the
  catalog. So the forward direction asks whether a documented name is declared
  somewhere, and the reverse asks whether a declared name is mentioned in the
  document; neither asks the question the operator asks, which is whether the
  binary knows the name. The consequence is the sharpest possible form of it:
  the environment example now carries a copyable line naming this plugin, and
  copying it produces precisely the startup failure the gate's own godoc
  describes itself as preventing.

  **Both fixes landed 2026-09-06.** The map entry is in, and the gate that was
  missing is `TestEveryPluginIsInstallable`: it reads one side from the plugins
  tree, as before, and the OTHER side from the composition root's catalog,
  resolving each key through that file's own import table so an aliased import
  cannot be read as a plugin that does not exist. It was proved on the defect
  itself — deleting the catalog line makes it fail naming `webhook-out`, and the
  file was restored from a scratch copy with a matching checksum rather than by
  git.

  **The lesson generalises past plugins, and it is why the new test says so in
  its own godoc: a gate that derives BOTH sides of a comparison from the same
  place proves the two agree with each other and nothing about the world.** The
  test that was green here walked documents against source and source against
  documents; both of its directions read the source tree, and the binary — the
  thing the operator actually runs — was never asked. This is the same shape as
  D16, where the column audit read a schema it reconstructed from CREATE TABLE
  only, and the same as the earlier finding that a fake repository proves the
  fake. Worth checking the remaining audits against.

- **D23** **The audits were audited, and three of them were green on the very
  defect they were written for.** D22 closed with a sentence rather than a fix —
  "worth checking the remaining audits against" — and 2026-09-06 is the round
  that did it. All **89** gate tests in `internal/arch` were read one by one and
  each was asked the same three questions: what are the two sides of the
  comparison, where does each side come from, and what plausible violation would
  leave it green. Three answers were bad enough to fix, four more are recorded
  below unfixed, and the rest are sound.

  **Start with the last part, because it is what makes the rest worth reading.**
  The overwhelming majority of these gates hold, and the reason is structural
  rather than lucky: **21 of the 89 exist ONLY as blindness guards or positive
  controls** — a test whose whole job is to plant a violation and prove the
  reader beside it still sees one. Almost every remaining gate carries at least
  one counter naming what it would mean for that particular link of its walk to
  go quiet, and several carry three to five separate counters because a single
  total would hide which link broke. The five shapes they come in are worth
  writing down, because the defect D22 named is the absence of all five: two
  packages that cannot import each other compared through the compiler; source
  against a DIFFERENT artefact (the environment example, the compose file, the
  build files, the smoke test's log literals); producer against consumer inside
  one scan, where the question itself is a relation in the source and both
  directions are audited; source against THE WORLD, where the test compiles an
  out-of-tree module, runs the binary, or round-trips SQL on a container; and
  prohibitions carrying a field-of-view counter. Three hypotheses about weakness
  were chased and killed by measurement — the SQL ownership map, the
  publish/subscribe family and the plugin-installability gate all turned out to
  be sound, two of them for reasons already argued in their own file headers.

  **Finding 1, and it is the expensive one: `TestMoneyIsAnInteger` read one tree
  out of five.** Its godoc states a repository-wide rule — money is stored as an
  integer in minor units — and its walk was the module list. Measured: **682**
  money-named struct fields in the production trees, **118 of them outside**
  `internal/modules`, and **93 of those in** `internal/workflows`, which is where
  the totals are actually COMPUTED — checkout, price, discount, tax, shipping,
  the payment amount. A float declared in the one place the rule exists to
  protect was outside the rule, and the gate was green on all 118 the way an
  audit that never opened the file is green. The walk is now `productionTrees`,
  the list a separate gate holds against disk, with a per-tree file counter so a
  tree going quiet fails instead of passing; the decision was widened at the same
  time from a bare identifier to pointer and slice floats, because a nullable
  money column is exactly where somebody reaches for a pointer. Mutation-proved
  three ways, and the old gate was green on the same planted float in the
  checkout money chain.

  **Finding 2 is D22's lesson at the smallest possible scale: the "same place"
  was not a second file, it was the assertion's own function.**
  `TestThePanelStatusOptionsAgreeWithTheModules` compared the panel's status
  dropdown against a three-element slice written INSIDE the test body. Each
  element was compiler-bound to a real constant, which is what made the list look
  like the module's answer; it was not, because the ENUMERATION belonged to the
  test. Mutation-measured: adding a fifth status to the module's constant block
  AND to its `Status.Valid` switch — the whole of a real change — left the gate
  green, so an operator could never have selected the new status and no error
  anywhere would have said why. That is the direction the test's own godoc calls
  its real reason. The module's vocabulary is now READ from the module's models
  package and then filtered through the COMPILED `Status.Valid`, which is two
  different instruments looking at the same thing — a source scan cannot check
  itself, and Go constants do not exist at run time to be reflected over. A
  constant declared and left out of the switch is now reported on its own, and
  the new control plants into the READER rather than into the comparison,
  because the reader is what was missing. A godoc in the panel already claimed
  this pinning worked; the sentence was false until this fix and is true now.

  **Finding 3: the three migration gates read 16 of the 24 migration directories
  that existed when it was measured** — 17 of 25 since the review module landed
  the same day (B4). Rollback, the cross-module foreign-key rule and the
  real-Postgres round trip all derived their input from the module list. The
  eight they never opened are four plugin schemas and the four CORE ones —
  `core/audit/migrations`, `core/eventbus/outbox/migrations`,
  `internal/core/workflow/pgstore/migrations` and
  `internal/core/job/jobpg/migrations` — and the core four are the worse
  omission by a distance, because they are applied BEFORE any module migration
  on every startup, so a down that fails there leaves the migration ledger dirty
  in front of the whole boot. That is verbatim the fault the round-trip test was
  written for. All eight were compliant when measured; they were compliant
  UNAUDITED, which is the same relationship to safety that an unread file has.
  A shared `migrationDirs` helper now discovers them from `productionTrees`
  under two floors — every module must still be found, and at least one set must
  be outside `internal/modules` so a future narrowing fails loudly — and the
  integration gate round-trips all 25 on a real container. The helper DISCOVERS
  them rather than being handed a list, so the review module's schema joined the
  three gates without anyone editing them; only the written counts move.

  **A bug was introduced while fixing that and caught before it shipped**, and
  it is recorded because the mutation run is what exposed it: the first version
  of the helper walked the repository root recursively as well, so every
  directory was found twice and each finding printed twice. It was fixed by
  reading the root at depth one — the way the shared file walk already documents
  — rather than by deduplicating, because a dedupe would have hidden the overlap
  instead of removing it.

  **Four more were measured and deliberately NOT fixed.** Each is written with
  the reason, because "we looked and left it" is a different statement from "we
  did not look":

  | gate | what it lets through | why it was left |
  | --- | --- | --- |
  | `TestEveryColumnIsWrittenBySomething` | the same 17 of 25 schemas. Ten tables owned outside the module tree have no "is this column ever written" audit at all | it reads writes from the modules' query files, and those packages build their SQL in Go. Widening the declaration side without the write side would report every one of their columns as dead — a pile of false accusations is how a gate gets deleted, and this package says so in three places. It is a scoping change, not a widening (see D16) |
  | `TestModuleSQLNamesOnlyItsOwnTables` | a module naming a PLUGIN's table. The ownership map skips any table no module owns | the file header ARGUES that allowance for core tables and run-time link tables and does not argue it for plugin tables. Measured: no module names a plugin table and no plugin names a module table today. The right order is to extend the header's argument first, and changing what "owner" means in a file whose header is an argued decision is not a silent edit |
  | `TestTheDefaultServeMuxIsNeverUsed` | an empty walk. It is the one prohibition gate in the package with no counter of its own and no positive control | cheap to fix and it lets through strictly less than the three above; it is protected indirectly, since the tree list it walks is pinned elsewhere and other gates share the walk |
  | `TestLayerPurityCatchesAViolation` | less than its name says. It plants into a RE-IMPLEMENTATION of the decision — its own fixture, its own inline match — so it proves the forbidden fragment is matchable and nothing about whether the real walk sees it | the pair is sound overall, because the gate it guards pins its own reader with a per-layer scanned counter against a written-down exemption map. The control alone is what is weak |

  Two smaller notes that are not defects and would mislead a reader who trusted
  the test names. `TestDetectorPassesEnglishSource` asserts that a fixture
  carrying a Turkish word written without diacritics produces NO hits: that is
  the detector's known blind spot encoded as correct behaviour, deliberately and
  documented, but the name reads like proof the detector is clean on Turkish and
  it is not. And `TestTheGuardedPrefixesStillExist` only requires the admin
  prefix literal to appear ANYWHERE in the composition root's setup file, which
  a comment would satisfy; measured, it is satisfied today by two real
  constants, so the gate is currently telling the truth by luck rather than by
  construction.

  **What this entry does NOT claim.** The 84 gates that were not changed were
  judged by reading their readers, their counters and their controls — not by
  breaking each one. Five were mutation-proved: the three fixed and the two
  blind spots. `TestNoTurkishOutsideLedger` could not be exercised at all
  (the user's untracked file at the repository root fails it, so the whole round
  skipped that one test), and the smoke-tagged suite was not run, so the gate
  that pins the smoke test's log literals against production source is read-only
  evidence too. One loose end for whoever is next: the new blindness control has
  no row in the README's invariant table. Nothing is red, because that audit only
  checks README against the repository and not the reverse, but the row is
  missing.
- **D25** **A handler can read a query parameter it never describes, and every
  gate in this repository stays green.** Proved by mutation on 2026-09-06: a
  branch reading `undocumented_switch` was planted in the storefront product
  listing, and the whole module suite plus `internal/arch` passed. The parameter
  would be a working, unadvertised switch — invisible to the generated client,
  to the document and to review.

  The audit that looks like it covers this,
  `TestStoreListDescribesOnlyParametersItReads`, only closes the OTHER
  direction. Its godoc states the fault it prevents — "putting a parameter that
  is not read into the schema is promising the client a feature that DOES NOT
  WORK" — and that is exactly one half. Worse for the missing half, neither of
  the sides it compares is the HANDLER: it checks the generated document against
  a list written by hand in the test, so a parameter absent from both agrees
  with itself and passes.

  **The gate this wants was priced and NOT written, because the naive form fails
  this repository's own standard.** A first approximation — every
  `URL.Query().Get("x")` literal and every `xxxParam(r, "x")` literal, compared
  against the `queryParameter("x")` calls of the same package — reported
  findings in TWELVE modules, and checking them showed almost all were noise:
  `id`, `upload_id` and `sales_channel_id` are PATH parameters, and `limit` and
  `offset` are described through a `paging` variable the regex could not see.
  A gate whose exemption list would have to hold its own false positives is not
  a gate, so what is needed is the structural rule: a function is a
  parameter READER when its body passes one of its OWN parameters to
  `URL.Query().Get`, and the literal is then collected at its call sites; the
  described side is derived from the calls that build an `openapi.Parameter`.
  Two sides, two different constructs, no hand list. Today's leakage under that
  rule is believed to be zero — the planted switch was the only one found — so
  the gate is PREVENTIVE, which is also why it was not worth shipping in a
  hurry.

- **D24** **A carrier's events arrive out of order, and the shipment state
  machine refused all of it — while tolerating repeats.** Measured 2026-09-06 by
  printing the whole transition table from a throwaway probe rather than reading
  it by eye, and the shape of the answer is what makes it worth an entry: a
  second ship, deliver or cancel landed on a no-op, so IDEMPOTENCE was handled,
  and pending + deliver and delivered + ship were both conflicts, so REORDERING
  was refused. Those are the two things a webhook stream does, and only one of
  them had been thought about.

  The consequence has a live half and a latent one. Latent: there is no carrier
  plugin yet (C6), so no webhook is being refused today. Live: the admin routes
  exist now, and an operator reconciling against a carrier's portal by hand had
  to click "ship" before recording a delivery — which stamps a dispatch moment
  from the clock, a number NOBODY MEASURED, written into the very column the old
  rule was trying to protect. The old godoc argued that skipping the step "would
  leave shipped_at empty and reconciliation would have no answer for when the
  fulfillment set out". It named a real hole and prescribed the wrong remedy:
  refusing the delivery does not produce the dispatch moment, it discards the
  delivery too, and leaves a parcel provably in the customer's hands reading
  pending for good.

  The fix is a fourth outcome in the table — a reported fact that is BEHIND the
  shipment's position is accepted without moving the status backwards — and not
  a second, looser table for callbacks. The rejected alternative is recorded
  where the table is: two tables disagreeing about the same statuses is exactly
  the "rules extracted from ifs scattered across three service methods" that
  having a table prevents, and it also mis-states the admin case, since an
  operator typing in what a portal told them is reporting too, not commanding.
  A late collection report writes the tracking number — often the only message
  carrying it — and stamps NO moment, because the only clock available says now
  and would date a dispatch after its own delivery. A missing stamp says nobody
  told us; an out-of-order stamp says something false.

  Cancellation stays strict and that is the discriminator the table now carries:
  it is the one transition here that is a COMMAND rather than a report, so
  "this arrived late" has no meaning for it, and a carrier reporting a
  collection after WE recalled the parcel contradicts our own record rather than
  merely overtaking it.

### E. Out of framework scope — written, not forgotten

- **e-Fatura transmission** needs the merchant's certificate and an integrator
  contract. The framework owes the document, the numbering and the slot; the
  first two are built (ADR 0024).
- **Customer identity** is the embedding application's job (ADR 0008), unless A9
  supersedes it.
- **A/B assignment** likewise, if A9 stands: the framework has no visitor.

### G. Found while building, not yet decided

- **The rig cannot reproduce the case that motivated the change it paid for.**
  Its taxonomy is uniform by construction — twenty categories of exactly 2,600
  products each, from `internal/rig/catalog.go` mapping product n to category
  `(n - 1) % C + 1` — and the OR/EXISTS collapse measured on 2026-09-06 is
  invisible at that shape: 16.5 ms at 5%, 147 ms at 0.05%, same statement, same
  catalog, one different category id. The selective categories were hand-built
  on a scratch database and are gone with it, so the 34x-to-586x row of the
  module's own table cannot be re-derived by running a command. `rig.Spec`
  takes a category COUNT and has no skew option. The DECISION is what shape a
  skewed rig should have — one small category, a power-law spread, or a
  parameter — and whether the plan-shape acceptance test should pin the skewed
  case too, which would make it the first acceptance test that asserts a
  DIFFERENCE between two plans rather than one plan.
- **Two clocks on one axis.** `payments.captured_at`, `fulfillments.shipped_at/
  delivered_at/canceled_at` and `invoices.issued_at` are stamped by the process
  that wrote them; every other moment comes from the database's `now()`. On one
  machine they agree; across machines a capture can be printed before the order
  it paid for. The timeline names the clock per entry rather than hiding it, but
  the DECISION — move those three to the database clock, and lose the injectable
  clock the tests use — is open.
- **`authorized_at` and `refunded_at` do not exist.** A session can become
  `authorized` and nothing records when; `refunds` has no `refunded_at` and its
  `created_at` is the de-facto moment. Adding either is a schema decision.
- ~~**A dead subscription shipped in an example and nothing refused it.**~~
  **Closed 2026-09-05.** `TestEverySubscribedTopicHasAPublisher` is the missing
  half of the topic audit: the old one walked publish → subscribe, and the other
  way is worse because it fails ENTIRELY silently — the handler registers, the
  bus accepts it, and it never runs. The examples are scanned too, since they
  are the files a customer project copies. Mutation-proved by restoring the
  actual bug.

### F. Standing work

- **Translation ledger: 260 files.** ADR 0012 lets it only shrink.
- **Panel: five sections over four of seventeen modules; thirteen modules have no
  screen at all**, nothing can be created or deleted from it, and there is no
  extension point for a plugin to add one. The two counts came apart on
  2026-09-05: Sales is the fifth SECTION and the order module's SECOND screen,
  so it changed the section count and not the module count. Review (B4,
  2026-09-06) then moved the denominator without moving either — no file under
  `internal/adminui` mentions it — and it is the first screenless module that is
  NOT configuration: its moderation queue, the review listing filtered to
  `submitted`, is daily operator work. The panel sections dated 2026-09-05 below
  therefore describe a sixteen-module tree and are due a re-measure rather than
  a correction.

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
   in a comment (`internal/app/migrate.go`).

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

3. **The catalog's text search has no index that can serve it, and its ceiling
   is now measured** — 2026-09-05.

   Not a defect and not a surprise: the predicate carries a leading wildcard, so
   no B-tree helps even if one existed on `title`. What was worth measuring is
   the SHAPE, and it is the opposite of what "no index" usually implies. A term
   matching almost the whole catalog is answered in 0.03 ms because the ordered
   scan stops at 25 rows; a term matching ONE product costs 9.1 ms and reads all
   730 pages of the table. At 16 clients a selective search runs at 638 to 856
   per second against 11,564 for an unfiltered listing.

   The number to plan against is the throughput one, not the latency: **a few
   hundred concurrent selective searches per second is the first ceiling this
   repository has that is not the response path.** The panel is nowhere near it
   and never will be; a storefront search box reaches it long before the catalog
   grows. The full record — plans, buffer counts, the prepared-statement plan
   flip, the count's behavior and the options not taken — is
   `docs/catalog-search-cost.md`, and its consequences for B2's remaining work
   are in the B2 section below.

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
   nothing anonymises. ~~The word KVKK appears exactly once in the repository, in
   a notification test, observing that not storing a second copy of the
   recipient address keeps the number of places to clean small — which is the
   right instinct and the only trace of the requirement anywhere.~~ **Corrected
   2026-09-06: it appears twice in the code, and the load-bearing one is not the
   test.** Both are in the notification module and both make the same argument:
   the schema comment in the module's `000001_notification_init` migration, under
   the heading that the recipient address is NOT STORED, says a second copy
   raises the number of places that have to be cleaned up on an erasure request,
   and `TestTheLogCarriesNORecipientAddressCOLUMN` holds that at the schema level
   so code that does not write the address today cannot write it tomorrow. Both
   strings predate this section by five days, so the count was wrong when it was
   written rather than overtaken. It is still the right instinct and still the
   only trace of the requirement in the code.

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

~~The nuance that will matter one day: those two methods cannot be composed into
a larger transaction. Called from inside a service transaction they would open a
SECOND, independent one, and a rollback of the outer would not undo them. Today
no caller does that. The day one does, the fix is the ambient-transaction
plumbing the other six already have, not a bigger method.~~

**Corrected 2026-09-06, and the wrong half was "today no caller does that".** A
caller did — the tax service's own create paths read a rule and then wrote
against it in two separate transactions — and a godoc in the repository was
already describing the composed behaviour as if it existed. TAX now carries the
transaction in the context like the other six, and its first shared lock exists;
the full record is D6. REGION still has the uncomposable shape, and so, unnamed
by the original sentence, do `pricing`, `promotion`, `auth` and `customer`. The
prescription in the struck paragraph was right and is what was done; what it got
wrong was the deadline.

### Global state: there is none

Zero `init()` functions. Zero package-level mutexes, maps or singletons. Every
package-level `var` in the repository is effectively immutable — an embedded
filesystem, a compiled Redis script, or a SQL string assembled from constants.
Dependencies are struct fields, resolved from the container BY NAME, and the
composition root is the only place that knows what is wired to what.

### Repository tests: real Postgres, and no mocking library at all

There is no gomock, no mockery, no testify/mock in go.mod. ~~Twenty-eight test
files bring up a real PostgreSQL or Redis with testcontainers~~ **Corrected
2026-09-06: the figure was never twenty-eight.** Counted by the testcontainers
IMPORT rather than by the word, thirty-two files brought one up on the date
above and thirty-five do today; the word finds exactly one file the import
does not — `internal/smoke/process_test.go`, which mentions testcontainers in
prose without importing it — and one file the import DOES find is excluded on a
different criterion: `plugins/files3/s3_integration_test.go` imports it and
brings up MinIO rather than PostgreSQL or Redis. **Fifteen of sixteen modules construct their REAL
repository over a real pool** in their integration tests.

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

1. ~~**There is no review module.**~~ **Built 2026-09-06 (B4), and the count is
   seventeen:** product, pricing, inventory, region, customer, cart, payment,
   order, fulfillment, promotion, tax, auth, file, notification, b2b, invoice
   and review. A customer can leave a review now and moderation has something to
   moderate, so the half this item said had to come first is done — schema,
   service, storefront write, admin read and a four-edge moderation state, with
   the storefront's reads carrying `approved` as a SQL literal.

   The other half of this item still stands and is now the ONLY half: the AI
   subsystem does not exist. Read what that leaves precisely, because the
   moderation FLOW is not the thing that is missing — it exists and it is a
   HUMAN's, a queue at `GET /admin/v1/reviews?status=submitted` and one endpoint
   that moves a review. What an AI would add is a SUGGESTION on that queue, not
   a decision, which is the `sagawatch` shape ADR 0017 already argues for.

   And the event this item named is still absent, deliberately: the module
   publishes none, because `TestTheEventTopicsHaveASubscriber` refuses a topic
   no production file subscribes to and its exemption map is empty by policy. A
   review event and its first subscriber are one package or neither — exactly
   the constraint B7 records for inventory. So "`review.created` reaching a
   worker" is not free machinery waiting to be used; it is a second thing the
   first AI use case has to bring with it.

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

> **Acted on 2026-09-05, and the measurement below is now HISTORY.** The twelve
> packages an extension author needs were promoted out of `internal/` to
> `core/` (ADR 0026), and the composition root moved out of `package main` to
> `internal/app` behind a four-method facade at the module root (ADR 0027).
> `cmd/server` is fifteen lines. An out-of-tree plugin is proved by compilation
> in `examples/plugin`, and an out-of-tree application by `examples/starter`,
> which the audit both compiles and runs.

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
| merged migrations | the registry already merges per-owner migration sources and refuses two owners claiming one table | `core/module`, `internal/app/migrate.go` |
| a project can add its own module | `Registry.Add(mod Module)` — the exact method the brief needs | `core/module` |
| `Product.Metadata` jsonb | present on auth, cart, customer, fulfillment, invoice, order, payment, product, promotion and tax — inside product it reaches the variant and taxonomy tables too | ~~eleven modules~~ **ten; corrected 2026-09-06** — variant and taxonomy are product's OWN tables rather than modules of their own, and promotion, which has the column, was left out |
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
   of `internal/app/setup.go` that no external program can call.
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

**Swept on 2026-09-06, and this flag has three siblings.** The sweep was an
instrument rather than a grep: a `go/ast` pass collecting side A — every `bool`
and `*bool` struct field carrying a json tag, plus the read layer's record keys
— and side B, the same name appearing in a CONDITION (an if, a for, a switch, a
case, or an operand of not/and/or), with the `x != nil` patch-application shape
excluded because it tests whether a field was SENT and not what it says. Every
SQL predicate was scanned for the snake_case column alongside. Generated sqlc
packages are skipped: their row structs mirror the column rather than publish it
independently, and counting them would double every finding.

Three numbers came back:

- **35 published boolean flags; 17 boolean columns, across ten modules.**
- **Never written: zero.** Every one of the 17 columns has an INSERT that names
  it or an UPDATE that sets it. That is `TestEveryColumnIsWrittenBySomething`
  (B18) doing its job across the whole boolean population, checked from the
  outside rather than trusted.
- **Carried but never decided upon: four** — `manage_inventory`,
  `allow_backorder`, `discountable`, `is_giftcard`. Every other stored boolean
  has a reader that changes what the system does: `is_active` and `is_internal`
  cut the category listing, `is_disabled` is applied inside the key-to-channel
  resolution itself, `admin_only` and `is_return` decide which shipping options
  a cart may see, `requires_shipping` is read in the inventory queries, and so
  on down the list.

**The four are not scattered, and that is the finding.** One module publishes
every unread boolean column in the repository; the other sixteen publish none.
A6 therefore is not "a flag needs a reader" — it is the product module's DTO
making four promises it does not keep, and answering it one flag at a time would
leave three behind. Two of the four cannot even acquire a reader here: the
storefront hands the inventory record through as a loosely typed record on
purpose (the accepted price of ADR 0004, written on `StoreVariant`), so nothing
in this module may interpret the stock pair and the only place that could is the
checkout saga.

**The sweep is deliberately NOT an arch test, and the reason is the more useful
half of the measurement.** Run naively over Go field names it reports 15 flags
and 11 of them are wrong — a 73% false-positive rate — because most booleans
here are not stored flags at all but RESULT fields on a response DTO:
`already_issued`, `already_open`, `released`, `cart_completed`,
`summary_recorded`, `reservations_confirmed`. Nothing in this repository should
read those; the CLIENT reads them. Anchoring the gate to a boolean COLUMN
removes ten of the eleven and takes the report down to five.

The eleventh survives the anchor and is still wrong, and it is the instructive
one. `automatic_taxes` is a real column, published on the region DTO, and no
branch in the region module reads it — but the cart does. The region module
hands it across the boundary through a primitive interop method that returns it
as an UNNAMED bool (`RegionTax` gives back a rate and an "automatic" flag), and
the cart's tax step branches on it: not automatic, no tax line. The value flows;
the NAME does not survive the crossing. That is ADR 0001's interop rule working
exactly as designed, which means a name-based reader audit cannot be made sound
against this repository's own house style — a gate failing the build on
`automatic_taxes` would be teaching somebody to widen a published contract in
order to satisfy a scanner. The finding is recorded here instead, which is the
honest place for something a test cannot hold.

Region is also the contrast that shows what pinning looks like: its service
tests already assert both that a flag left out of an update is unchanged and
that an explicit false is WRITTEN rather than read as "do not touch". Inventory
pins its equivalent default too —
`TestCreateInventoryItemVarsayilanSevkiyatGerektirir` fails at once when
`requires_shipping` is flipped. Product pinned none of its four until
2026-09-06, and that contrast is what makes this a gap rather than a house
convention. What was pinned, and what it cost to find out it was not, is D2.

**The waitlist ("tell me when it is back") is nothing today, and it is NOT the
cheapest item on this list.** That claim stood in this file until 2026-09-05,
when it was measured and found wrong twice over. The correction is recorded
here as well as in the C1 row, because the row was corrected on its own once
and this passage went on repeating the disproved sentence.

The first count was wrong. Three parts are missing, not one: the table, an
inventory EVENT — the module publishes nothing at all, so there is no "it is
back" to react to — and a subscriber to turn that event into a message. Nor can
the event be landed by itself: the topic gate refuses a name no production file
subscribes to, its exemption map is empty as a matter of policy, and the one
plugin that reads the catalog indexes no stock and so would not want the event.
The event and its first consumer are one package or neither.

The second is larger, and it is a DECISION rather than a gap — A15. The table is
not the hard part; the ADDRESS in it is. The storefront has no customer
identity: the only principal it carries is a publishable key that names a sales
channel, and every storefront write endpoint takes its subject from a path
identifier the client chooses, which is D3. ADR 0008 already measured the cart's
email as an identity anchor and rejected it. So the only thing a "notify me"
form could hold is an address typed into a box, unverified — and this repository
has no verification, no double opt-in, no unsubscribe and no CAPTCHA, while the
notification module deliberately stores no recipient address at all, because a
second copy of one raises the number of places an erasure has to reach. That
table would be the first row in this repository holding an unverified address
for the purpose of mailing it.

Two smaller consequences follow from the same measurement. The subscribe
endpoint's own answer leaks: a success that differs from a conflict tells the
caller whether an address is already waiting, which is the enumeration hazard
ADR 0008 names for the order endpoint. And nothing slows that down per address —
the storefront's quota is one bucket over the whole prefix, reads and writes
alike, keyed by default on the connection rather than on the client, so behind a
proxy it is one shared bucket for every visitor.

None of this makes the feature wrong to build. It makes the FIRST step a
sentence rather than a table, and A2 sits upstream of that sentence: if the
embedder is the controller, the answer may be "publish the hooks and the
erasure contract" rather than "implement consent", and those are different
builds. Writing the table first is what would make it expensive — an address
stored before the question is answered is an address that has to be migrated, or
erased, once it is.

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

1. **The waitlist's DECISION** (A15), because it is a sentence rather than a
   build, and because it is the step that gets more expensive the longer it
   waits: every row written before it is answered is a row that has to be
   migrated or erased afterwards. The build behind it is not one part but
   three, and the event half is blocked separately by B7.
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

## B2's remainder is four different kinds of work — measured 2026-09-05

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
  useless.**~~ **Built 2026-09-06.** `GET /store/v1/option-values` is the fourth
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

Filed as a filter, measured as a definition problem: there is no amount on the
page to compare against. The full reasoning is in A16; the short form is that a
product has no price, "the price" is a selection function with five ordered
tie-breakers, and the storefront's prices arrive in a second round trip made
AFTER `LIMIT`/`OFFSET` has already cut the page — filtering there would filter a
page that was chosen before the filter ran.

### In-stock — also a decision. Now A17

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
record with every plan and every buffer count is `docs/catalog-search-cost.md`;
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

## AI-powered commerce features — measured against the brief, 2026-09-05

The brief: natural-language search ("winter, under 500 TL, dark colour"),
automatic review summaries and Q&A, attribute extraction from product photos, a
chat assistant with tool-use that can read an order and start a return, and
price/stock forecast SUGGESTIONS an operator applies rather than the system.

Five areas measured in parallel against the tree. One is genuinely close; the
rest are blocked by something more basic than the AI.

### Natural-language search: the filters it would translate INTO are half built

The layer the brief describes turns a sentence into filters. Two claims made
here on 2026-09-04 were overtaken by B2 and B3 the next day and are struck
rather than deleted, because the shape of the miss is worth keeping:

~~**The entire structured filter surface of the storefront is one collection id
plus free text** — `collection_id`, `q`, `limit`, `offset`, `after`,
`with_count`, on REST and GraphQL alike.~~ **Measured again 2026-09-05:**
`category_id` and `tag_id` are on both surfaces, and the test that pins REST and
GraphQL to each other holds them together.

~~There is also **no storefront endpoint that enumerates collections, categories
or tags**, so an NL layer has no public vocabulary to resolve a word to an
id.~~ **B3 built all three listings**, so the word→id half now has a public
vocabulary.

What is still missing is narrower and no longer one thing — the split above is
the measurement. "Under 500 TL" has nowhere to land and will not until **A16**
is answered; "in stock" until **A17** is; "dark colour" needs both the
option-value filter and a vocabulary endpoint for option values, neither of
which exists; "cheapest first" needs a sort parameter no surface accepts, and a
cursor that would silently page through the wrong order if one were added
carelessly. "Winter" still has no first-class home — season is not a column —
but of its natural carriers, collection, category and tag, ALL THREE are
filterable now rather than one.

Colour and size ARE modelled (`product_option`, `product_option_value`, and the
variant join) and neither the listing nor the search index reads them.

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

The honest order was: the filters first, then a vocabulary endpoint, then the
layer that maps a sentence onto them. Two of those three steps are part-done,
and the ordering claim survives its own progress — an NL layer built before the
filters would be a translator with no target language, and the target language
is currently four words short: price, in-stock, option value, sort. Two of the
four are sentences somebody has to write (A16, A17), not code somebody has to
type, and that is the only reason the order still holds.

### Review summaries and Q&A: the data exists now, the two hooks do not

**The review module landed on 2026-09-06 (B4), so "there are no reviews" is no
longer the blocker.** What blocks C11 now is narrower and it is named in the
module's own package doc: it publishes no read-layer provider and no event, and
both absences are deliberate. A provider nothing resolves fails the consumer
audit; a topic nothing subscribes to fails the topic gate. C11 is the first
reader of both, so both land in the package that brings the reader — which is
this repository's standing rule for a capability, applied rather than waived.

Read the two hooks separately, because they are needed for different halves of
the feature. Without a PROVIDER a summariser cannot read a review at all: it
lives in another module and ADR 0001 forbids the import, so the read layer is
the only door. Without an EVENT a stored summary has nothing to invalidate it,
and the section below on `product.metadata` says why the summary has to be
stored somewhere of its own rather than on the product.

**One number a summariser should know before it stores anything.** The review
module already computes the count and the average on READ, and that was measured
rather than preferred: against PostgreSQL 16 on a rig of 505,000 reviews over
20,001 products, with the module's partial index on the approved rows, the
aggregate costs 0.17-0.21 ms for a product with 19 approved reviews, 1.3-2.0 ms
at 5,000 and 9.3 ms at 50,000, against 33-38 ms with no index at all — where it
is a full parallel sequential scan whose cost does not depend on the product's
own review count. The first page of twenty reviews is 0.03-0.04 ms at every one
of those sizes, because the LIMIT stops the index scan. The index is 40 MB
against a 348 MB table.

The crossing point is stated instead of hidden: the cost is linear in the ONE
product's approved count, so only a shop with hundreds of thousands of reviews
on a single product is buying anything with a stored counter — and it would buy
those milliseconds by owing a correctness obligation to every path that writes a
review. That is the same trade A16 records against denormalising a price into
the catalog, and it fails for the same reason there: the missing piece is the
invalidation signal. A TEXT summary is the opposite case and this is where the
numbers stop applying — it cannot be recomputed per request at any price, so it
must be stored, and storing it is precisely what needs the event.

The measurement adds two more details that decide where such a summary could
live.

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
needs a fifth, which is the review module's to publish. That is still exactly
true after B4 landed — the module publishes none — and it is now assignable
rather than hypothetical: the topic, its first subscriber and the summary table
are one change.

### Attribute extraction from photos: the image cannot be read back

Blocked below the AI, and in a way that is easy to miss.

- **`FileProvider` has only `Upload` and `Delete`.** On any real object-store
  deployment the application cannot read an uploaded image back at all. A vision
  pipeline has no bytes to look at.
- **The file module publishes no events**, so nothing can react to a photo
  arriving.
- ~~**A product image and its upload record are not linked.** `product_image.url`
  is free text, there is no `upload_id`, and a cross-module foreign key is
  forbidden (Principle 2.2). Given an image row there is no way to reach its
  storage key.~~ **Corrected 2026-09-06: the link was built later the same day
  (B15).** `product_image.upload_id` is a nullable opaque text column with a
  non-empty CHECK (product migration `000002_product_image_upload`) — no
  cross-module foreign key, so Principle 2.2 is intact — written on image
  create, carried on the admin image DTO, and readable in reverse through
  `GET /admin/v1/product-images/by-upload/{upload_id}`; the binding is declared
  by PRODUCT as `LinkUploadProductImage`, and `file.interop`'s `UploadJSON`
  resolves the id to the upload record. What is still missing is narrower than
  "not linked": the cross-module `uploadRecord` carries the URL, content type,
  size and checksum but deliberately NOT the storage key, and `FileProvider`
  still has only `Upload` and `Delete` — so a pipeline can now reach the RECORD
  of the photo and still not its key or its bytes, which is the first bullet's
  point.
- **Images are write-once at product create** — no per-image endpoint and no
  `Images` field on the update input, so a pipeline could not write back what it
  found.
- **There is nowhere to put a suggestion.** `product_category_map` is a bare
  `(product_id, category_id)` with no confidence, no source and no pending
  state, and the setter replaces the whole set atomically.

Not one of these is about models or prompts. Four were ordinary plumbing
decisions that would each be worth making on their own merits; the image ↔
upload link was made on 2026-09-05, so three are left.

### The chat assistant: the tools already exist, the caller does not

**This is the area that is genuinely close, and for a reason nobody planned.**

There are **seventeen `*.interop` container surfaces carrying sixty-four
methods** — twelve module ones and the five under `workflows.` — and by written
rule (ADR 0001/0006) every one takes and returns primitives, slices of
primitives, or `json.RawMessage`, with composite schemas documented next to the
method. That is a tool catalogue, built for module isolation and arriving fit
for tool-use by accident. The container can even enumerate the names at runtime.
Re-measured 2026-09-06: the figures here were fifteen and sixty-one, and both
had already moved by the close of the date above, because `file.interop` and
`workflows.fulfilling.interop` were registered later that same day.

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
   ten tools and not for sixty-four.

### Forecast suggestions: there is no history to forecast from

- **Stock is a current-value column, not a ledger.** `inventory_levels`
  overwrites `stocked_quantity` and `reserved_quantity` in place, so the
  database cannot answer "how much stock did this item have last Tuesday" and a
  depletion rate is not derivable at all.
- ~~**Demand history exists and is unreachable.**~~ **HALF CLOSED 2026-09-05
  (B14): it is reachable now, and it is still not aggregated.** `orders` and
  `order_line_items` carry quantity, price and `created_at` — a real time series
  — and the read layer offers the line as its own entity, `order_line_item`,
  next to `order`. It takes `placed_from`/`placed_to`, half-open, matched
  against the ORDER's `placed_at` through a join. The join is a decision and the
  migration argues it: copying `placed_at` onto the line would make the listing
  a single-table range scan, which is the cheaper shape, but it would be a
  SECOND source of truth for one fact and nothing in the schema would keep the
  copy right — a line added to an existing order by an exchange carries the day
  of the exchange, and a report drawn from it would disagree with the order it
  belongs to without anything failing. Migration 000006 adds
  `orders_placed_at_idx` and `order_line_items_variant_idx`, so that "last
  month" costs the month rather than the whole sales history.

  Three things are still missing, and they are three different things:

  - **There is no aggregation surface.** The provider returns RECORDS — no
    grouping, no sums, no ranking — because a GROUP BY behind that interface
    would produce records that are not records of an entity, which is the one
    thing the read layer's contract cannot express. So "which variants sold most
    last month" is still a query somebody writes by hand; what exists is one
    clamped page of the rows it would be computed from. The panel's report
    prints no total at all rather than a sum of whichever rows sorted first, and
    the argument is written in the template and the handler — where the next
    person tempted to add one will read it.
  - **There is no link between a line and a variant**, so no Graph request can
    expand from a sold line to the product it sold. The line carries
    `variant_id` as another module's identifier and does not validate it
    (Principle 2.2); a consumer that wants today's catalog name has to read the
    product entity itself, and what the line holds is the title AS SOLD.
  - **Forecasting itself is still absent.** This is the READ SURFACE a forecast
    needs, not the forecast. The other half of it is the bullet above: stock is
    a current-value column, so the depletion rate has no history to come from,
    and that is B7.
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
~~reviews that do not exist~~ **reviews that exist since 2026-09-06 but publish
no event and no read-layer entity**, images that cannot be read back, history
that was never kept. The fifth is blocked by a decision (ADR 0008) rather than
by machinery, and its machinery is unusually ready.

The review item is worth watching as it moves, because it changed CATEGORY
rather than closing: it used to be blocked by missing data, and it is now
blocked by two withheld capabilities that its own first reader is supposed to
bring. That is a smaller blocker and a differently shaped one — nobody has to
design a schema for it, somebody has to write the consumer.

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

### Checkout: better than the brief assumes, and its one hole is closed

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

~~**The real hole: the cart's addresses never reach the order.** `cart_addresses`
exists; the order module has no address table, column or model field. This is
also why the invoicing flow has to take the buyer's address from its caller —
the order does not have one.~~ **Corrected 2026-09-06: the hole was closed on
2026-09-05, hours after this section was measured, and B11 above already records
it.** The order module's migration `000005_order_addresses` creates
`order_addresses` — one shipping and one billing per order, enforced by the
unique index `order_addresses_one_per_type` — `models.Order` carries
`ShippingAddress` and `BillingAddress`, and the cart's copies travel cart →
interop → `checkout.SnapshotAddress` → order snapshot, written by
`CreateOrderAddress` inside the order's OWN transaction. The invoicing flow does
still take the buyer from its caller, but for the reason `invoicing.IssueInput`
gives in its own godoc — the VKN or TCKN and the tax office are not in this
repository's customer model at all — not because the order has no address.

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
- ~~**The status vocabulary is four values** — pending, shipped, delivered,
  canceled — pinned by a database CHECK. There is no "in transit", no "at
  branch", no "delivery attempt failed", and no **"returned to sender" (iade)**,
  which is a real carrier state a shop must act on.~~ **The iade half was closed
  2026-09-06.** `returned` is the fifth value, terminal, with a moment that
  mirrors it exactly. The transit statuses stay absent and that is now a
  DECISION rather than an omission: "at branch" and "delivery attempt failed"
  are positions on a journey that the module records nowhere and no consumer
  asks for, while a return changes what the shop must do next.
- ~~**The state machine is strict and one-way**, and a carrier's event stream is
  not. `DeliverAction` from `pending` is a CONFLICT — delivered may not skip
  shipped — so a webhook that arrives out of order or twice hits an error rather
  than converging.~~ **Corrected 2026-09-06, and the measurement is worth
  keeping: the module tolerated REPEATS and refused REORDERING.** The whole
  table was printed by a throwaway probe rather than read by eye — a second
  ship, deliver or cancel landed on a no-op, while pending + deliver and
  delivered + ship were both conflicts. The two out-of-order pairs converge now;
  see D24.

One provider ships: the manual one, which makes no network call and returns
whatever tracking number the caller passed it. There is no carrier plugin.

`MarkShipped` and `MarkDelivered` exist and are admin-scoped, and their godoc
already names the intended source: *"THE PROVIDER IS NOT CALLED: this method
records the fact the carrier REPORTED (a webhook or an administrator action)."*
~~**The word "webhook" appears exactly once in the entire non-test codebase — in
that comment.**~~ **Re-measured 2026-09-06: 131 occurrences in 11 non-test Go
files.** The inbound callback ring (ADR 0028) and the outbound sender (C5)
landed in between. What has NOT changed is the thing that sentence was really
about — an inbound carrier event still has no way into this module, which is now
recorded on the C6 row with the measurement behind it.

### ~~There is no inbound webhook machinery, and the one working callback is unguarded~~ — built 2026-09-05 (B1, D1)

**Everything below this heading was true when it was measured and the paragraph
that follows is kept for the argument it makes, not as a description of today.**
`CallbackRegistry` in the core HTTP package is the class this passage asked for:
a registered callback route gets a quota, a body limit, a timeout, a signature
check enforced before anything reads the payload, and a replay window derived
from the signed fields. PayTR was converted onto it at the same URL, and
`TestEveryStateChangingRouteIsGuarded` now fails a write bound outside the
guarded prefixes. What a carrier still lacks is not the door but the room behind
it — see the C6 row.

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
- **None of the twenty-eight ADRs covers privacy.** ADR 0008 governs the customer
  identity trust boundary, which is authentication, not data protection.
  Re-counted 2026-09-06: `docs/adr/` holds 0001 through 0028, and the three that
  landed after this bullet was written — the published surface, the composition
  root and the inbound callback ring — do not touch data protection either, so
  only the numeral moved.
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

- ~~**There is no retry and no dead-letter queue, by explicit decision.** A
  handler error is logged and the event counts as processed; the ADR-grade
  reasoning is the poison pill — redelivery without a DLQ lets one broken event
  lock the consumer. An outbound sender must therefore build its own retry.~~
  **Half wrong, and both halves were corrected on 2026-09-06.** The decision is
  real but it is about the BUS's subscriber side, and this bullet let it stand
  for the whole system — including the outbox relay, which is a different layer
  with a different failure mode. The relay now has both: a doubling backoff and
  a dead letter after ten attempts (B12). The poison-pill reasoning is exactly
  what it is built around — the pill is the reason the ceiling exists, and the
  dead letter is the DLQ whose absence made redelivery unsafe. An outbound
  sender still builds its own retry for what happens INSIDE its handler; what
  it no longer has to build is the retry of the delivery itself.

  The measurement that made this a defect rather than a preference: the relay
  reads the OLDEST pending rows up to its limit, so a limit's worth of
  permanently failing rows fills every batch. Against a real PostgreSQL with
  the previous code, five consecutive passes published nothing and a healthy
  event written behind two poisoned ones finished with `attempts = 0`. The
  backlog did not slow delivery down; it ENDED it.
- **Redis cannot fan out to N processes as configured.** Every subscriber joins
  one consumer group, and a group delivers each message to exactly ONE consumer.
  Different group names fan out; nothing in the repository uses different names.
- **Under Redis the handler context carries nothing** — no request id, no logger,
  no identity, no tracing span. Only the event id and its data cross the process
  boundary, so an outbound sender must read everything from the payload.

No NATS, Kafka or RabbitMQ appears anywhere, including `go.mod`.

~~And one structural blocker for shipping this as a plugin: **the plugin host
cannot register a job.** Its surface is Container, Logger, Setting, AddModule,
AddRoutes and four provider registrations — an outbound-delivery plugin could
mount a route but could not schedule its own retry pass.~~ **Corrected
2026-09-06: the blocker was removed (B13) and the plugin it was blocking now
ships (C5).** `plugin.Host.RegisterJob` exists, and the host's surface is
fourteen methods rather than nine — Container, Logger, Setting, AddModule,
AddRoutes, RegisterJob, Jobs, RegisterCallback, RegisterErrorReporter, Subscribe
and the four provider registrations. `plugins/webhookout` is exactly the
outbound-delivery plugin this paragraph called impossible: it mounts its route
AND schedules its own minute-by-minute delivery pass through that surface, and
`plugins/paymentpaytr`'s hourly `pendingWatch` was the first consumer.

The outbox is the right foundation and it is already there (ADR 0023): the event
is written inside the transaction that promises it, and a relay publishes it.
An external subscriber would hang off that relay — which, as of 2026-09-06,
retries with a growing delay and gives up out loud instead of retrying forever.
~~The one piece of that machinery an operator still cannot reach is the way
back: `Redrive` and `Discard` have no caller outside their tests.~~ **Reachable
since 2026-09-06:** `gobit deadletters` is their caller — a read-only listing
plus a redrive and a discard verb, each of which requires the event id to be
typed back with `-confirm`. The guard is a repeated ID rather than a `-force`
flag on a measured argument about what goes wrong: the mistake these verbs
attract is not "did not mean to discard", it is "meant a different id", and a
constant flag carries no information about the target while migrating into a
runbook line where it stops being a decision.

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
flag without a reader becomes — and the 2026-09-06 sweep found it has three
siblings in the same module, none of which a boolean-column count would have
separated from the thirteen that do have readers. The sweep's own false
positives are the lesson for a flag SUBSTRATE too: eleven of its fifteen naive
findings were response fields reporting an outcome to a client, which is a
different thing from a flag entirely, and a design that cannot tell the two
apart in its own storage will not be able to audit itself either.

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

### ~~Order timeline: the facts exist, scattered, and two of them are unreachable~~ — all four holes are closed, the last two on 2026-09-06

What an order itself records: `placed_at`, `completed_at`, `canceled_at` with a
reason, plus database CHECKs tying each stamp to its status. Returns carry
`received_at` and `canceled_at`; claims carry both transitions.

Four measured holes, and all four are now closed. The last two went on
2026-09-06; the first two went on 2026-09-05 and this section had not been
updated, which is the divergence class this file keeps producing:

- ~~**Archiving leaves no timestamp.** The status flips to `archived`,
  `completed_at` is deliberately untouched, and there is no `archived_at`. When
  an order was archived is not recorded.~~ Closed 2026-09-06 (D5). The stamp
  exists and the timeline emits a dated `order.archived` entry.
- ~~**`order_exchanges.completed_at` and `canceled_at` exist and are never
  written** — there is no Complete or Cancel query for an exchange at all.~~
  Closed 2026-09-06 (D4), by writing one and dropping the other.
- ~~**The money timeline is unreachable through the read layer.**
  `payments.captured_at` and the refund rows are the two facts a support team
  asks for first — "when was it paid", "when did the refund go out" — and there
  is no query provider that exposes them.~~ Closed 2026-09-05 (B6).
- ~~**There is no order↔fulfillment link, and nothing creates a fulfillment for an
  order.** The link definition was assigned to the fulfillment module, which
  never declared it. So "where is the parcel" cannot be answered from an order
  at all.~~ Closed 2026-09-05 (B5, D8).

**And the timeline's documentation described behaviour the code did not have.**
Its godoc said an undated fact — an exchange that finished — came back LAST
rather than being dropped; the entry builder emitted no such entry at all, and
the branch that would have produced it was unreachable. It has been corrected
and the branch is gone, replaced by a DATED withdrawal entry. The timeline had
zero tests of any kind when it was built on 2026-09-05, which is how a false
sentence survived a day in a file everybody had read. It has tests now for the
entry builder; the composed read itself still has none, because it needs a
wired query catalog the module's fake store cannot supply.

The audit log built this session does not close this: it records the REQUEST —
who called what and what came back — and says in its own header that it does not
record the change. It answers "who touched this" and not "what happened".

~~So the timeline is a read-side composition over facts that mostly exist, plus
two that do not: the order↔fulfillment binding, and a money-event surface.~~ The
timeline is a read-side composition over facts that ALL exist now. Both of the
two that did not were built on 2026-09-05, and the two stamps it was reading
around were built on 2026-09-06.

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
- **The metadata slot exists**: a `metadata` jsonb sits on ~~eleven~~ **ten
  (corrected 2026-09-06)** modules' models — auth, cart, customer, fulfillment,
  invoice, order, payment, product, promotion and tax — and inside product it
  reaches the variant and taxonomy tables as well, which are that module's own
  and were being counted as two more modules. What is missing is the form
  generator, not the field.
- **The extension points do not exist.** There is no `AddPage`, no widget slot,
  and the plugin host cannot add a panel page.
- **Coverage is five sections over four of sixteen modules** — catalog, orders,
  sales, customers, inventory — which is what an operator looks at daily. Sales
  (2026-09-05) is the first section that is not a module's own screen: it reads
  the order module's line entity, so it adds a section without adding a module,
  and the two counts have to be read separately from here on. The remaining
  twelve modules are configuration.

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

   **On 2026-09-06 that decision was taken all the way into the schema** (D4).
   Exchange completion is not merely unbuilt now, it is unrepresentable: the
   `completed` status value and the `completed_at` column are gone, because a
   state nothing can enter and no moment can date reads as a feature somebody
   forgot rather than a capability the framework does not have. The exchange
   did gain its first transition in the same change — WITHDRAWAL — with a
   route, a stamp and a mirror CHECK. The day the `order_payment` cardinality
   opens, `completed` comes back as a migration, which is a smaller change than
   leaving a dead state reachable-looking in the meantime.

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

   **A sales report landed (2026-09-05), and it is the fifth section.**
   `/admin/ui/sales` lists the LINES sold in a period, newest first, and it is
   the first consumer of the order module's line entity (B14). The period
   travels in the address bar rather than in a session, so the page is a URL an
   operator bookmarks and sends on. It costs two reads — the lines, then the
   orders behind them in ONE batch keyed by the order ids the lines carry —
   because the read layer joins across LINKS and two entities of one module are
   not linked to each other; the alternative was a read per row, which is the
   N+1 the read layer exists to prevent. The filter's upper bound is exclusive
   while the screen prints the INCLUSIVE last day, so what the operator typed
   and what the report covers cannot drift apart.

   It has no total and no per-variant summary, deliberately. The read layer
   cannot aggregate, so the only sum this screen could compute is the sum of
   whichever 25 lines sorted first — printed under a heading that says "Sales"
   and read as the period's takings. A wrong number an operator cannot see is
   worse than a missing one: a missing total sends somebody to write the query,
   a wrong one ends the question.

   Still open: the section count and the module count have come apart — five
   sections, still four modules, because Sales is the order module's SECOND
   screen rather than a new module's first. Twelve of the sixteen modules have
   no screen — all of them configuration (regions, tax rates, shipping options,
   promotions, keys) rather than daily work — nothing can be created or deleted
   from the panel, and there is no extension point for a plugin to add a screen.

### The rest

83 gaps in total; the full measurement is at
`.claude/jobs/*/tasks/wgday14jh.output` for as long as that job lives. The 22
written refusals are listed there too and must be read as decisions: customer
identity (ADR 0008), scheduled compensation (ADR 0017), order editing
(`order/models/models.go`), and a capability with no consumer (ADR 0009).
