# What a shopping agent already reaches — measured 2026-10-05

The evidence behind [ADR 0400](../adr/0400-a-shopping-agent-is-a-storefront-client.md).
Taken on the tree at `8339896c` (ADR 0398, D256). Files are named with
`grep -rl`; no line numbers are given, because they move.

## 1. What a client that is not a browser reaches today

- **The document.** `/openapi.json` is bound by `internal/app/app.go` outside
  both guarded prefixes, listed among the quota-only prefixes in
  `internal/app/guards.go`, and declared open in `openRoutes` of
  `internal/e2e/authorization_matrix_test.go`.
- **Every route under `/admin/v1` and `/store/v1` is in it.** `included` in
  `core/openapi/openapi.go` admits only those two prefixes, so the document
  itself, `/files`, the panel, the callback ring, health and readiness, and any
  root GET a module binds stay out of it. Within them
  `internal/e2e/testdata/undescribed_routes.txt` holds no entry, and
  `TestEveryRealRouteIsDescribed` refuses one that is not described and not
  listed.
- **Every operation is named.** `Doc.operation` in `core/openapi/openapi.go`
  fills an empty `OperationID` with `operationID(method, path)`. Deleting that
  fill empties the id of all 418 operations the e2e ground serves.
- **Every store operation names the key.** `security` in the same file answers
  `publishableKey` for a path under the store prefix, and the scheme is an API
  key in a header. The 64 store operations of the served document all carry it.
- **A second read surface sits under the same key.** The GraphQL storefront
  (`internal/modules/product/graph/graph.go`, `Path`) is mounted under
  `/store/v1`, so `RequireStore` in `core/http/guard.go` guards it like every
  REST read (ADR 0225, ADR 0226).

## 2. What holds each bound today

| Bound | What holds it | Where |
|---|---|---|
| The provider's charge | the credential in `payment_data`, against the collection's remainder | `storeCompleteCart` in `internal/modules/cart/api/store.go`; `OpenSessionWithData` in `internal/workflows/checkout/authorize_payment.go` and `internal/modules/payment/service/interop.go` |
| The store session's amount | nothing the client sends; a body naming one is refused | `createStoreSession` in `internal/modules/payment/api/handlers.go` |
| A customer's store credit and points | holding the cart id | `PayFirstWith` in `internal/modules/cart/api/store.go` (ADR 0269) |
| A gift card | holding the code | `GiftCardCode`, same file (ADR 0209) |
| An offline method | nothing; the order is placed owing | `internal/modules/cart/api/describe.go` (ADR 0284) |
| Who the customer is | the bound `corehttp.Identity`, asked by `provenCustomer` where a body names one | `internal/modules/cart/api/api.go`, `core/http/identity.go` |
| The sales channel | the publishable key's channels, carried into the plan as `SalesChannelIDs` | `internal/modules/cart/api/store.go` |
| A company's spending | the B2B limit, under the customer lock in the order write | `spendingRuleFor` in `internal/modules/order/service/order.go` |

Completion asks no identity: `storeCompleteCart` never calls `provenCustomer`,
whose only callers are the cart's creation and update. A cart id is a
capability (`docs/known-limits.md`, "Storefront carts carry no ownership
check"). When the balances cover the total, `authorize_payment.go` returns
before any provider session opens, so no delegated credential is consulted.

## 3. Where each part of C6 would sit

| Ask | Where it would sit | What it meets |
|---|---|---|
| A machine-readable policy | a policy read under `/store/v1` | the enforced facts are store reads already: regions (`internal/modules/region/api/api.go`), shipping options (`pathStoreOptions` in `internal/modules/fulfillment/api/api.go`), payment providers (`pathStoreProviders` in `internal/modules/payment/api/api.go`) |
| Return terms | the same read | `CreateReturn` in `internal/modules/order/service/aftersales.go` takes no window; a return is a request an operator decides (ADR 0051) |
| The seller | a storefront read of the profile | `internal/modules/settings/api/api.go` refuses a store namespace by design |
| A delegated credential | `corehttp.Identity` or a contract beside it | gobit issues no customer credential (ADR 0008, 0043, 0057); `contrib/identity-session` is the working one (ADR 0127) |
| A per-agent limit | the store limiter | `RateLimit` in `core/http/ratelimit.go` runs before `RequireStore` and keys on the client's address: the installation sets `LimitKey` to `clientKey` in `internal/app/setup.go`, which is `TrustedProxyIPKey` in `core/http/ratelimit.go` |
| A per-agent audit row | the audit ring | `Audit` in `core/http/audit.go`, scoped to the admin prefix by `core/http/guard.go` |
| Discovery | a root GET | a module may bind one, as `examples/storefront/storefront/storefront.go` does; `internal/arch/callback_test.go` refuses only state-changing routes at the root |

## 4. The first slices that were considered

- **A policy read.** Its enforced facts are readable today; return terms and the
  seller are promises nothing in the tree keeps.
- **A storefront read of the profile.** No storefront prints the seller, and the
  profile carries the tax number.
- **Expiry, budget or category on `api_key`.** The publishable key is public and
  names a channel (the `api_key` table in
  `internal/modules/auth/migrations/000001_auth_init.up.sql`); it delegates
  nothing.
- **A limit keyed by the publishable key.** Anyone reading the page source would
  spend the channel's quota.
- **A mandate contract now.** Nothing issues one and nothing asks for one.

## 5. What the earlier records said otherwise (D259)

- **"A tool call goes through the audit log."** ADR 0161, the `Handler` field of
  `internal/mcp/server.go` and `runMCP` in `internal/app/mcp.go`. A GET is
  recorded only when its path is in `AuditOptions.ReadPaths`, and the
  composition root lists `/admin/v1/audit-log` alone (`AuditedReads` in
  `guardOptions`, `internal/app/guards.go`).
  `TestTheOneAuditedReadIsTheAuditLogsOwnListing` in
  `internal/app/guards_internal_test.go` holds that list: deleting it, or adding
  `/admin/v1/orders` to it, fails the test. Before it, deleting the list
  survived `internal/app`'s unit tests and the end-to-end package, because the
  read-path tests in `core/http/audit_test.go` build paths of their own.
- **"Not one operation carries an operationId."** ADR 0161, its measurement and
  the comment in `internal/app/mcp_integration_test.go`. The fill has been in
  the generator since the document was first generated (`936223fb`,
  2026-08-31, as `islemKimligi`); `get_admin_v1_orders` is the operationId,
  while the path-derived fallback in `toolName` (`internal/mcp/tools.go`) would
  have said `get_orders`.
- **"The storefront is unauthenticated by decision (ADR 0008)."** The godoc of
  `Audit` in `core/http/audit.go`. ADR 0043 retracted the word; the guard's own
  godoc had already been corrected.
- **"A delegated token is never stored."** A premise of the first draft of this
  record. The checkout's workflow record leaves `PaymentData` out
  (`internal/workflows/checkout/complete_cart.go`), but the session row keeps
  whatever `Data` the provider returns (`internal/modules/payment/service/session.go`,
  `mergeData`). The record says "kept only as far as the provider returns it".

The 0.9.0 entry of `CHANGELOG.md` and
[measurements/0161](0161-a-hundred-and-twenty-one-tools.md) stay as written:
they are the record of their date.

## 6. The tree-wide search

`git grep -liwE 'agents?'` at `8339896c` names 23 files, in four senses:

- **The `User-Agent` header**: `plugins/webhookout/sender.go`,
  `plugins/webhookout/signature.go`,
  `internal/modules/auth/api/session_browser_test.go`,
  `internal/adminui/session_internal_test.go`, `docs/security.md`,
  `docs/known-limits.md`, ADR 0267, ADR 0276 and `docs/adr/README.md`.
- **The Web Push "user agent"**: `plugins/webpush/crypto.go` and
  `plugins/webpush/crypto_test.go`.
- **The agents that wrote or measured this repository, and their tooling**:
  `.gitignore`, `CHANGELOG.md` (the translation entries),
  `core/query/query_integration_test.go`, `internal/arch/arch_test.go`,
  `internal/arch/count_claims_test.go`, `internal/arch/language_test.go`,
  `internal/arch/schema_names_test.go`, and measurements 0119, 0120 and 0159.
- **Two other senses**: a human support agent
  (`docs/measurements/must-have-commerce-features.md`), and the actor of a
  write, which ADR 0051 refuses to substitute for the property a rule names.

None is a shopping agent. `llms.txt`, `.well-known`, "agentic" and the names of
agent payment protocols occur nowhere.

"mandate" occurs in other senses only:

- `docs/measurements/0064-the-stored-payment-instrument.md`, twice: a provider's
  payment mandate, which the stored-instrument record does not build.
- `docs/measurements/storefront-speed-and-checkout.md`: the same absence, in the
  payment-method comparison.
- `docs/measurements/commerce-models.md`: a subscription's stored mandate at the
  provider.
- ADR 0043 and `CHANGELOG.md`: a requirement switched on, in the sense of an
  enforced flag.
