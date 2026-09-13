# A name that was already taken — measured 2026-09-13

The evidence behind [ADR 0164](../adr/0164-a-capture-earns-the-customer-points.md).
The feature list's row A7.7 read, in full:

> `[ ]` A7.7 Loyalty points — "Nothing exists. The first slice is the EARN half
> only: a `loyalty` module, an only-appended `loyalty_transaction` table, and a
> subscription to `order.placed` writing points at a single rate" — promotion/new

(The list is kept in Turkish; the row is translated here because this file is
not.)

Every noun in it was measured against the tree. Three of them did not survive.

## 1. "Nothing exists" is false on the two attributes the row claims

`examples/starter/loyalty` is a module whose `Module.Name` returns `"loyalty"`
and whose `Module.Routes` binds `GET /store/v1/loyalty/balance`, described by its
`Module.Describe` as "The customer's loyalty point balance" and answering
`{"points": 0}`. It is a real module in a separate Go module
(`example.com/gobit-starter`), added to a `gobit.New()` app in
`examples/starter/main.go` beside gobit's own eighteen.

So the NAME and the ROUTE ADDRESS exist; the behaviour does not. The row priced
the feature and read that as pricing the name.

`module.Registry.Bootstrap` calls `validateNames` FIRST and it returns
`errors.Conflict("module_name_duplicate", …)` — before `mountRoutes`, so this is
not a routing question and chi never sees it. For the record, chi 5.3.2 would
not have complained either: its `tree.go` insert is "insert or update" for a verb
method, and the only duplicate-path panic in `mux.go` is `Mount`'s.

### What no lane would have said

`TestTheOutOfTreeStarterRuns` runs the starter with `go run . help`, and
`internal/app`'s command switch answers `help` from `usageText` before a registry
exists. The only non-test caller of `Registry.Bootstrap` is `openApplication`.
`examples/starter` carries no test files, so `make test-modules` runs nothing
there.

One lane WOULD have gone red, on a different subject: the root's own
`TestTheInstallationCarriesTheEmbeddersOwnModule` boots `gobit.New().Add(&loyaltyModule{})`
whose `Name` returns `"loyalty"` — behind `//go:build integration`. The same fake
name is used by `TestTheCallersModulesComeLast` and by the facade's own test.

### What freeing the name would have cost

`examples/starter/loyalty` is named in ADR 0035 (three times), ADR 0043, ADR
0045, `docs/measurements/0127-where-a-session-package-belongs.md`,
`core/personaldata`'s package godoc and `internal/arch/route_claims_test.go`'s
own godoc. Three of those records are ≤0051, which the working agreement
freezes. No gate resolves any of them: `rootedPathReference`, the regular
expression both path-reference audits share, holds cmd, core, internal, plugins,
docs, config, deploy and migrations — not `examples`. So the citations would have
rotted silently.

## 2. `order.placed` cannot carry an earn rule

| Fact | Where |
|---|---|
| Published at step 2 of a 5-step saga, before authorize and capture | `internal/workflows/checkout`'s package godoc |
| `status` is the constant "pending" — the only publish site is `CreateOrder`, and `writeOrder` writes `OrderPending` | `order/service` |
| `customer_id` is documented "empty on a guest order" | `order/service.EventFieldCustomerID` |
| `total` is GROSS (subtotal − discount + tax + shipping) and the payload carries none of the four parts | `order/models.Order.Total` |
| `CancelOrder` publishes nothing, and no `order.canceled` topic exists | `order/service.Service.CancelOrder`, `plugins/webhookout.ForwardedTopics` |
| Delivered TWICE per order in normal operation: the direct publish never marks the outbox row, and the relay runs unconditionally every minute | `order/service.publishOrderPlaced`, `core/eventbus/outbox.Relay`, `internal/jobs/outboxrelay` |

The two scenarios this repository executes end to end create no customer at all:
`docs/first-run.md` says "Nothing here creates a **customer**. The cart is a
guest's", and `examples/storefront/README.md` is "A guest storefront … No
customer." A ledger written from this event would have been keyed on the empty
string in both of them — the failure ADR 0153 refused by name.

## 3. The earn half alone is invisible to every gate

`TestEveryColumnIsWrittenBySomething` audits that every column is WRITTEN and
says so in its own godoc; nothing audits that a table is READ. ADR 0063 already
recorded the same hole for the movement ledger: "a table with an internal reader
and the event gate never touched it".

The five gates in `internal/arch/consumers_test.go` are all structurally blind
here. `order.placed` already has three subscribers that chose it — the
notification module, `plugins/webpush` and `plugins/analytics` — so a loyalty
handler would have been the fourth reader of a satisfied topic, and
`TestEverySubscribedTopicHasAPublisher` passes because the publisher exists.

## 4. What the payment module already holds

`payment_collections` carries, on one row: `customer_id` (nullable, added by the
module's migration 000004), `currency_code`, `captured_amount` and
`refunded_amount` — and migration 000001 says the three amounts "are updated
under the collection row's lock".

No other module can read that customer. The payment query entity publishes
`reference`, `amount`, `currency_code`, `status`, the three amounts and four
timestamps, and no customer at all (`payment/service.NewQueryProvider`). Reaching
it from outside means widening a published name promised until 1.0.0 (ADR 0026)
or walking the `order_payment` link backwards, which is what the order module's
`HandleMoneyMoved` does and which finds nothing for a capture with no order.

Three shipped artifacts already name loyalty points as the next tender of this
shape: the payment module's migration 000004, `payment/models`' comment on the
collection's customer ("store credit today, loyalty points tomorrow") and the
PUBLISHED `core/provider.CreateSessionInput.CustomerID` ("store credit, loyalty
points, an account on terms"). None of them is a decision; all three are
anticipations, and this record is the decision they were anticipating.

## 5. The choke point

`payment/service.Service.writeCollectionTotals` is the ONLY function that writes
a collection's totals. Six production callers reach it — two in `capture.go`
(`CapturePayment`, `RefundPayment`) and four in `session.go` — and the four in
`session.go` move only the AUTHORIZED figure, so they compute the same target and
append nothing.

Every caller is already inside a transaction with the collection row locked
(`lockCollectionAndSession`), and the function's own call to
`UpdatePaymentCollectionTotals` returns the row with `RETURNING *`. The earn path
therefore reads the row the database now holds rather than the pre-write snapshot
the callers pass, which is where a stale-read defect would otherwise have lived:
the callers hand in `col.CapturedAmount + captured`, so `col` itself is one write
behind by construction.

## 6. The arithmetic

target = (captured − refunded) × rate ÷ 10000, truncating toward zero, and the
row written is target − (what this collection already has).

| Sequence | captured | refunded | target @ 100 bp | row appended |
|---|---|---|---|---|
| capture 10000 | 10000 | 0 | 100 | earn +100 |
| the same event again | 10000 | 0 | 100 | none |
| refund 2500 | 10000 | 2500 | 75 | reverse −25 |
| refund the rest | 10000 | 10000 | 0 | reverse −75 |
| capture 999 at 100 bp | 999 | 0 | 9 | earn +9 |
| capture 99 at 100 bp | 99 | 0 | 0 | none |

The ceiling is 10 000 basis points — one point per minor unit — and a rate above
it is REFUSED rather than clamped. The tree does both elsewhere: promotion's
`percentageOf` clamps, while `region/models` and `tax/models` reject. Clamping
here would let a shop that asked for two points per minor unit pay out at one and
never hear about it. The bound also keeps the multiplication inside int64: money
is capped at `payment/models.MaxAmount`, one trillion minor units, so the product
stays at 10^16 against a limit of roughly 9.2 × 10^18.

The ceiling has two homes — `config.MaxLoyaltyEarnBasisPoints` and
`payment/service.MaxLoyaltyEarnBasisPoints` — because the core may not import a
module (Principle 2.4). They are bound by
`TestTheLoyaltyEarnCeilingAgreesWithThePaymentService`, which is the instrument
the GraphQL limits already use for the same shape. The repetition is deliberate
and the assertion is what keeps it from becoming the defect this repository has
recorded four times.

## 7. The gates, and the four mutations that proved the new one

`TestEveryLoyaltyPointWriteGoesThroughTheCollectionTotals` is
`TestEveryPhysicalStockWriteGoesThroughTheLedger`'s instrument pointed at a
second ledger. It needs THREE chained entries where inventory needs one, because
the payment repository does not name its methods after its queries: the chain is
`InsertLoyaltyEntry` → `AppendLoyaltyEntry` → `earnLoyaltyPoints` →
`writeCollectionTotals`. With only the far end named, a new repository method
calling the generated query would reach the table untouched.

| Mutation | Result |
|---|---|
| `s.store.AppendLoyaltyEntry` added inside `CapturePayment` | FAIL: "calls AppendLoyaltyEntry from CapturePayment, and the only function allowed to is earnLoyaltyPoints" |
| the choke point renamed in the map | FAIL: "no function of that name exists in the production source" |
| `UPDATE payment_loyalty_entries` added to the query file | FAIL: "a query says \"update payment_loyalty_entries\"" |
| the query file removed | FAIL: "no query names payment_loyalty_entries, so this audit read nothing" |

The append-only half MOVED rather than being copied.
`TestKrediDefteriGuncellenmez` lived in the payment module's integration test,
behind the integration build tag, and read ONE path by name:
`os.ReadFile("queries/payment_store_credit.sql")`. The second ledger's query file
would have been invisible to it on the day it was added. It had no floor either —
a renamed file made `assert.NotContains` read an empty string and pass. The
replacement, `TestThePaymentLedgersAreAppendOnlyInSQL`, has the DIRECTORY for its
subject, names both tables, carries the floor, and runs in the fast lane.

One detail cost a second attempt: the floor was first written as
`require.Contains`, which prints the HAYSTACK — and the haystack is the payment
module's entire query directory, thirty-nine kilobytes of SQL. The gate's own
godoc refuses exactly that, so it became `require.True(strings.Contains(…))`.

A contrived mutation also survived and is worth recording, because it is the
shape of the rule rather than a defect in it: renaming the table to
`payment_store_credit_entriesZZ` left the floor green, since the new string
CONTAINS the old one. Renaming it to `pmt_credit_rows` fails correctly. A text
scan cannot tell a prefix from a name.

## 8. The setting that reached nothing, and the gate that did not see it

The wire from an environment variable to the module it configures was audited by
nothing, and it was measured rather than reasoned about: deleting one line from
the composition root left the field on `config.Config`, its `.env.example` entry
and its validation rule in place, and `go test ./internal/arch/ -count=1` stayed
green. An operator could have set the rate and earned nothing, with no error.

`TestNoVariableInTheEnvExampleIsOrphaned` already writes the right sentence — "a
variable that stands in the document but nobody reads promises the operator a
knob that does not work" — but its idea of a reader is a FIELD on `config.Config`.
The hop after that had no gate. `TestEveryConfigurationFieldReachesTheApplication`
is that hop: of the fifty-three fields, fifty-two are selected somewhere in the
production tree and one, `AppPort`, is reached through `Config.Addr` and is
recorded as such.

The first version of the gate did not work, and the way it failed is worth the
paragraph. It matched the field NAME wherever it was selected — and the
composition root writes `LoyaltyEarnBasisPoints: cfg.LoyaltyEarnBasisPoints`
while the module writes `m.opts.LoyaltyEarnBasisPoints`, so deleting the line the
gate exists to protect left the name selected on the OTHER SIDE OF THE SAME WIRE
and the gate green. It is the audited-population defect in its purest form: the
sentence said "reaches the application" and the measurement was "this identifier
appears". The repair reads the selector's BASE, and only a base holding a
`config.Config` counts. Proved by putting the defect back in.

One honest limit stays: the gate asks whether a field is read AT ALL. Removing
`cfg.StorefrontTrustsUnverifiedCustomerClaim` from the payment module's options
leaves it green, because the cart and b2b modules still read it. That was
measured too.

## 9. Two more sentences that had gone stale

Both were found by reading the file the new endpoints had to be added to, and
neither has a gate — the route-address audit needs a method word before an
address, and the count audit needs the population's path on the number's line.

The four sentences saying what each payment scope OPENS enumerate resources, and
store credit's three endpoints have been missing from all four since ADR 0152
(D111). They now name the surface instead.

And the collection listing's own description opened with "This is the ONLY
endpoint in this module that reads the query string" — false since the same
record, which added two endpoints that read `customer_id` and `currency_code`.

## 10. What this slice does not close

- Points are not spendable. The tender is a provider in this module and is not
  written.
- A customer cannot read their own balance; the identity bill ADR 0152 declined
  is still unpaid.
- `payment_loyalty_entries.customer_id` is declared to `storefrontStoredClaims`
  as `limbConfined` and to `core/personaldata` not at all — because the payment
  module declares nothing there, and `customer_id` is on neither person-column
  audit's list. That silence is inherited rather than introduced, and it is the
  same silence `payment_store_credit_entries` sits in.
