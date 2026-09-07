# ADR 0042 — The customer pays what the merchant receives, and a spread will arrive as a SETTLEMENT ROW rather than by relaxing that equality

- **Status:** Accepted
- **Date:** 2026-09-07
- **Phase:** after the roadmap

## Context

Gap A3 asks one yes/no question: may the customer pay a different amount than
the merchant receives? Two named future items need the answer to be yes. C7
wants installments with a vade farki — a surcharge the customer pays over
months and the merchant never receives. C18 wants a multi-vendor marketplace,
where a commission is taken out of what the shopper paid before a vendor is paid
out. `docs/gaps.md` records today's answer as no, held by "four independent
guards" including database CHECK constraints.

**The guards are real and the count has never been written out.** Commit 0537a93
repeats the figure with no list, so a reader loosening something in the payment
module cannot tell which four they are loosening. They are four LAYERS, and this
record enumerates them because a decision to keep them has to say what it is
keeping.

**One — the identity.** In `internal/workflows/checkout/plan.go` the plan's
`Amount` is assigned the computed cart total, the order snapshot that becomes the
order row takes its own total from that same `Amount`, and
`internal/workflows/checkout/authorize_payment.go` opens the payment collection
with that same `Amount`. So `orders.total` and `payment_collections.amount` are
NOT two numbers that happen to be equal — they are one number written into two
tables. Nothing reads the order ROW back and holds it against the collection,
because there is nothing to compare; the only comparison in the tree is layer
four's, and it checks the collection's amount against the same in-memory
`Amount` the order total was written from rather than against the order.

**Two — the service ceiling.** In `internal/modules/payment/service/session.go`,
`CreateSession` works out what is left to open as the collection's amount minus
what LIVE sessions already reserve, and refuses an amount above that remainder;
it also refuses to open any new session once the collection's captured amount is
above zero. `authorizedAmount` in the same file refuses a provider that reports
holding more than the session's amount. In
`internal/modules/payment/service/capture.go`, a capture above the session's
authorized amount is refused.

**Three — the database ceiling**, written into
`internal/modules/payment/migrations/000001_payment_init.up.sql` and described
there as the last defence, so that not even an edit made straight in SQL gets
past it: `payment_collections_authorized_le_amount`,
`payment_collections_captured_le_amount`,
`payment_sessions_authorized_le_amount`, `payment_collections_refund_le_capture`
and `payments_refund_le_amount`.

**Four — the saga floor.** `internal/workflows/checkout/authorize_payment.go`
fails the checkout when the amount held is BELOW the plan's amount, and
`internal/workflows/checkout/capture_payment.go` fails it when the captured
amount is below the plan's amount and again when the collection's own amount has
DRIFTED from the plan's. The first three layers are ceilings; this one is the
floor, and the pair of them is what makes an equality rather than a cap.

**The equality is one-directional outside the saga, and gaps.md does not say
so.** The admin route registered in `internal/modules/payment/api/api.go` posts a
capture carrying an optional amount, and the check in
`internal/modules/payment/service/capture.go` bounds that amount only ABOVE;
`payment_collections` carries `partially_captured` as a first-class status,
computed from the amounts rather than asserted by a caller. So an operator can
make the merchant receive LESS than the order says, outside the checkout saga
entirely. This is still not a spread — the customer is simply charged less — but
"forced EQUAL" is true as a ceiling everywhere and as a floor only inside the
saga, and the sentence in gaps.md saying the equality "is enforced in the schema"
is loose in the same way: the enforcement is the CHECK constraints PLUS the
identity above, and a schema constraint could not do it alone because the two
numbers live in two modules' tables.

**One place looks like it already admits a spread, and it is the wrong shape.**
`internal/modules/order/service/summary.go` deliberately declines to compare the
paid total with the order total, and its godoc argues the case: overcollection is
a real fact — an exchange-rate difference, a correction on the provider's side —
and rejecting it would mean being unable to record a collection that really
happened. The measurement confirmed by hand that nothing refuses it: an order
total of 1000 beside a paid total of 1100 was accepted with no constraint firing.

That tolerance is not A3's difference, twice over.

- **It has the opposite sign.** The order summary's `Outstanding` returns the
  excess as a NEGATIVE, and `internal/modules/order/models/erasure.go` names that
  branch as money the shop is holding OVER the value of the sale, owed back to
  the customer. A vade farki is money the merchant never holds at all. A
  surcharge recorded there would make the shop's own ledger claim it owes the
  customer a sum it never received.
- **It is unreachable through the code anyway.** `SetOrderSummaryTotals` gets no
  HTTP route, which `internal/modules/order/api/api.go` states twice, and the
  service method has exactly ONE caller outside tests: the four-argument wrapper
  of the same name in `internal/modules/order/service/interop.go`. Two workflow
  steps call that wrapper — `recordPaymentTotals` in
  `internal/workflows/checkout/clear_cart.go` and `recordRefund` in
  `internal/workflows/returns/refund.go` — and both pass the collection's own
  captured figure straight through. That figure is capped by
  `payment_collections_captured_le_amount` at the collection's amount, which is
  the same number as the order total, and the write merges with `GREATEST` in
  `internal/modules/order/repository/orderdb/order_summaries.sql.go` rather than
  adding, so repeated or late reports cannot accumulate past it either. The
  tolerance is a stated defensive posture for a subscriber that does not exist —
  `internal/modules/order/service/interop.go` says as much, naming "one day a
  subscriber fed by an at-least-once bus".

**The provider contract cannot carry the question either.**
`core/provider/payment.go` carries every amount as a bare `int64`: `Session` has
one, `AuthResult` has one, and `SessionInspection` has THREE — authorized,
captured and refunded. Five scalars, and not one list; the contract has no list
type at all, so a provider has no way to answer "these are the three, six, nine
and twelve month options and their totals". `core/provider` is on the published
surface (ADR 0026), so whatever shape were chosen there is a promise kept
forever.

**And there is no consumer.** No installment, BIN, card-bank, vendor, commission
or payout concept exists anywhere under `internal/` or `core/`. The nearest thing
in the tree is an observation in `internal/modules/invoice/module.go` that on a
marketplace the SELLER is a different natural person on every invoice — a note
about who the data subject is, not a money model.

## Decision

**NOT NOW. The customer pays what the merchant receives, and the four layers stay
four layers.**

**When the first consumer arrives — an installment provider, or a marketplace —
the difference arrives as a SEPARATE SETTLEMENT ROW, not by relaxing the
equality.** The sale keeps one amount. The surcharge or the commission becomes a
second money row with its own counterparty, carrying its own amount and
reconciled on its own, so that `orders.total` and `payment_collections.amount`
remain the single number they are today and every ceiling above keeps meaning
what it means.

**The first consumer decides that row's shape.** This record fixes the DIRECTION
and refuses the other one. It deliberately fixes no table name, no column list
and no cardinality, because the facts that would decide them — whether a bank's
installment table is per BIN or per campaign, whether the surcharge comes back as
a rate or as a set of totals, whether the provider or gobit computes it — arrive
with the provider and not before.

**The route is measured rather than assumed, so the deferral is not a bet on
feasibility.** The row multiplicity a settlement row needs already exists in
`internal/modules/payment/migrations/000001_payment_init.up.sql`:
`payment_sessions_collection_idx` and `payments_collection_idx` are plain
non-unique indexes, so one collection can already carry several sessions and
several payments; `refunds_payment_idx` is a plain btree, so a payment already
carries many refunds; and `payment_sessions.provider_id` is a party identifier
sitting on a row that carries money, per session, so two sessions on one
collection can already name two different parties. Nothing in the schema ties the
sum of the payment rows to the collection's amount. A settlement row can
therefore sit beside `payment_collections` in the payment module — same-module
foreign key, no boundary crossed — and reach the order the way this module
already reaches it: through the link registry, where
`internal/modules/payment/service/links.go` declares `LinkOrderPayment` and the
module applies the definition at startup rather than in a migration (ADR 0005).
That route is sanctioned in the header of `internal/arch/module_sql_test.go`,
which states that a module's SQL may name a table NO module owns and gives the
link tables as exactly that case. `TestModuleSQLNamesOnlyItsOwnTables` fails over
a different thing — a module naming ANOTHER module's table — and its failure
message offers the two ways out of THAT: the owning module's interop surface
resolved from the container, or the cross-module read layer in `core/query`. A
settlement row needs neither, because it names nothing outside the payment
module. The link that would have to widen has its reopening PRE-WRITTEN: the same
file declares the order-to-collection link one-to-one and says in the same
comment that an exchange whose difference is positive is money collected against
an existing order, and "that day this becomes OneToMany and nothing else
changes".

## Rejected alternatives

**Relax the equality now — give the collection a surcharge column and let the two
amounts differ.** It buys the smallest diff there is: one column, a second number
for the saga floor to compare against, and C7 stops being blocked in the money
model. It is killed by what the four layers are. They are not four opinions about
one rule that could be updated together; three of them are ceilings and the
migration calls them the last defence against an edit made straight in SQL.
Relaxing the equality means every one of them needs a second number, and that
number would have to be invented before any provider has said what it is. A
ceiling widened for a feature nobody has built is a ceiling that is not there on
the day a bug arrives, and the bug it would not be there for is charging a
customer more than the order says.

**Use the order summary's existing tolerance and call the excess the spread.** It
looks free — nothing refuses a paid total above the order total, the godoc
already argues for the tolerance, and no migration would be needed. It is killed
by the sign. That excess is defined as a debt owed BACK to the customer, returned
as a negative outstanding amount and read by the erasure path as an unsettled
fact; a vade farki is the reverse, money the merchant never holds. Writing one
into the other's field would not be a shortcut, it would be a ledger that lies
about which way the money is owed.

**Publish the quote type on the provider contract now, and leave the rest for
later.** It buys the half of C7 that is genuinely blocked rather than merely
absent: every amount on `Session`, `AuthResult` and `SessionInspection` is a
bare `int64` and the contract has no list type, so there is no return path for an
installment quote at all, and no provider could be written even if the money
model were ready. It is killed by ADR 0026, and by what 0026 actually says rather
than by a version threshold: `core/provider` is published, the warning 0026
carries forward from ADR 0025 is that a published name cannot be taken back AT
ALL, and 0026's own consequence prices the alternative — fourteen packages are a
promise, and /v2 is a real cost. The shape of an installment quote is exactly the
thing this repository currently cannot know, and guessing it buys a permanent
surface for a guess.

**Reserve a whole settlement MODULE now, so the payment module's arithmetic is
never touched.** It buys isolation and it is the tidy-looking answer. It is
killed on two counts. It decides more than the question asks — a module is a
migration, a container registration, an interop surface and a describe loop, all
committed before a consumer exists to say whether the row belongs beside the
collection or somewhere else. And the measurement says it is not even the cheaper
route: the multiplicity is already in the payment module's own tables, so the row
has a home that crosses no boundary.

**Answer YES now and build the general multi-counterparty model, since C18
changes what every money path means anyway.** It buys doing the disruption once
instead of twice. It is killed by proportion: gaps.md puts C18 last on purpose,
and building n counterparties per order to serve a case with ONE merchant and one
surcharge would put a counterparty column on every money row in every shop that
will only ever have one. The general model is also the one most likely to be
wrong, because it is the one furthest from a consumer.

## Consequences

**Positive**

- **The ceiling holds in every direction that matters.** No route in this tree
  lets the customer be charged more than the order says: the identity, the
  service ceiling, the database ceiling and the saga floor were each read and
  named here rather than counted.
- **The four guards are enumerated for the first time.** A reader changing the
  payment module now has the list that commit 0537a93 asserted the size of.
- **The published surface is not spent.** `core/provider` carries no type
  invented for a feature nobody has built, and the promise ADR 0026 makes stays
  cheap.
- **The path is open and its obstacles are named.** The row multiplicity exists,
  the link's relaxation is already argued in the code, and reconciliation is
  per-session — so a settlement session would reconcile against its provider
  without reconciliation needing a new idea.

**Negative, and accepted**

- **C7 stays blocked, and it is the item this market expects most.** Installments
  are ordinary in Turkey, and this decision says plainly that gobit does not do
  them yet — not that they are hard, but that no provider has arrived to say what
  the quote looks like. A shop that needs them cannot use gobit for them today.
- **A settlement row will be more than an ALTER, and this is the correction this
  record makes to its own ground.** The schema permits many sessions and many
  payments per collection, but `CreateSession` refuses a second session once live
  sessions reserve the whole collection amount, and refuses any new session at
  all once the captured amount is above zero. So the code path that would open
  the settlement session refuses it TODAY. Naming the row a kind is not enough:
  the remainder calculation in `internal/modules/payment/service/session.go` has
  to learn which rows count against the sale's amount and which do not, and that
  is a change to the layer this decision exists to protect.
- **The equality this record defends is asymmetric and the record does not fix
  it.** An operator can capture less than the order total through the admin
  route, and the collection will say `partially_captured`. That is left standing
  because the alternative is refusing to record a partial capture a provider
  genuinely performed — but it means "the customer pays what the merchant
  receives" is a ceiling with a floor only inside the saga, and a reader who
  takes it as symmetric will be wrong.
- **The count is held by a document, not by a test.** Nothing fails if a fifth
  layer appears or a fourth is quietly removed. Enumerating them here is better
  than a bare figure in a commit body, and it is still weaker than the audits this
  repository holds its other invariants with — which is the same weakness ADR
  0038 wrote down about its own six copies.

## What this deliberately does NOT do

- **It does not design the settlement row.** No table, no columns, no cardinality
  and no decision about whether the counterparty is a vendor id, a bank, or the
  provider itself. Those are the first consumer's to answer, and a reader should
  not treat the direction fixed here as a schema.
- **It does not forbid a partial capture.** The one-directional asymmetry above
  is a described state, not a change; nothing in this decision touches the admin
  capture route or the `partially_captured` status.
- **It does not close the overcollection tolerance in the order summary.** The
  outstanding amount may still go negative, and that stays a debt to the customer
  — a different fact from the one A3 asks about, kept for the reason its own
  godoc gives.
- **It does not say gobit will never carry two amounts.** It says the model does
  not admit the spread today, that the direction when it does is a row rather
  than a loosened constraint, and that the arrival of a consumer is what starts
  the work.
- **It does not add the audit its own enumeration wants.** The four layers stay a
  written list, and turning them into something that fails when one goes missing
  is a separate piece of work with its own record.

## Related

- [ADR 0022](0022-the-saga-records-what-was-collected.md) — why the order records
  the payment module's OWN figure rather than the amount the saga intended, which
  is what makes the paid total capped rather than free.
- [ADR 0021](0021-the-server-decides-the-shipping-price.md) — the server decides
  what money is, not the client; a surcharge quoted into a request body would be
  the same defect in a new place.
- [ADR 0026](0026-the-published-surface-is-fourteen-packages.md) — the promise
  that makes publishing an installment quote type expensive.
- [ADR 0020](0020-reconciliation-asks-the-provider-and-reports.md) — the
  per-session comparison a settlement row would inherit unchanged.
- [ADR 0001](0001-modul-arasi-iletisim.md) — the module boundary that puts the
  settlement row beside the collection and reaches the order through the link
  registry.
