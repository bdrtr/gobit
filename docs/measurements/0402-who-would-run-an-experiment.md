# Who would run an experiment — measured 2026-10-05

The evidence behind [ADR 0402](../adr/0402-an-experiment-runs-outside-gobit.md).
Taken on the tree at `97d25ee2` (ADR 0400, D259). Files are named with
`grep -rl`; no line numbers are given, because they move.

## 1. Nothing in the tree assigns, exposes or stops

`grep -rli` over the `.go` and `.sql` files under `core`, `internal`,
`contrib`, `plugins`, `examples` and `cmd`:

| Term | Files | What they are |
|---|---|---|
| `abtest`, `feature flag`, `featureflag`, `mSPRT`, `stopping rule`, `visitor_id`, `anonymous_id` | none | — |
| `experiment` | `internal/modules/product/queries/variant.sql` and its generated `productdb/variant.sql.go` | a comment: a schema reader would learn a rule "by experiment" |
| `a/b` | `core/plugin/plugin_test.go`, `core/db/db_integration_test.go`, `core/db/testdata/rollback/000001_rollback_init.up.sql`, `internal/arch/build_files_test.go` | paths and regexps |
| `exposure` | `internal/workflows/returns/exchange_funding.go`, `internal/core/config/config.go` and its test and validator, `internal/app/setup.go` | the risk a refund, a session lifetime or a setting carries |
| `sequential test` | three integration tests | a test that runs its steps in order |
| `visitor` | `internal/e2e/authorization_matrix_test.go`, `internal/adminui/session.go`, `internal/arch/storefront_schema_test.go`, `plugins/analytics/plugin.go` | prose and a column-name example; no key |

No table, route, topic or package names an arm, a variant assignment or an
exposure. `plugins/analytics` counts a daily cart funnel by region and keys
nothing on a visitor.

## 2. There is no visitor key to draw an arm from

- **The publishable key names a channel.** `core/http/auth.go` and the
  `publishableScheme` description in `core/openapi/openapi.go` say it is not a
  secret and binds a request to a sales channel (ADR 0008). Every browser of a
  storefront holds the same key.
- **The one working identity proves a signed-in shopper.**
  `contrib/identity-session` signs a cookie for a customer who logged in
  (ADR 0127) and answers whom it proves (ADR 0366); an anonymous visitor gets
  nothing from it.
- **gobit issues no customer identity** (ADR 0043), which names itself the
  upstream of A/B assignment.
- **Consent is the embedder's.** ADR 0029 leaves the lawful basis and the
  consent text to the embedding application; a key that follows an anonymous
  visitor across visits is a tracking identifier under that rule.
- **The 2026-09-05 sweep said the same.** `docs/measurements/platform-features.md`
  records that A/B needs a stable per-visitor key, that there is no visitor,
  and that assignment would have to come from the embedder.

## 3. What an experiment product can already count

| Fact | Where | Carries |
|---|---|---|
| `cart.created`, `cart.completed` | `cartEventPayload` in `internal/modules/cart/service/events.go` | `cart_id`, `region_id`, `currency_code`, `occurred_at` |
| `order.placed` | `orderPlacedPayload` in `internal/modules/order/service/events.go` | `order_id`, the total, `customer_id`; no cart id |
| The completion response | `completeCartDTO` in `internal/modules/cart/api/store.go` | `order_id`, `cart_id`, `total` together |
| The order read | the order DTO in `internal/modules/order/api/api.go` | `cart_id` beside the total |

So a guest's conversion joins on the bus by cart id, and its revenue joins
only by reading the order or keeping what the completion answered. `cart_id`
on `order.placed` would close the bus gap; `cart.completed` cannot gain an
order id alone, because `cartEventPayload` builds both cart events.

## 4. A price experiment is already expressible, and is the merchant's

`POST /admin/v1/customer-groups/{id}/customers`
(`internal/modules/customer/api/api.go`) puts a customer in a group, and a
price ruled on that group charges them: `internal/e2e/segment_test.go` charges
a group's member the group's list price and a non-member the base. An embedder
that fills groups by arm runs a price experiment through the merchant's own
ladder; the order line records the price row it was charged (ADR 0168).

## 5. The consumer

Feature row C10 names its trigger as the first checkout variation experiment.
No module, plugin or example in the tree varies a checkout by anything a
visitor carries, and no panel screen shows an arm's result.
