# Capability inventory — measured 2026-09-04

Moved out of `docs/gaps.md` on 2026-09-08 unchanged. A measurement is
evidence, not a decision: it is kept in full and read when a number is
questioned, not on the way to understanding the repository.

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
