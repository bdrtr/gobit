# Must-have commerce features — measured 2026-09-04

Moved out of `docs/gaps.md` on 2026-09-08 unchanged. A measurement is
evidence, not a decision: it is kept in full and read when a number is
questioned, not on the way to understanding the repository.

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

   **And for two days it was a table nothing read.** `Store` had one method and
   it was `Write`, while FOUR other places in this repository cite this very
   table as "the write-only ledger we have already built once" — the outbox, the
   webhook plugin's migration and module, and the relay job. The lesson was
   applied everywhere except where it was learned. Closed properly on 2026-09-07
   by ADR 0037: `Store.List`, `GET /admin/v1/audit-log` behind `audit:read`,
   keyset paging, and a THIRD index — the two the migration shipped both lead
   with another column, so the question an operator asks first ("what happened
   most recently") had no index at all and was a sequential scan of the whole
   log. Measured with EXPLAIN rather than asserted, because this repository has
   already shipped a godoc claiming "the index is used" that turned out false.

   The decision inside the decision: **reading the log is recorded.** The rule
   that excludes reads exists because "somebody listed the orders answers no
   question"; that reason does not survive contact with this one path, which is
   the read an intruder makes. The exception is a list of EXACT paths with one
   entry, because its cost is exactly its breadth.

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
