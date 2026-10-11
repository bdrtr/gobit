# What a receiver could open — measured 2026-10-11

Evidence for [ADR 0439](../adr/0439-an-id-gobit-hands-out-opens-nothing-at-the-storefront.md)
and gaps D280, D281, D282 and D283. Read on a tree at 136fde4f (ADR 0437
landed); the integration lanes ran against Docker's `postgres:16-alpine`
through testcontainers, with the e2e harness's customer identity bound
(`storefrontIdentity`, which proves the customer an `X-E2E-Customer` header
names).

## 1. The reproduction, before

Six tests in `internal/e2e/store_order_access_test.go`, written in the fix's
direction, run on 136fde4f with nothing else changed:

```
go test -tags=integration -count=1 -run 'TestAWebhookReceiver|TestAGuestsOrder|TestAnOrderRead|TestACartCreated|TestAParcelDelivery|TestAPaymentCaptured' -v ./internal/e2e/
--- FAIL: TestAWebhookReceiverReadsNoShoppersOrderAndOpensNoReturn
--- FAIL: TestAGuestsOrderIsNotReadByItsID
--- FAIL: TestAnOrderReadLeadsToNoAddress
--- FAIL: TestACartCreatedDeliveryOpensNoCart
--- FAIL: TestAParcelDeliveryOpensNoTrackingNumber
--- FAIL: TestAPaymentCapturedDeliveryOpensNoCollection
```

The second party is a webhook receiver the operator registered through
webhook-out's admin route; every call it made carried the shop's publishable
key and nothing else. The decisive answers, cut down:

| What the receiver held | What it sent | What it got |
|---|---|---|
| `order.placed`'s `order_id` | `GET /store/v1/customers/{id}/orders` (the shopper's list) | `401 identity_refused` |
| the same | `GET /store/v1/orders/{id}` | `200`, with `customer_id` (withheld from the delivery), `email`, `cart_id`, the lines and the totals |
| the same | `GET /store/v1/orders/{id}`, proving another customer | `200` |
| the same | `POST /store/v1/orders/{id}/returns` for both units | `201`, a return holding the line's two units |
| — | the owner's own return of one unit, proving the owner | `409 order_return_quantity_exceeded`: "1 requested plus 2 already returned or canceled, 2 bought" |
| the same | `GET /store/v1/orders/{id}/timeline` | `200` |
| a guest's `order_id` | `GET /store/v1/orders/{id}` | `200`, with the guest's e-mail |
| the order record's `cart_id` | `GET /store/v1/carts/{id}` of the completed cart | `200`: shipping address "9 Far Road", gate code 4411, billing company and address |
| `cart.created`'s `cart_id` | `GET /store/v1/carts/{id}` of a live cart | `200`: e-mail, "1 Home Street", phone `+905550000000` |
| the same | `PUT /store/v1/carts/{id}/shipping-address` | `200`: the delivery moved |
| `fulfillment.delivered`'s `reference` | `GET /store/v1/orders/{id}/timeline` | `200`: `"detail":"TRK-E2E-ACCESS-0001"` on two parcel moments |
| `payment.captured`'s `payment_collection_id` | ~~`GET /store/v1/payment-collections/{id}`~~ | `200`: `amount`, `captured_amount`, `reference: cart_…` |
| that `reference` | `GET /store/v1/carts/{id}` | `200`, the cart's e-mail and customer |

Ids are 26 Crockford characters over 80 random bits (`order/models/ids.go`,
`cart/models/ids.go`); nothing was guessed. Every id above was one gobit put in
the receiver's hands: an order topic's `order_id`, a parcel topic's
`reference`, a cart topic's `cart_id`, a payment topic's
`payment_collection_id`, and a storefront record's `cart_id` and `reference`.
The webhook envelope's `event_id` carried the cart's id a second time,
`cart.created:<cart id>`, so withholding the payload field alone would not have
withheld it.

## 2. The storefront payment routes: who called them

Counted before deciding whether they are gated or removed. The routes were
~~`GET /store/v1/payment-collections/{id}`~~,
~~`POST /store/v1/payment-collections/{id}/payment-sessions`~~ and
~~`POST /store/v1/payment-sessions/{id}/cancel`~~.

| Where | Calls | How it was read |
|---|---|---|
| gobit-storefront at 5bf657a (`src/`, `e2e/`) | 0 | `grep -rIn "payment-collections\|payment-sessions"` without `node_modules` and `.next`: the hits are `src/lib/gobit/schema.d.ts`, generated types, and `e2e/account.spec.ts`, which calls the ADMIN routes to capture |
| `examples/` (storefront, starter, plugin) | 0 | `grep -rIn "store/v1/payment"`: `GET /store/v1/payment-providers` only |
| `docs/first-run.md` | 0 | the same grep |
| `internal/e2e`, `internal/smoke` | 0 | the same grep: a comment in `internal/e2e/schema_test.go` and the provider list in `internal/smoke/b2b_test.go` |
| the PayTR checkout branch (`paytr-checkout`, read at 136fde4f plus one untracked file) | 0 in code | its new route, a payment session opened on the cart, is not written yet; its design answers the session and collection ids to the shopper from the cart route, and names the session cancel as the storefront's way to release a paid session after a refused completion |
| `internal/modules/payment/api` tests | 9 | `api_test.go` and `describe_internal_test.go` |

No caller outside the module's own tests, so the three are removed rather than
gated. The PayTR checkout branch's plan to cancel a session from the storefront needs
a route of its own when it lands on this one, keyed on the cart its shopper
holds.

## 3. Why tokens are issued per recipient

The order keeps a SHA-256 of a token and never the plaintext. A single token
minted with the order could not serve both holders:

- **The mail is built after the commit.** The notification module mails on
  `order.placed`, asynchronously and through `order.interop`
  (`notification/service/events.go`, `orderMail`); it reads the order, and a
  digest gives nothing back to put in a link.
- **The saga's record keeps step outputs.** `CompleteCartResult` is the output
  of the checkout's last step and the workflow engine stores it as JSON
  (`checkout/complete_cart.go`), so a token returned through the saga would sit
  in the workflow tables for as long as the record does.

So the completion asks for a token after the saga returns
(`Workflows.issueOrderToken`, the result field tagged `json:"-"`), each order
mail asks for its own as it is built, and `order_access_tokens` holds one row
per issue. A mail sent later does not close the page the shopper has open,
because an issue never replaces an earlier token.

## 4. After

The same file, rewritten as the guard (the receiver's calls refused, the
shopper's ways in held open), plus one test of the completion over HTTP:

```
go test -tags=integration -count=1 -run '…' ./internal/e2e/
ok  	github.com/bdrtr/gobit/internal/e2e
```

Every refusal the receiver met answers `404 order_not_found` with the unknown
id's message, its request id aside. The shopper's ways in: the completion's
token (`200` on the read and the timeline, `201` on a return), the proven
owner (`200`, `201`: the receiver's request held nothing), the order mail's own
token (`200`), a guest's token on the guest's order (`200`, the guest's own
e-mail). Another order's token, a forged token and a proof of another
customer answer the 404. A repeat of the completion under the same
`Idempotency-Key` answers the recorded `order_token` (the known limit).

The original six against the fixed tree, as a cross-check: four pass, and two
fail by construction. `TestAnOrderReadLeadsToNoAddress` required the receiver's
read to answer 200 before following its `cart_id` (it answers 404, and the
record carries no cart id), and `TestACartCreatedDeliveryOpensNoCart` waited
for a delivery whose `data.cart_id` named the cart (no delivery carries one).
The guard asks the same questions without those two preconditions.

The other e2e call sites that read an order at the storefront with the key
alone — sixteen, in thirteen files — now present a token the test issues
through the order service, as a shopper's page presents the completion's.
`TestAShopperListsTheirOwnOrdersOnTheStorefront` read `customer_id` off the
list's records and now holds its absence; the analytics funnel's tests waited
for `cart.created:<cart id>` and wait for the digest's id.

## 5. Mutants

Run by `mutate.py` against a green baseline of every lane it uses, each mutant
applied alone, `-count=1`, the tree restored after each.

| # | Mutant | Killed by |
|---|---|---|
| M1 | the order read's gate removed | order/api access tests, `TestEveryStorefrontOrderRouteDecidesItsAccess`, `TestNoForwardedFieldIsAStorefrontKey`, e2e T1, T2 |
| M2 | the timeline's gate removed | order/api access tests, both arch gates, e2e T1, T5 |
| M3 | the return request's gate removed | order/api access tests, both arch gates, e2e T1 |
| M4 | the order's empty customer no longer guarded (a guest's order, nobody proven, `"" == ""`) | `TestAStoreOrderRouteRefusesWithTheUnknownIDsAnswer`, e2e T2 |
| M5 | the empty token's guard removed in the comparison | `TestAnAccessTokenMatchesOnlyADigestTheOrderKept` |
| M5b | the empty token's skipped read removed in the service | **survives, equivalent**: the comparison refuses an empty token itself, so the read costs a query and changes no answer |
| M6 | the digest read ignores the order (`WHERE order_id = $1 OR true`) | `TestATokenOpensItsOwnOrderOnly` (integration), e2e T2 |
| M7 | the identity's outage swallowed, served as nobody | `TestAStoreOrderRouteAnswersAnIdentityThatCouldNotDecide` (the harness's identity never fails, so e2e cannot) |
| M8 | any proven customer accepted | `TestAStoreOrderRouteRefusesWithTheUnknownIDsAnswer`, e2e T1 |
| M9 | the token in the saga's execution record (`json:"order_token"`) | `TestTheOrderTokenIsNotInTheExecutionRecord` |
| M10 | the storefront's completion asks for no token | `TestOnlyTheStorefrontsCompletionAsksForTheOrdersToken`, e2e completion test |
| M10b | the checkout interop drops the token | `TestTheInteropAnswersTheTokenOnlyWhenAsked`, e2e completion test |
| M10c | the completion's answer drops the token | `TestOnlyTheStorefrontsCompletionAsksForTheOrdersToken`, e2e completion test |
| M11 | the order mail carries no token | notification service tests (three), e2e completion test |
| M12a | `cart_id` not withheld | `TestTheTopicFieldsAreEveryPayloadsFields`, e2e T4 |
| M12b | `cart_id` forwarded with `TopicFields` agreeing | `TestNoForwardedFieldIsAStorefrontKey`, e2e T4 |
| M12c | a cart event's id carries the raw cart id | e2e T4 |
| M13 | the storefront read answers the admin record | `TestTheOperatorReadsWhereTheOrderWent`, e2e T1, T3 |
| M13b | the own-orders list answers the admin record | `TestAShopperListsTheirOwnOrdersOnTheStorefront` (e2e) |
| M14 | a completed cart read at the storefront | `TestACompletedCartIsNotReadAtTheStorefront`, e2e T3, T6 |
| M15 | `==` for the constant-time compare | `TestASecretIsComparedInConstantTime` (behaviour is the same, so only the gate can) |
| M16 | the refusal answered 403 | `TestAStoreOrderRouteRefusesWithTheUnknownIDsAnswer`, e2e T1, T2, T5 |
| M17 | the erasure keeps the tokens | service and integration erasure tests |
| M18 | the storefront collection read registered again | `TestTheStorefrontTakesNoCollectionOrSessionID`, `TestNoForwardedFieldIsAStorefrontKey`, e2e T6 |
| M19 | the identity asked before the token | `TestAStoreOrderRouteOpensToTheOrdersToken` |
| M20 | the plaintext stored instead of the digest | service test, four integration tests (the CHECK refuses it) |
| M21 | any token matches an order that issued one | service and integration tests, e2e T1, T2 |
| M22 | a token issued without being asked | `TestACompletionThatDoesNotAskIssuesNoToken` and two more |
| M23 | the operator's completion asks for the shopper's token | `TestOnlyTheStorefrontsCompletionAsksForTheOrdersToken` |

T1 to T6 are the guard's six tests in the order of section 1. The first run of
M11 deleted the data key and left its value unused, which does not compile; it
was rerun with the value discarded, and the table records the rerun.

## 6. The independent review, and what it changed

An independent security review read the fix before it landed (one medium
finding, eight low) and probed it on the e2e harness. Each finding below was
reproduced by its probe, and each probe is a test now.

- **The idempotency ring stood in front of the gate (medium).** The store ring
  keyed a record on the publishable key — every shopper's — the method, the
  path and the body. The order's gate is in the handler, after the ring. The
  probe: the owner returned a unit with a token under
  `Idempotency-Key: return-<order id>` (201); a caller with no token sending the
  same key and body was answered the owner's recorded 201, reason included,
  `Idempotency-Replayed: true`; a caller with no token writing first under the
  owner's next key recorded a 404, and the owner presenting the token was
  answered that 404. The ring now keys a store record on the request's proof as
  well — the order token it presents and the customer the bound identity
  proves, as a SHA-256 in the store key. This round served a request whose
  proof could not be read without a record; section 7 says why that changed.
- **The sweep of the class.** Every unsafe storefront route was read for where
  its access lives. The cart's routes carry it in the path (the cart's id), so
  the path in the fingerprint suffices; cart creation is exempt from the ring.
  The return request (token or customer), the customer's twelve writes —
  addresses five, wishlist and alerts six, the profile one — and the review
  submission prove a customer that is not in the path, as do the session-bound
  writes of `contrib/identity-session` and `contrib/identity-passkey`. Before,
  each answered its record to a caller replaying the key, path and body without
  the proof; a second e2e test holds the customer's address book to it. The
  completion's record carries the order's token and stays readable to a caller
  holding the cart's id, the key and the body; the cart's id is its access
  already, and the known limits say so.
- **An outage told an order from none (low).** During an identity outage an
  existing order, a guest's included, answered 503 and an unknown id 404. The
  identity is asked before the order's existence is told, so both answer 503.
- **The timing of a token (low, no change).** A request presenting a wrong
  token takes about 75 µs longer on an existing order than on an unknown id
  (median of 200, in process: 197–210 µs against 275–283 µs), for the digest
  read; it tells existence to a caller who already holds the id, and over a
  network the difference sits in the noise.
- **A completed cart still answered its option list (low).** Its read was 404
  and its writes 409 `cart_completed`, which told the cart completed. Every
  storefront route on a completed cart now answers the unknown cart's 404, from
  one check its routes share; a router sweep and an arch gate hold every route
  under `/store/v1/carts/{id}` to it.
- **An addition cart told a stranger about an order (low).** Opening a cart
  with `adds_to_order_id` answered a guest `order_addition_needs_customer` and
  a proven stranger `order_addition_customer_mismatch`, the status and the
  parent before them; an unknown id answered 404. Anyone but the order's
  customer is now answered the unknown order's 404. A token does not open this
  path: an addition needs a customer.
- **Queued deliveries (low, documented).** Deliveries queued before the
  upgrade still carry `cart_id`, and a receiver keeps the cart ids it was sent;
  the CHANGELOG says to drain the queue.
- **No cache headers (low).** The order routes' answers carry
  `Cache-Control: no-store` and vary on the token's header, refusals included.
- **A replayed checkout issues a new token (low, documented).** The engine's
  replay for a cart it could not mark completed issues a new token to whoever
  holds the cart's id; the known limits say so.
- **The erasure kept retained orders' tokens (low).** It now withdraws the
  tokens of every order of the person, a retained one too; the customer's own
  proof still opens a retained order.

Mutants of the amendment, baseline green, each alone:

| # | Mutant | Killed by |
|---|---|---|
| MA1 | the ring leaves the proof out of the namespace | `TestTwoCallersWithDifferentProofsNeverShareARecord`, both e2e replay tests |
| MA2 | the application passes no proof | both e2e replay tests |
| MA3 | the proof without the order token | the return replay test |
| MA4 | the proof without the proven customer | both e2e replay tests |
| MA5 | an unreadable proof recorded in the shared namespace | that round's unreadable-proof unit test, which section 7 replaced with `TestAProofThatCannotBeReadRefusesTheRequest` |
| ML1 | the identity asked only for a real order | `TestAnOutageAnswersTheSameForEveryID` |
| ML3a | the completed-cart refusal removed from the read | cart unit tests, the arch gate, three e2e tests |
| ML3b | the refusal removed from the option list | the cart sweep, the arch gate, the router sweep |
| ML4 | the addition check tells a stranger about the order | `TestCheckAdditionAsksWhatTheWriteAsks`, two e2e tests |
| ML6 | `no-store` dropped | `TestAStoreOrderAnswerIsTheCallersAlone` |
| ML8 | the erasure withdraws the settled orders' tokens alone | service, integration and e2e erasure tests |

MA4's first run left a variable unused and did not compile; the rerun discards
it, and the table records the rerun.

## 7. The follow-up review

A second review read the amended fix: all nine findings above closed, one new
medium and three low.

- **The ring asked the identity on every keyed write (medium).** The store
  proof was computed for a cart's routes too, and a proof that could not be
  read ran the write with no record. With a harness identity that answers a
  classified Unavailable for one session, measured on the amended tree and on
  edd17e19:

  | Request, sent twice under one key during the outage | amended | edd17e19 |
  |---|---|---|
  | a return carrying the order's token and the session | 201, 201: two returns | — |
  | a cart line add of one unit, with the session | 201, 201: quantity 2 | 201, replayed: quantity 1 |
  | the cart's completion, with the session | 200, then 404 `cart_not_found` | 200, replayed |

  A cart's routes now ask nothing — the path is their access, and their records
  are keyed as at edd17e19 — and a keyed write elsewhere whose proof cannot be
  read is refused with the identity's 503 and writes nothing: the outage
  probes are tests, both requests of the return answer 503 with no return
  written, the same key's request after the outage is its first run, and the
  cart's line add and completion are replayed during the outage.
- **The operator's door (low).** `POST /admin/v1/carts` with `adds_to_order_id`
  answers 404 where it answered 409 for no customer or another one; the
  CHANGELOG now says so.
- **A completed cart as a merge's source (low).** It answered
  `409 cart_completed` and an unknown source 404; it answers the 404 now.
- **The CHANGELOG (low).** `GuardOptions.IdempotencyProof` and
  `IdempotencyProofFunc` are under Added, and the Breaking entry says that a
  changed proof is not replayed and that an outage refuses a keyed write.

| # | Mutant | Killed by |
|---|---|---|
| MB1 | a cart's route asks the identity | `TestTheStoreProofAsksTheIdentityOnlyOffACart`, `TestACartsKeyedWritesAreReplayedDuringAnOutage` |
| MB2 | an unreadable proof runs the write unrecorded | `TestAProofThatCannotBeReadRefusesTheRequest`, `TestAKeyedReturnDuringAnOutageIsRefusedAndWritesNothing` |
| MB3 | a completed merge source told apart | `TestAMergeOfACompletedCartAnswersAsAnUnknownOne` |
