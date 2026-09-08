# Measurement 0057 — what an unproven `customer_id` bought

Serves [ADR 0057](../adr/0057-one-comparison-holds-the-storefront-customer-claim.md).
Measured 2026-09-08 against the tree at `3c912f3`, before the change.

## 1. The class: where a storefront request names a customer

Resolved from the route registrations and the request types rather than
estimated. Thirteen surfaces, twelve under `internal/modules` and one in a
plugin.

| Surface | How the customer is named | State before |
|---|---|---|
| `customer/api` — profile (2) and address book (6) | path segment `{id}` | **proven** since ADR 0043 |
| `b2b/api` — company, employee (2) | path segment `{customer_id}` | believed |
| `cart/api` — creation, handover (2) | body field `customer_id` | believed |
| `plugins/webpush` — subscribe (1) | body field `customer_id` | believed |

`POST /store/v1/customers` names no customer: it mints the record. The order
module's storefront read names a CART, not a person. The catalog's GraphQL POST
writes nothing.

## 2. The cart, followed end to end

`storeCreateCart` passed the body's `customer_id` to `CartOpening`, which is
`CreateCart` in `internal/workflows/cart`. That flow, when the field is set,
calls `CustomerEmail` on the customer module's interop surface, and:

- **it fails with `NotFound` when the customer does not exist** — so `201`
  against `404` answers "does this identifier belong to anybody";
- **it copies the customer's registered e-mail into the cart when the body sent
  none** — the flow's own godoc says so, and
  `TestCreateCartRegisteredCustomerUsesStoredEmail` has always asserted it;
- `storeCreateCart` then READS the cart back and returns it, and `cartDTO`
  carries `email`.

So one `POST /store/v1/carts` naming a stranger returned that stranger's
registered e-mail address in the `201` body. The identifier it needs is not a
secret: it travels in cart and order responses.

`storeUpdateCart` had no such lookup at all — `UpdateCart` in the cart service
never calls the customer module — so the handover wrote ANY string as the
cart's owner, including one belonging to nobody. Its single existing guard
(`CodeCustomerMismatch`) refuses a SECOND owner, not a first.

## 3. The spending window: what leaks and what does not

The cart's customer becomes the order's customer, and `enforceSpendingLimit` in
the order service compares that customer's window. Two claims were checked
separately.

**The numbers do NOT leak.** `enforceSpendingLimit` builds a message carrying
the spend, the order total and the limit — but the checkout runs inside the saga
engine, and `stepFailureCode` carries only the CODE outward while the engine
writes its own sentence ("the %q step (%d) of the %q workflow failed") as the
message. `WriteError` renders the FIRST typed error in the chain, which is the
engine's. A storefront client therefore sees
`409 order_spending_limit_exceeded` and no figures.

**The code alone is still an oracle, and it is free.** A rejected completion
writes no order, holds no stock and leaves the cart open — `TestStorefrontB2BLimitRejectionReportsReason`
asserts all three. So a caller could vary the cart total and read the 409 as
"spend + total > limit", converging on a stranger's remaining allowance from
above at no cost. The other half of the pair was `GET /store/v1/b2b/customers/{customer_id}/employee`,
which hands back `spending_limit`, its reset period and the current window's
start; `.../company` adds the employer's name, contact address and billing
address. Limit from one route, spend from the other: the two halves of defect
D3's residue compose, which is why one record closes both.

## 4. The radius, checked rather than assumed

Three claims were made about what a fix would touch. All three hold.

- **`internal/e2e` has six live callers.** `openStorefrontCartInCountry` is
  called from six scenarios that pass a customer id, directly or through
  `openStorefrontCart`. The harness binds a verifier so the check is observable
  at all, and that verifier refuses a request carrying no header — so all six now
  send the proof. Verified after the change: the six still exercise checkout.
- **The OpenAPI describe surface of both modules is in scope.** `Describe` in
  each api package writes the operations, and a refusal an operation can produce
  and does not describe is the repository's standing defect class. The cart's
  description is CONDITIONAL where the others are not, which is why the three
  modules do not share one helper.
- **`plugins/webpush` holds a third route of the same class.** Verified again
  after the change: `POST /store/v1/webpush/subscribe` decodes a
  `subscribeRequest` whose `CustomerID` godoc says the field "is believed", and
  the plugin tree is outside the gate's walk. It is NOT closed here: its defect
  is that it is a standing authority (ADR 0051) — the binding persists in
  `webpush_subscription` and outlives the request — and proving the claim at
  subscribe time would leave that authority in place while making the row look
  closed.

An e2e Identity implementation cannot read the request BODY: the handler decodes
it first, and the contract on `corehttp.Identity` says the request must not be
modified. The harness's verifier therefore reads a header — which is what an
upstream proxy would set, and what a real one does with a cookie.

## 5. The three questions a cheap fix would have decided silently

The cheap fix was: stop an unproven `customer_id` populating the cart's e-mail,
so nothing is echoed. It answers three separate questions without asking them.

1. **Is the defect DISCLOSURE or AUTHORIZATION?** Suppressing the echo treats it
   as disclosure and retires the measurable symptom. The spending-window burn,
   which no test can see from outside, stays — and the ledger row would be
   closed on the half that was visible.
2. **Does the cart take ADR 0043's contract?** After the echo is gone the
   remaining harm is invisible, so the pressure to bind is gone with it. "Never"
   would be the answer, and nobody would have written it down.
3. **What is the cart's contact address FOR?** The copy is a shipped
   convenience — a registered customer's cart arrives with an address, so the
   checkout does not ask again. Removing it degrades the HONEST path too,
   because the flow cannot tell a proven claim from an unproven one. That is a
   product decision about the order's contact channel and it belongs to
   whoever owns the checkout, not to a security patch.

## 6. The shape: why the narrowing is not a refusal

The first draft of this record refused when no `corehttp.Identity` was bound, on
the address book's model. That was measured against the tree and withdrawn.

- **`cmd/server` binds nothing.** `IdentityName` and `identity` appear nowhere
  under `cmd/`, `internal/app/` or `internal/smoke/`. The repository's own
  reference installation is therefore the "bound nothing" case.
- **`internal/smoke/b2b_test.go` proves the shipped binary through those exact
  routes.** It is `//go:build smoke`, so `go test ./...` never compiles it: the
  refusing draft turned three assertions from `200` into `401` and no lane the
  author ran could see it. `smoke_test.go` builds `./cmd/server`, and the three
  are the company read and the two `b2bReadStorefrontEmployee` calls.
- **Under the shipped shape they stay `200`.** With nothing registered the
  binding hands the handler a nil identity and `storeCustomerID` returns the path
  claim, which is what those routes did before. `go vet -tags smoke
  ./internal/smoke/` compiles clean and no file under `internal/smoke/` is
  touched by this change.

So the cost of refusing was not an upgrade note for third parties, as the draft
claimed — it was a red suite on the repository's own binary. What replaces it is
the residue in ADR 0057's Consequences: with no verifier bound the four routes
that record reaches still believe the claim, and binding one closes them.

## 7. The gate, proven by mutation

`TestNoStorefrontSurfaceActsOnACustomerItCannotProve` in `internal/arch` walks
the registered storefront routes and the request types the handlers decode,
selects those that name a customer, and requires each handler to REACH
`corehttp.ProvenCustomer` (through its own package's helper, resolved by a
fixpoint over the call graph). `TestNoAPIPackageDefinesItsOwnProof` beside it
forbids a LOCAL function of that name, which the first gate cannot tell from the
core's because `calleeName` drops the package qualifier on purpose.

Four mutations, each run with `-count=1` and each reverted from a copy taken
aside:

| Mutation | Failure |
|---|---|
| `storeCreateCart` passes `body.CustomerID` straight to the flow | `POST /store/v1/carts names a customer and never reaches ProvenCustomer` |
| `storeGetCompany` passes the path parameter straight to the service | `GET /store/v1/b2b/customers/{customer_id}/company names a customer and never reaches ProvenCustomer` |
| `storeGetCustomer` passes the path parameter straight to the service | `GET /store/v1/customers/{id} names a customer and never reaches ProvenCustomer` |
| `cart/api` declares its own `ProvenCustomer` and calls that | `the cart module's api package declares its own ProvenCustomer` |

The third exists because that branch of the population — the customer IS the
resource, so the segment is `{id}` — is the one a structural scan would silently
miss. The fourth is the loophole the adversarial pass named.

The population comes from the routes and the request types, both independent of
the property being audited, so a handler that stopped proving anything still
appears in it. The blindness floor is three-sided: the walk fails if no route
naming a customer is found, if no function in any api package reaches the
comparison at all, and if no function is found declared in an api package.

## 8. The non-breaking claim, proven by mutation as well

"An installation that bound nothing is unaffected" is a property no gate above
can see, so it is held behaviourally. Two mutations, same protocol:

| Mutation | Failure |
|---|---|
| the cart binding answers `errors.Unauthorized(CodeIdentityNotBound)` when nothing is registered | `TestTheIdentityBindingIsEmptyWhenNothingIsRegistered`: `Received unexpected error: identity_not_bound` |
| `Handler.provenCustomer` drops the nil-identity branch and calls the comparison anyway | `TestABodyNamingACustomerIsStillServedWhenNothingIsBound`: `POST /store/v1/carts answered 401 with no identity bound` |

The b2b half is the same pair: the second mutation applied to `storeCustomerID`
turns `TestTheStorefrontStillAnswersWhenNoIdentityIsBound` red with
`expected: 200, actual: 401`.
