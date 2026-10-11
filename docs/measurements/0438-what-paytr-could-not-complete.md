# What PayTR could not complete — measured 2026-10-11

Evidence for [ADR 0438](../adr/0438-a-shoppers-payment-is-opened-before-the-cart-is-completed.md)
and gaps D284, D285 and D286. The base is 136fde4f; the integration lanes ran
against Docker's `postgres:16-alpine` through testcontainers, with PayTR replaced
by an `httptest` server that verifies the get-token signature, issues
`iframe-<merchant_oid>` tokens and records refunds, and PayTR's callback posted
with a correct signature to `/paytr/callback` through the installation's own
callback ring.

## 1. The reproduction, at 136fde4f

The reproduction put PayTR on the e2e ground and tried every sequence a
storefront can drive on a one-line guest cart of 12 000 TRY (10 000 net, 20
percent tax). Re-run on 2026-10-11 at the base, request ids elided, and cart
ids elided to `{id}` in the refusal quoted after the change:

```
S1 complete: status=503 body={"error":{"code":"paytr_not_authorized","message":"the \"authorize_payment\" step (3) of the \"complete_cart\" workflow failed","request_id":"…"}}
S1 PayTR get-token calls: [{OID:paycol06GJHEE78HDJCRYKEZC94ZQQGC Amount:12000 Token:iframe-paycol06GJHEE78HDJCRYKEZC94ZQQGC SignatureOK:true}]
S1 state: {Orders:[canceled] Collections:[paycol_06GJHEE78HDJCRYKEZC94ZQQGC] Sessions:[paytr:paycol06GJHEE78HDJCRYKEZC94ZQQGC:canceled] PayTRRows:[paycol06GJHEE78HDJCRYKEZC94ZQQGC:pending:0] Executions:[failed paytr_not_authorized: the "authorize_payment" step (3) of the "complete_cart" workflow failed: paytr_not_authorized: PayTR has not reported on this payment yet;]}
S2 callback for paycol06GJHEE7H1S80K1C6W244H4X6M: status=200 body="OK"
S2 retry complete (same Idempotency-Key): status=503 body={"error":{"code":"paytr_not_authorized","message":"the \"authorize_payment\" step (3) of the \"complete_cart\" workflow failed","request_id":"…"}}
S2 PayTR get-token calls: [{OID:paycol06GJHEE7H1S80K1C6W244H4X6M Amount:12000 Token:iframe-paycol06GJHEE7H1S80K1C6W244H4X6M SignatureOK:true} {OID:paycol06GJHEE7JXQZSP0F1DDP772QD4 Amount:12000 Token:iframe-paycol06GJHEE7JXQZSP0F1DDP772QD4 SignatureOK:true}]
S2 final state: {Orders:[canceled canceled] Collections:[paycol_06GJHEE7H1S80K1C6W244H4X6M paycol_06GJHEE7JXQZSP0F1DDP772QD4] Sessions:[paytr:paycol06GJHEE7H1S80K1C6W244H4X6M:canceled paytr:paycol06GJHEE7JXQZSP0F1DDP772QD4:canceled] PayTRRows:[paycol06GJHEE7H1S80K1C6W244H4X6M:success:12000 paycol06GJHEE7JXQZSP0F1DDP772QD4:pending:0] Executions:[failed paytr_not_authorized: the "authorize_payment" 
S3 POST /store/v1/payment-collections: status=404 body=404 page not found
S3 POST /admin/v1/payment-collections with a publishable key: status=401 body={"error":{"code":"unauthenticated","message":"authentication is required","request_id":"…"}}
S3 store session on leftover collection: status=201 body={"data":{"id":"payses_06GJHEE7Q28HB8XQE918F84DY4","payment_collection_id":"paycol_06GJHEE7PBV5C3K2G6P5GFVHX4","provider_id":"paytr","external_id":"paycol06GJHEE7PBV5C3K2G6P5GFVHX4","status":"pending","amount":12000,"authorized_amount":0,"currency_code":"TRY","data":{"iframe_token":"iframe-paycol06GJHEE7PBV5C3K2G6P5GFVHX4"},"created_at":"2026
S3 complete after store session + payment: status=503 body={"error":{"code":"paytr_not_authorized","message":"the \"authorize_payment\" step (3) of the \"complete_cart\" workflow failed","request_id":"…"}}
S3 final state: {Orders:[canceled canceled] Collections:[paycol_06GJHEE7PBV5C3K2G6P5GFVHX4 paycol_06GJHEE7QWRSBBJ0M6BARJH2H0] Sessions:[paytr:paycol06GJHEE7PBV5C3K2G6P5GFVHX4:canceled paytr:paycol06GJHEE7PBV5C3K2G6P5GFVHX4:pending paytr:paycol06GJHEE7QWRSBBJ0M6BARJH2H0:canceled] PayTRRows:[paycol06GJHEE7PBV5C3K2G6P5GFVHX4:success:12000 paycol06GJHEE7QWRSBBJ0M6BARJH2H0:pending:0] Executions:[failed
S4 complete: status=200 body={"data":{"order_id":"order_06GJHEE7TYZFS6TT5XFS4J6MM4","cart_id":"cart_06GJHEE7SDDGQWJ7SQQBETAAQW","currency_code":"TRY","total":12000,"outstanding":0}}
S4 state: {Orders:[pending] Collections:[paycol_06GJHEE7V1K29VJ0S1SH6Q3970] Sessions:[paytr:paycol06GJHEE7V1K29VJ0S1SH6Q3970:captured] PayTRRows:[paycol06GJHEE7V1K29VJ0S1SH6Q3970:success:12000] Executions:[completed ]}
S5 complete: status=500 body={"error":{"code":"payment_provider_contract_violation","message":"an unexpected server error occurred","request_id":"…"}}
S5 refunds sent to PayTR: [paycol06GJHEE7YD9GRXC0NS16P7E04G:125.00]
S5 state: {Orders:[canceled] Collections:[paycol_06GJHEE7YD9GRXC0NS16P7E04G] Sessions:[paytr:paycol06GJHEE7YD9GRXC0NS16P7E04G:canceled] PayTRRows:[paycol06GJHEE7YD9GRXC0NS16P7E04G:success:12500] Executions:[failed payment_provider_contract_violation: the "authorize_payment" step (3) of the "complete_cart" workflow failed: payment_provider_contract_violation: provider "pay]}
```

S4 is the control: PayTR's callback is recorded inside the get-token call,
before any customer could have seen the iframe, and the order is placed — the
wiring was right and the ordering was the fault. S5 is S4 with the customer
paying in installments: `total_amount` 12 500 against a session of 12 000, a
contract violation, and the full 125.00 refunded. With PayTR on the ground the
whole e2e package ran 373 top-level tests green and two red, the authorization
matrix ("POST /paytr/callback belongs to no surface this matrix knows") and the
schema gate ("NEW DEBT: GET /admin/v1/paytr/pending … NO BODY"); without it both
were green (the reproduction's own run). S3's first two lines are the routes a
storefront might have tried for a collection of its own: neither exists for it.

## 2. The same reproduction after the change

The one-call completion is refused before anything is opened, so S1, S2 and S3
fail their own suspicion assertions and S4 dereferences the callback its
get-token hook never got to post:

```
S1 complete: status=409 body={"error":{"code":"checkout_workflow_payment_session_required","message":"\"paytr\" is paid on the provider's own page, so the payment is opened before the cart is completed: POST /store/v1/carts/{id}/payment-sessions, then complete with its payment_session_id (ADR 0438)","request_id":"…"}}
S1 PayTR get-token calls: []
S1 state: {Orders:[] Collections:[] Sessions:[] PayTRRows:[] Executions:[]}
S2, S3: the same refusal before any token, so the scenarios' "one token issued" assertions fail
S4: the get-token hook never runs, so the callback it would post is nil and the subtest panics; S5 is not reached
```

The scenarios the reproduction could not drive are the guard,
`internal/e2e/paytr_checkout_test.go`, on the same cart (12 000, five in stock):

| Test | What it measures | Figures |
|---|---|---|
| `TestAPayTRCheckoutPlacesAnOrder` | open → complete before the callback → callback → complete again under the same `Idempotency-Key` | open: one get-token call for 12000, signature valid, `data.iframe_token` = PayTR's; early completion 409 `checkout_workflow_payment_pending`, `Retry-After: 2`, no order, no execution, session `pending`, sellable 5; callback "OK" places the order: session `captured`, captures `paytr:12000`, row `success:12000`, sellable 4, report `completed` with 0 attempts; second completion 200, not replayed, total 12 000, outstanding 0, the report's order with an `ordtok_` token that opens `GET /store/v1/orders/{id}`, still one order, one capture, one get-token call, no refund, the order bound to the opened collection; the same key again answers the same token, another key the completed cart's 404 `cart_not_found` |
| `TestAPayTRCustomerWhoNeverReturnsGetsTheOrder` | open → callback, no storefront completion | one order, captures `paytr:12000`, one execution; reconciliation does not report the session; the `order.placed` mail carries a token that opens the order |
| `TestThePaymentReportAndTheStorefrontCompleteOnceAtOnce` | the callback and the storefront's completion released together, the storefront sending again every 20 ms while told 409 | 3 rounds a run: one order, captures `paytr:12000`, no refund, the storefront ends with that order and saw only 409 before it; `-race -count=10`: 10 of 10 green, 30 races, 30 orders |
| `TestAOneCallPayTRCompletionIsRefusedBeforeItStarts` | the documented single call | 409 `checkout_workflow_payment_session_required`; no token, no order, no session, no PayTR row, no execution |
| `TestAPayTRSessionPaysOnlyForItsOwnCart` | cart A's session named for cart B, and for A under `manual`; then for B again once A's order took it | all three 409 `checkout_workflow_payment_session_foreign`, the refusal names no other cart; A's callback places A's order, captures `paytr:12000`; B has none |
| `TestAPayTRPaymentForACartThatChangedIsRefunded` | opened at 12 000, a 6 000 line added, then paid | no order; the report refunds `120.00` and settles `refunded`; the storefront's completion of that session 409 `checkout_workflow_payment_session_closed`; reopened for 18 000 on a new `merchant_oid` and paid: one order, sessions `canceled, captured`, captures `paytr:18000` |
| `TestAPayTRPaymentForACartCompletedOtherwiseIsRefunded` | the cart completed under `manual` while the PayTR page was open, then paid there | refund `120.00`, report `refunded`, one order, captures `manual:12000` |
| `TestAPayTRPaymentInInstallmentsPlacesTheOrderForItsTotal` | callback `total_amount` 12 500 | one order, captures `paytr:12000`, row `success:12500`, no refund; the storefront's completion 200, total 12 000; session authorized 12 000, data `{"paid_amount":12500,"installment_difference":500}`; a paid-in-installments payment for a cart that changed is refunded `120.00`, not 125.00 |
| `TestAServerCompletionThatFailsIsTriedAgainAndReported` | stock set to 0 before the callback | no order, no refund, report open with 1 attempt and its next try one retry base (5 minutes) away; reconciliation reports the session: local `pending`, provider captured 12 000; stock back to 5, a sweep before the next try places nothing, a sweep after it places the order, captures `paytr:12000`, report `completed` |
| `TestAReportThatNeverArrivedIsFoundBySweep` | PayTR's row written `success` with no callback | the sweep discovers it and places the order, report `completed` |
| `TestAPaymentMadeAfterItsSessionWasCanceledIsReported` | opened, canceled under the cart, then paid on PayTR's page | callback "OK"; no order, session `paytr:canceled`, row `success:12000`, no report row; with the session an hour old, reconciliation reports it, provider captured 12 000 |
| `TestADrivenProviderCanBeOpenedFirst` | `manual` opened first | completion 200, one session, `manual:captured`, captures `manual:12000` |
| `TestAPayTROrderIDIsNotPaidTwice` | a refunded payment's collection, a second session through the admin route | 409 `paytr_order_id_taken`; still one get-token call, sessions `paytr:canceled`, no order |
| `TestAPaymentIsCanceledOnlyThroughItsOwnCart` | cart A's session canceled through cart B, then through A after its order | 409 `checkout_workflow_payment_session_foreign`, no refund, the message names no other cart; after the order, refused, no refund, session `captured` |

## 3. Who completes the cart

PayTR posts its callback to the server; the shopper's browser lands on the
shop's success page only if the shopper waits for it. The first form of the fix
had the storefront's completion authorize the paid session, so a shopper who
closed the tab left 12 000 taken and no order until a person found it. The
callback now reports the payment through `core/plugin`'s `PaymentReports`, and
the checkout completes the cart there and then, with the cart's own email, its
channel narrowing the warehouses, and the opened session's amount as the total
the shopper confirmed (`TestAReportCompletesTheCart`).

Both completions run under the checkout's saga key `complete_cart:<cart>`. The
second one in either order meets a captured session bound to one order and
answers with that order without preparing anything
(`TestACompletionOfATakenSessionAnswersWithItsOrder`); one that meets the first
still running is answered 409 `checkout_workflow_payment_pending`
(`TestACompletionOfASessionAlreadyRunningSaysNotYet`). A captured session of
another cart or provider, or a collection two orders hold, is not answered with
an order (`TestATakenSessionAnswersOnlyItsOwnCompletion`).

The report's outcome is kept in `payment_session_reports`:

| A report whose session or cart is… | settles | money |
|---|---|---|
| captured already | `completed` | kept |
| canceled, before or while it runs — by the storefront, an operator, a completion's compensation, or this path before a crash — and PayTR says the payment went back | `refunded`, ERROR | given back by the cancel |
| canceled while PayTR still holds the payment — a cancel that met the payment on its way | `closed`, ERROR | held; reconciliation reports it |
| declined by the provider | `closed` | none taken |
| opened by anything but the cart's own opening (key not `cart_payment:<its collection>`) | `left`, WARN | held, for a person |
| for a cart gone, emptied, completed with another payment, at another total, currency or customer | `refunded`, ERROR | cancel, at PayTR the refund — only once the session is neither captured nor authorized and no completion of the cart is running |
| refused for anything else (stock, a fault, a completion running or holding the session) | retried 5, 10, 20, 40, 80 minutes later, then `gave_up`, ERROR | held; reconciliation reports it for the rest of its day |

Held by `TestAReportSettlesBySessionState`, `TestAReportClosedWhileItRanSettlesByState`,
`TestAReportThatCanNeverPlaceItsCartIsRefunded`,
`TestAReportIsNotRefundedWhenAnotherCompletionTookIt`,
`TestAHeldSessionIsNotRefunded`,
`TestAFailedCompletionIsTriedAgainThenGivenUp` and `TestTheWaitsDouble`. The six
attempts' waits add up to 155 minutes after the first, inside the day
reconciliation reads a paid session with no order. A new report is first due
five minutes after it was opened (`TestASettledReportIsNotNamedAgain`, and on
PostgreSQL `TestTheReportQueueKeepsItsAttemptsOnPostgres`), so the sweep does
not attempt it beside the callback's own attempt, and a report already settled
is not handed back when PayTR sends the callback again.

The opposition review of this section found the refund path racing the
completion it was meant to stand beside. The second of two completions of one
cart is prepared while the first runs; when the first spends a promotion's last
use, the second's totals no longer match, and the first form of the report
refunded the session the first completion was about to authorize — the shopper
paid, was refunded, and had no order — while the storefront's second completion
was told to cancel the payment and open a new one. A completion naming an
opened session that is refused now asks the engine's record whether a
completion of the cart is running (`core.workflow.store`); one running is
answered "not yet", one that placed the order with this session answers with
it, and only a refusal with neither stands; the storefront's cancel is refused
the same way, and the report path never refunds an authorized session
(`TestARefusalBesideARunningCompletionIsNotYet`). The same review found the
engine's replay of a completed key handing a second paid session the first
one's order and settling it completed with its money held; the completion now
refuses a replay whose session is not its own, and the report refunds it
(`TestASecondPaidSessionIsNotHandedTheFirstOrder`). A report whose every
session is canceled — a customer who paid after the storefront canceled — is
refused by the payment module with `payment_report_session_closed`, logged at
ERROR by the checkout, and read by reconciliation
(`TestAReportOfOnlyClosedSessionsIsRefused`,
`TestAPaymentForAClosedSessionIsTakenAndLeftToAPerson`,
`TestMoneyTakenOnACanceledSessionIsReported`).

The follow-up review of those fixes found the running check taking an abandoned
record — a completion whose process stopped — for a live one, so a refusal
beside it answered "not yet" for good and the report gave up with the money
held; a record past the completion's lease (`ExecutionLease`, ten minutes) is
no longer running (`TestAnAbandonedCompletionIsNotRunning`). It found a canceled
session settled "refunded" when the cancel had met the payment on its way and
given nothing back; the report now asks PayTR, settling refunded only when the
payment went back and closed at ERROR otherwise
(`TestAReportSettlesBySessionState`). A report sent again after a refund no
longer logs money held, since the payment module asks PayTR before refusing a
report of closed sessions only, and the cart cancel's 409 carries
`Retry-After` and is described.

## 4. What the storefront is told after the report completed the cart

ADR 0439 answers a completed cart with the unknown cart's 404 on every
storefront route. After PayTR's callback completed the cart, the storefront's
own completion on PayTR's success page would have met that 404 and never
learned its order or its token. The first form of the answer took the cart's
id and the session's id as the proof, once within an hour of the report; the
review of the rebase found a log line pairing the two at INFO, so a log reader
could claim the order of a shopper who closed the browser. The opening now
hands the storefront a `completion_secret` (`cpsec_`, 256 random bits) that
the payment module keeps as a SHA-256 digest (`payment_session_answers`); a
completion of a completed cart reaches the checkout only when it presents one,
the checkout spends it once before issuing the token, and the cart module
answers every other outcome — no secret, another one, spent, another
payment's cart, the cart workflow's own refusal — with the same 404
(`TestATakenSessionIsAnsweredOnceWithItsToken`,
`TestTheStorefrontsOwnCompletionGetsTheTokenForItsSecret`,
`TestACompletedCartAnswersItsReportedSessionOnce`, and on PostgreSQL
`TestACompletionSecretIsSpentOnceOnPostgres`). On the e2e ground the ids alone,
and the ids with a guessed secret, meet the 404 while the shopper's secret is
still answered (`TestAPayTRCustomerWhoNeverReturnsGetsTheOrder`). Its
re-review found the same ids still enough while the cart is open — a report
waiting for stock leaves it open for hours, and the report's log lines name
the session — so the saga's own answer to a completion paid with an opened
session issues the token only for that secret too, spending it, and answers
the order without one otherwise
(`TestTheStorefrontsOwnCompletionGetsTheTokenForItsSecret`); a fault on a
completed cart's completion stays a fault rather than a recorded 404. The
server's own completion asks for no token: the order mail carries one. A report the queue never
took — the process stopped between PayTR's row and the queue's — is found by
the `payment-reports` job every five minutes asking PayTR about the pending
sessions of the last day (`TestDiscoveryFindsAPaymentWhoseReportNeverArrived`,
`TestASweepAttemptsOnlyWhatIsDue`); PayTR answers from its own table, so the
question costs a row read, not a network call.

## 5. Why the pending sessions have a page of their own, and a window

The reconciliation job reads at most fifty authorized sessions a pass
(`limit`), oldest first, every hour, after a fifteen-minute settling window. A
PayTR payment page the customer leaves unpaid stays `pending` for good: nothing
cancels it, because PayTR cannot void a token, and a payment made on a page
whose session was canceled lands on that canceled session — which is why the
page reads canceled sessions too. Mixed into
the oldest-first page, the first fifty abandoned pages would fill every pass
from then on, and a card's authorized session, and every newer PayTR payment,
would never be read again.

The first form of the second page read the newest fifty pending sessions. The
review measured its hole by arithmetic: a session first eligible at a pass is
skipped by that pass and every later one when more than fifty pending sessions
settled after it and before the pass's own cut — with a hundred abandoned pages
an hour, about half of the paid ones, never reported. The page now reads the
window of the last settled day, newest first, up to `MaxReconcileLimit`
(1 000), canceled sessions with the pending ones, so every session in it is
asked about on each pass while it is among the newest thousand. A full page is reported (`UnreportedTruncated`, not clean,
in `gobit jobs`) only when its oldest session is younger than an hour, so a
pass that missed sessions inside an interval between passes says so and a quiet
day of a thousand abandoned pages does not:
`TestAPageThatCannotCoverAnIntervalIsReported` (1 001 sessions a second apart:
1 000 asked, truncated, not clean) and
`TestADayLargerThanThePageThatCoversAnIntervalIsNotReported` (1 001 a minute
apart: 1 000 asked, clean). `TestThePendingPageIsItsOwn` and
`TestThePendingPageReadsTheLastSettledDay` hold the page on the service,
`TestThePendingListingSelectsOnlyTheUnreportedSet` (canceled read, declined
and authorized not) and
`TestThePendingListingIsNewestFirst` on PostgreSQL, and
`TestThePendingListingUsesItsIndex` the plan:
`payment_sessions_pending_reconcile_idx` chosen over 20 000 captured rows, no
sequential scan.

## 6. The installment difference

PayTR's callback signs `merchant_oid + salt + status + total_amount`, and PayTR's
iFrame API documentation describes `total_amount` as what the customer paid,
the installment difference included, and the unsigned `payment_amount` as the
order's figure; the plugin reads only the signed one. Nothing here observed a
real PayTR test-mode payment in installments, so the figure is PayTR's word, not
a measurement. What the plugin does with it is measured by
`TestAnInstallmentPaymentIsAuthorizedForTheSessionsAmount` (10 000 asked, 10 500
reported: authorized 10 000, capture 10 000 accepted and 10 001 refused,
inspected 10 000, row 10 500) and
`TestAnInstallmentPaymentIsRefundedForAtMostTheOrder` (a refund of 10 001
refused before PayTR is called; the cancel sends `100.00` once).

## 7. A refusal that said "not yet" was replayed

The first form of the fix answered the early completion 409 without
`Retry-After`. The idempotency ring records every response below 500, so a
storefront sending its completion again under the same `Idempotency-Key` — the
way a retry is meant to be sent — got the recorded 409 back for the record's
lifetime, after PayTR had reported. Measured by mutants M14 (the ring records a
`Retry-After` answer) and M13 (the refusal carries none): both turn
`TestAPayTRCheckoutPlacesAnOrder` red at the second completion.

## 8. Mutants

Every mutant is an exact edit, each anchor matched once, run with `-count=1`
after a baseline of every command it names came back green, its files restored
after it (`mutate.py` outside the tree). 175 mutants, 175 killed. The last full
run, of 179 on the rebased tree, killed 160; fourteen failed only to build (an
unused variable once a condition was struck out) and were re-expressed to keep
the variable read, and five survived. Two of those were lane faults rather
than test gaps — the PostgreSQL lane's pattern left out the secret's own test
— and went into a second run with the fourteen. The others led to code, not to
the table: an uncaptured session answered with an order (R26) and a running
completion without a session answered "not yet" (R29c) each got a test; the
mapping of the engine's contended execution to "not yet" (R29b) and a guard
against an empty secret, equivalent to the query it saved, were removed. The
second run's one survivor, the cart module's mapping of two checkout codes on
an open cart, was removed with its codes, since no path reached it.
Lanes: checkout-unit `./internal/workflows/checkout/`,
payment-unit `./internal/modules/payment/service/`, payment-int
`./internal/modules/payment/` (`-run 'Pending|PaidPage|Reconciliation|ReportQueue|CompletionSecret'`),
paytr-int `./plugins/paymentpaytr/`, cart-unit, core/http, core/plugin,
recon-job, reports-job, app-pin `TestPayTRSaysItsCustomerAuthorizes`, arch-pin
the constant pins, e2e the PayTR, opened-payment, report, cancel, matrix and
description tests. The table lists at most three killers a lane.

| # | Mutant | Result | Killed by (lane: first tests) |
|---|---|---|---|
| M1 | the completion ignores the opened session | KILLED | checkout-unit: `TestACompletionNamingAnOpenedSessionAuthorizesIt`, `TestACompletionOfASessionAlreadyRunningSaysNotYet`, `TestACustomerAuthorizingProviderWaitsForItsReport`; e2e: `TestADrivenProviderCanBeOpenedFirst`, `TestAPayTRCheckoutPlacesAnOrder`, `TestAPayTRCustomerWhoNeverReturnsGetsTheOrder` |
| M2 | a failed authorization releases the customer's page | KILLED | checkout-unit: `TestAFailedAuthorizationLeavesTheOpenedSessionOpen` |
| M2b | a driven provider's failed authorization keeps its hold | KILLED | checkout-unit: `TestADrivenProvidersOpenedSessionIsReleasedWhenItsAuthorizationFails` |
| M4 | the order bound before the authorization | KILLED | checkout-unit: `TestACompletionNamingAnOpenedSessionAuthorizesIt`, `TestADrivenProvidersOpenedSessionIsReleasedWhenItsAuthorizationFails`, `TestAFailedAuthorizationLeavesTheOpenedSessionOpen` |
| M3+M4 | no readiness gate and the order bound first | KILLED | e2e: `TestAPayTRCheckoutPlacesAnOrder` |
| M22 | a hold whose order cannot be bound is kept | KILLED | checkout-unit: `TestAnOpenedSessionWhoseOrderCannotBeBoundIsReleased` |
| M23 | a short hold accepted | KILLED | checkout-unit: `TestAnOpenedSessionHeldShortIsReleased` |
| M24 | the compensation does not know the opened session | KILLED | checkout-unit: `TestACompletionNamingAnOpenedSessionAuthorizesIt`, `TestACompletionOfASessionAlreadyRunningSaysNotYet`, `TestACustomerAuthorizingProviderWaitsForItsReport`; e2e: `TestADrivenProviderCanBeOpenedFirst`, `TestAPayTRCheckoutPlacesAnOrder`, `TestAPayTRCustomerWhoNeverReturnsGetsTheOrder` |
| M3 | the readiness gate removed | KILLED | checkout-unit: `TestACustomerAuthorizingProviderWaitsForItsReport`; e2e: `TestAPayTRCheckoutPlacesAnOrder` |
| M26 | a reported refusal not final | KILLED | checkout-unit: `TestACustomerAuthorizingProviderWaitsForItsReport`, `TestAReportClosedWhileItRanSettlesByState` |
| M27 | a driven provider's session waits for a report | KILLED | checkout-unit: `TestACompletionNamingAnOpenedSessionAuthorizesIt`, `TestADrivenProviderIsNotAskedForItsReport`, `TestADrivenProvidersOpenedSessionIsReleasedWhenItsAuthorizationFails`; e2e: `TestADrivenProviderCanBeOpenedFirst` |
| M27b | an authorized session asked about | KILLED | checkout-unit: `TestACustomerAuthorizingProviderWaitsForItsReport` |
| M6 | the session's cart not compared | KILLED | checkout-unit: `TestACompletionIsHeldToItsOpenedSession`, `TestAForeignSessionsRefusalNamesNoOtherCart`, `TestATakenSessionAnswersOnlyItsOwnCompletion`; e2e: `TestAPayTRSessionPaysOnlyForItsOwnCart` |
| M8 | the session's provider not compared | KILLED | checkout-unit: `TestACompletionIsHeldToItsOpenedSession`, `TestATakenSessionAnswersOnlyItsOwnCompletion`; e2e: `TestAPayTRSessionPaysOnlyForItsOwnCart` |
| M40 | the session's customer not compared | KILLED | checkout-unit: `TestACompletionIsHeldToItsOpenedSession`, `TestAReportThatCanNeverPlaceItsCartIsRefunded` |
| M40b | a guest's session refused for a signed-in cart | KILLED | checkout-unit: `TestACapturedSessionWithNoOrderIsLeftToThePreparation`, `TestACompletionIsHeldToItsOpenedSession`, `TestACompletionNamingAnOpenedSessionAuthorizesIt` |
| M53 | the refusal names the other cart | KILLED | checkout-unit: `TestAForeignSessionsRefusalNamesNoOtherCart` |
| M7 | the amount not compared | KILLED | checkout-unit: `TestACompletionIsHeldToItsOpenedSession`, `TestAnAbandonedCompletionIsNotRunning` |
| M7b | the collection's amount not compared | KILLED | checkout-unit: `TestACompletionIsHeldToItsOpenedSession` |
| M7c | the currencies not compared | KILLED | checkout-unit: `TestACompletionIsHeldToItsOpenedSession`, `TestAReportThatCanNeverPlaceItsCartIsRefunded` |
| M15 | a closed session accepted | KILLED | checkout-unit: `TestACapturedSessionWithNoOrderIsLeftToThePreparation`, `TestACompletionIsHeldToItsOpenedSession`, `TestAReportClosedWhileItRanSettlesByState` |
| M15b | a collection that captured not closed | KILLED | checkout-unit: `TestACompletionIsHeldToItsOpenedSession` |
| M9 | a customer-authorizing provider completes without a session | KILLED | checkout-unit: `TestACustomerAuthorizingProviderNeedsAnOpenedSession`; e2e: `TestAOneCallPayTRCompletionIsRefusedBeforeItStarts` |
| M34 | the plan does not record the session | KILLED | checkout-unit: `TestACompletionNamingAnOpenedSessionAuthorizesIt`, `TestACompletionOfASessionAlreadyRunningSaysNotYet`, `TestACustomerAuthorizingProviderWaitsForItsReport`; e2e: `TestADrivenProviderCanBeOpenedFirst`, `TestAPayTRCheckoutPlacesAnOrder`, `TestAPayTRCustomerWhoNeverReturnsGetsTheOrder` |
| M41 | the plan does not record the customer's page | KILLED | checkout-unit: `TestAFailedAuthorizationLeavesTheOpenedSessionOpen` |
| M42 | the opened session's tender checked again | KILLED | checkout-unit: `TestTheOpenedSessionsTenderIsNotCheckedAgain` |
| M5 | the opened payment hands back no provider data | KILLED | checkout-unit: `TestOpenPaymentJSONCrossesTheBoundary`, `TestOpenPaymentOpensACollectionForTheTotalAndASession`; e2e: `TestAPayTRCheckoutPlacesAnOrder` |
| M30 | every opening shares one key | KILLED | checkout-unit: `TestOpenPaymentOpensACollectionForTheTotalAndASession`; e2e: `TestAPayTRCheckoutPlacesAnOrder`, `TestAPayTRCustomerWhoNeverReturnsGetsTheOrder`, `TestAPayTROrderIDIsNotPaidTwice` |
| M31 | the opening does not compare the confirmed total | KILLED | checkout-unit: `TestOpenPaymentRefusesWhatTheCompletionRefuses` |
| M58 | a balance opened first | KILLED | checkout-unit: `TestAShopsOwnBalanceIsNotOpenedFirst` |
| M59 | a cancel through another cart | KILLED | checkout-unit: `TestCancelPaymentRefusesAnotherCartsSession`; e2e: `TestAPaymentIsCanceledOnlyThroughItsOwnCart` |
| M60 | a completed cart's payment canceled | KILLED | checkout-unit: `TestCancelPaymentRefusesACompletedCart` |
| M61 | the cancel route cancels nothing | KILLED | checkout-unit: `TestCancelPaymentCancelsTheCartsOwnSession`, `TestCancelPaymentJSONCrossesTheBoundary`; e2e: `TestAPaymentMadeAfterItsSessionWasCanceledIsReported` |
| M25 | a session beside a gift card | KILLED | checkout-unit: `TestAnOpenedSessionPaysTheWholeTotal` |
| M35 | the interop drops payment_session_id | KILLED | checkout-unit: `TestCompleteCartJSONCarriesTheOpenedSession`; e2e: `TestADrivenProviderCanBeOpenedFirst`, `TestAPayTRCheckoutPlacesAnOrder`, `TestAPayTRCustomerWhoNeverReturnsGetsTheOrder` |
| M10 | reconciliation does not read the pending page | KILLED | payment-unit: `TestADayLargerThanThePageThatCoversAnIntervalIsNotReported`, `TestAPageThatCannotCoverAnIntervalIsReported`, `TestAPaymentGivenBackIsNotMoneyWithNoOrder`; payment-int: `TestAPaidPageWithNoOrderIsReported`; e2e: `TestAPaymentMadeAfterItsSessionWasCanceledIsReported`, `TestAServerCompletionThatFailsIsTriedAgainAndReported` |
| M43 | the pending page has no window | KILLED | payment-unit: `TestThePendingPageReadsTheLastSettledDay` |
| M44 | a page that misses an interval not reported | KILLED | payment-unit: `TestAPageThatCannotCoverAnIntervalIsReported` |
| M44b | every full page reported | KILLED | payment-unit: `TestADayLargerThanThePageThatCoversAnIntervalIsNotReported` |
| M56 | a refunded pending session counted as money kept | KILLED | payment-unit: `TestAPaymentGivenBackIsNotMoneyWithNoOrder`, `TestMoneyTakenOnACanceledSessionIsReported` |
| M45 | a full pending page counted clean | KILLED | payment-unit: `TestAPageThatCannotCoverAnIntervalIsReported`; recon-job: `TestWhatCouldNotBeAskedIsSaidOutLoud` |
| M17 | the pending page read oldest first | KILLED | payment-int: `TestThePendingListingIsNewestFirst` |
| M18 | the pending page reads any provider | KILLED | payment-int: `TestThePendingListingSelectsOnlyTheUnreportedSet` |
| M18b | the pending page reads unsettled sessions | KILLED | payment-int: `TestAPaidPageWithNoOrderIsReported`, `TestThePendingListingIsNewestFirst`, `TestThePendingListingSelectsOnlyTheUnreportedSet` |
| M18c | the pending page reads closed sessions | KILLED | payment-int: `TestThePendingListingSelectsOnlyTheUnreportedSet` |
| M18d | the pending page ignores its window | KILLED | payment-int: `TestThePendingListingSelectsOnlyTheUnreportedSet` |
| M32 | the capability is never found | KILLED | payment-unit: `TestADayLargerThanThePageThatCoversAnIntervalIsNotReported`, `TestAPageThatCannotCoverAnIntervalIsReported`, `TestAPaymentGivenBackIsNotMoneyWithNoOrder`; app-pin: `TestPayTRSaysItsCustomerAuthorizes`; e2e: `TestAOneCallPayTRCompletionIsRefusedBeforeItStarts`, `TestAPayTRCheckoutPlacesAnOrder`, `TestAPaymentMadeAfterItsSessionWasCanceledIsReported` |
| M36 | a driven provider's ledger asked | KILLED | payment-unit: `TestAReportedStatusIsTheProvidersOwn` |
| M55 | the checkout is not told the collection's customer | KILLED | payment-unit: `TestTheCheckoutReadsTheSessionAsJSON` |
| M51 | the job does not warn of a full pending page | KILLED | recon-job: `TestWhatCouldNotBeAskedIsSaidOutLoud` |
| M52 | the listing leaves the full pending page out | KILLED | recon-job: `TestAFullPageOfPaymentPagesIsInTheListing` |
| M11 | PayTR authorizes what was paid | KILLED | paytr-int: `TestAnInstallmentPaymentIsAuthorizedForTheSessionsAmount`; e2e: `TestAPayTRPaymentInInstallmentsPlacesTheOrderForItsTotal` |
| M19 | PayTR's refund ceiling is what was paid | KILLED | paytr-int: `TestAnInstallmentPaymentIsRefundedForAtMostTheOrder` |
| M19b | PayTR's cancel refunds what was paid | KILLED | paytr-int: `TestAnInstallmentPaymentIsRefundedForAtMostTheOrder`; e2e: `TestAPayTRPaymentInInstallmentsPlacesTheOrderForItsTotal` |
| M20 | PayTR inspects what was paid | KILLED | paytr-int: `TestAnInstallmentPaymentIsAuthorizedForTheSessionsAmount` |
| M20b | PayTR captures up to what was paid | KILLED | paytr-int: `TestAnInstallmentPaymentIsAuthorizedForTheSessionsAmount` |
| M48 | PayTR captures what a refund gave back | KILLED | paytr-int: `TestARefundedPaymentIsNotAuthorizedOrCaptured` |
| M21 | PayTR keeps no installment difference | KILLED | paytr-int: `TestAnInstallmentPaymentIsAuthorizedForTheSessionsAmount`; e2e: `TestAPayTRPaymentInInstallmentsPlacesTheOrderForItsTotal` |
| M46 | PayTR opens an order id it reported on | KILLED | paytr-int: `TestAnOrderIDPayTRReportedOnIsNotOpenedAgain`; e2e: `TestAPayTROrderIDIsNotPaidTwice` |
| M46b | PayTR reopens a page for another amount | KILLED | paytr-int: `TestAnOrderIDPayTRReportedOnIsNotOpenedAgain` |
| M46c | PayTR adopts a refunded payment | KILLED | paytr-int: `TestAnOrderIDPayTRReportedOnIsNotOpenedAgain`; e2e: `TestAPayTROrderIDIsNotPaidTwice` |
| M46d | PayTR adopts no paid payment | KILLED | paytr-int: `TestAnOrderIDPayTRReportedOnIsNotOpenedAgain` |
| M57 | PayTR reports a refunded payment held | KILLED | paytr-int: `TestARefundedPaymentIsNotAuthorizedOrCaptured` |
| M47 | PayTR authorizes a refunded payment | KILLED | paytr-int: `TestARefundedPaymentIsNotAuthorizedOrCaptured` |
| M47b | PayTR authorizes nothing paid as everything | KILLED | paytr-int: `TestAReportOfNothingPaidIsNotAHoldOfEverything` |
| M49 | PayTR writes its row before checking the request | KILLED | paytr-int: `TestNoRowIsWrittenForARequestPayTRCouldNeverAccept` |
| M12 | PayTR's customer does not authorize | KILLED | app-pin: `TestPayTRSaysItsCustomerAuthorizes`; paytr-int: `TestPayTRSaysItsCustomerAuthorizes`; e2e: `TestAOneCallPayTRCompletionIsRefusedBeforeItStarts`, `TestAPayTRCheckoutPlacesAnOrder`, `TestAPaymentMadeAfterItsSessionWasCanceledIsReported` |
| M29 | PayTR describes nothing | KILLED | e2e: `TestEveryRealRouteIsDescribed` |
| M54 | the pending list answers outside the envelope | KILLED | paytr-int: `TestThePendingListAnswersInTheEnvelope` |
| M13 | the payment-pending refusal carries no Retry-After | KILLED | e2e: `TestAPayTRCheckoutPlacesAnOrder` |
| M14 | the ring records a Retry-After refusal | KILLED | core/http: `TestIdempotencyAResponseThatSaysWhenToAskAgainIsNotRecorded`; e2e: `TestAPayTRCheckoutPlacesAnOrder` |
| M14b | the ring drops a success that says when to look | KILLED | core/http: `TestIdempotencyASuccessThatSaysWhenToLookIsRecorded` |
| M50 | a browser cannot read Retry-After | KILLED | core/http: `TestABrowserMayReadWhenToAskAgain` |
| M37 | the completion drops payment_session_id | KILLED | cart-unit: `TestCompleteCartSendsTheOpenedSession`; e2e: `TestADrivenProviderCanBeOpenedFirst`, `TestAPayTRCheckoutPlacesAnOrder`, `TestAPayTRCustomerWhoNeverReturnsGetsTheOrder` |
| M33 | the opening takes no channels | KILLED | cart-unit: `TestOpenPaymentHandsTheProvidersDataBack` |
| M38 | the cart's code spelled apart | KILLED | arch-pin: `TestThePaymentPendingCodeAgrees`; e2e: `TestAPayTRCheckoutPlacesAnOrder` |
| M39 | a session status spelled apart | KILLED | arch-pin: `TestTheSessionStatusesAgree` |
| R1 | a report is not attempted at once | KILLED | checkout-unit: `TestAHeldSessionIsNotRefunded`, `TestARefusalBesideARunningCompletionIsNotYet`, `TestAReportClosedWhileItRanSettlesByState`; e2e: `TestAPayTRCheckoutPlacesAnOrder`, `TestAPayTRCustomerWhoNeverReturnsGetsTheOrder`, `TestAPayTROrderIDIsNotPaidTwice` |
| R2 | a captured session settles closed | KILLED | checkout-unit: `TestAReportIsNotRefundedWhenAnotherCompletionTookIt`, `TestAReportSettlesBySessionState` |
| R3 | a captured session is not settled by its state | KILLED | checkout-unit: `TestAReportIsNotRefundedWhenAnotherCompletionTookIt`, `TestAReportSettlesBySessionState` |
| R4 | a canceled session is not settled by its state | KILLED | checkout-unit: `TestAReportClosedWhileItRanSettlesByState`, `TestAReportSettlesBySessionState` |
| R4b | a declined session is not settled by its state | KILLED | checkout-unit: `TestAReportSettlesBySessionState` |
| R4c | a canceled session settles closed, its refund unsaid | KILLED | checkout-unit: `TestAReportClosedWhileItRanSettlesByState`, `TestAReportSettlesBySessionState` |
| R4d | a canceled session settles refunded whatever the provider says | KILLED | checkout-unit: `TestAReportSettlesBySessionState` |
| R5 | a session the checkout did not open is completed | KILLED | checkout-unit: `TestAReportSettlesBySessionState` |
| R5b | a session under the opening's prefix for another collection is completed | KILLED | checkout-unit: `TestAReportSettlesBySessionState` |
| R6 | a payment for a gone cart is tried again | KILLED | checkout-unit: `TestAReportThatCanNeverPlaceItsCartIsRefunded` |
| R7 | a completed cart's payment refunded without a second look | KILLED | checkout-unit: `TestAHeldSessionIsNotRefunded`, `TestAReportIsNotRefundedWhenAnotherCompletionTookIt` |
| R8 | a payment for a changed cart is tried again | KILLED | checkout-unit: `TestAReportThatCanNeverPlaceItsCartIsRefunded`, `TestAnAbandonedCompletionIsNotRunning`; e2e: `TestAPayTROrderIDIsNotPaidTwice`, `TestAPayTRPaymentForACartThatChangedIsRefunded`, `TestAPayTRPaymentInInstallmentsPlacesTheOrderForItsTotal` |
| R8e | an emptied cart's payment is tried again | KILLED | checkout-unit: `TestAReportThatCanNeverPlaceItsCartIsRefunded` |
| R8b | a stale session's payment is tried again | KILLED | checkout-unit: `TestAReportThatCanNeverPlaceItsCartIsRefunded` |
| R8c | a total mismatch's payment is tried again | KILLED | checkout-unit: `TestAReportThatCanNeverPlaceItsCartIsRefunded`, `TestAnAbandonedCompletionIsNotRunning`; e2e: `TestAPayTROrderIDIsNotPaidTwice`, `TestAPayTRPaymentForACartThatChangedIsRefunded`, `TestAPayTRPaymentInInstallmentsPlacesTheOrderForItsTotal` |
| R8d | a foreign session's payment is tried again | KILLED | checkout-unit: `TestAReportThatCanNeverPlaceItsCartIsRefunded` |
| R9 | a cart completed meanwhile is tried again | KILLED | checkout-unit: `TestAReportThatCanNeverPlaceItsCartIsRefunded`, `TestASecondPaidSessionIsNotHandedTheFirstOrder` |
| R10 | a refusal for the session's state is tried again | KILLED | checkout-unit: `TestAReportClosedWhileItRanSettlesByState` |
| R11 | the waits do not grow | KILLED | checkout-unit: `TestTheWaitsDouble` |
| R12 | a completion is never given up | KILLED | checkout-unit: `TestAFailedCompletionIsTriedAgainThenGivenUp` |
| R13 | a completion is given up an attempt early | KILLED | checkout-unit: `TestAFailedCompletionIsTriedAgainThenGivenUp`, `TestTheWaitsDouble` |
| R14 | a failed attempt is not recorded | KILLED | checkout-unit: `TestAFailedCompletionIsTriedAgainThenGivenUp`, `TestAHeldSessionIsNotRefunded`, `TestARefusalBesideARunningCompletionIsNotYet`; e2e: `TestAServerCompletionThatFailsIsTriedAgainAndReported` |
| R15 | a refund gives nothing back | KILLED | checkout-unit: `TestAReportThatCanNeverPlaceItsCartIsRefunded`, `TestASecondPaidSessionIsNotHandedTheFirstOrder`, `TestAnAbandonedCompletionIsNotRunning`; e2e: `TestAPayTROrderIDIsNotPaidTwice`, `TestAPayTRPaymentForACartCompletedOtherwiseIsRefunded`, `TestAPayTRPaymentForACartThatChangedIsRefunded` |
| R16 | a refund does not look at the session again | KILLED | checkout-unit: `TestAReportIsNotRefundedWhenAnotherCompletionTookIt` |
| R17 | the sweep finds nothing | KILLED | checkout-unit: `TestASweepAttemptsOnlyWhatIsDue`, `TestASweepThatCannotDiscoverStillAttemptsWhatIsDue`; e2e: `TestAReportThatNeverArrivedIsFoundBySweep` |
| R18 | a report just found waits its turn | KILLED | checkout-unit: `TestASweepAttemptsOnlyWhatIsDue`; e2e: `TestAReportThatNeverArrivedIsFoundBySweep` |
| R19 | the sweep attempts nothing due | KILLED | checkout-unit: `TestAFailedCompletionIsTriedAgainThenGivenUp`, `TestASweepThatCannotDiscoverStillAttemptsWhatIsDue`, `TestTheWaitsDouble`; e2e: `TestAServerCompletionThatFailsIsTriedAgainAndReported` |
| R20 | the server completion has no email | KILLED | checkout-unit: `TestAReportCompletesTheCart`; e2e: `TestAPayTRCustomerWhoNeverReturnsGetsTheOrder` |
| R21 | the server completion is not narrowed by the cart's channel | KILLED | checkout-unit: `TestAReportCompletesTheCart` |
| R47 | a report outcome spelled apart | KILLED | arch-pin: `TestTheReportOutcomesAgree` |
| R23 | a taken session's order is not answered | KILLED | checkout-unit: `TestACompletionOfATakenSessionAnswersWithItsOrder` |
| R24 | a taken session answers another cart | KILLED | checkout-unit: `TestATakenSessionAnswersOnlyItsOwnCompletion`; e2e: `TestAPayTRSessionPaysOnlyForItsOwnCart` |
| R25 | a taken session answers another provider | KILLED | checkout-unit: `TestATakenSessionAnswersOnlyItsOwnCompletion` |
| R26 | an uncaptured session answers with an order | KILLED | checkout-unit: `(non-test failure)` |
| R27 | a collection two orders hold answers with one | KILLED | checkout-unit: `TestATakenSessionAnswersOnlyItsOwnCompletion` |
| R28 | a replay owes nothing | KILLED | checkout-unit: `TestATakenSessionAnswersOnlyItsOwnCompletion` |
| R29 | a running completion of a session is not 'not yet' | KILLED | checkout-unit: `TestACompletionOfASessionAlreadyRunningSaysNotYet`, `TestAHeldSessionIsNotAnsweredWithItsOrder` |
| R29c | a running completion without a session is 'not yet' | KILLED | checkout-unit: `TestARunningCompletionWithoutASessionIsTheEnginesConflict` |
| R30 | PayTR reports nothing | KILLED | paytr-int: `TestAPaidCallbackIsReportedToTheCheckout`; e2e: `TestAPayTRCheckoutPlacesAnOrder`, `TestAPayTRCustomerWhoNeverReturnsGetsTheOrder`, `TestAPayTROrderIDIsNotPaidTwice` |
| R31 | PayTR reports a refused payment | KILLED | paytr-int: `TestAPaidCallbackIsReportedToTheCheckout` |
| R32 | PayTR is never given the door | KILLED | e2e: `TestAPayTRCheckoutPlacesAnOrder`, `TestAPayTRCustomerWhoNeverReturnsGetsTheOrder`, `TestAPayTROrderIDIsNotPaidTwice` |
| R33 | the door forwards nothing | KILLED | core/plugin: `TestThePaymentReportsDoorResolvesWhenItIsUsed`; e2e: `TestAPayTRCheckoutPlacesAnOrder`, `TestAPayTRCustomerWhoNeverReturnsGetsTheOrder`, `TestAPayTROrderIDIsNotPaidTwice` |
| R34 | nothing stands behind the door | KILLED | e2e: `TestAPayTRCheckoutPlacesAnOrder`, `TestAPayTRCustomerWhoNeverReturnsGetsTheOrder`, `TestAPayTROrderIDIsNotPaidTwice` |
| R35 | a report opens closed sessions | KILLED | payment-unit: `TestAReportOfOnlyClosedSessionsIsRefused`, `TestAReportOpensTheOpenSessionsItNames` |
| R36 | discovery counts money given back | KILLED | payment-unit: `TestDiscoveryFindsAPaymentWhoseReportNeverArrived` |
| R37 | discovery finds a report already made | KILLED | payment-unit: `TestDiscoveryFindsAPaymentWhoseReportNeverArrived` |
| R38 | discovery has no window | KILLED | payment-unit: `TestDiscoveryFindsAPaymentWhoseReportNeverArrived` |
| R39 | discovery reports a page nobody paid on | KILLED | payment-unit: `TestDiscoveryFindsAPaymentWhoseReportNeverArrived` |
| R40 | the due read is a day early | KILLED | payment-int: `TestTheReportQueueKeepsItsAttemptsOnPostgres` |
| R40b | the due read reads settled reports | KILLED | payment-int: `TestTheReportQueueKeepsItsAttemptsOnPostgres` |
| R41 | a second report reopens the first | KILLED | payment-int: `TestTheReportQueueKeepsItsAttemptsOnPostgres` |
| R42 | a settled report is settled again | KILLED | payment-int: `TestTheReportQueueKeepsItsAttemptsOnPostgres` |
| R43 | a settled report counts attempts | KILLED | payment-int: `TestTheReportQueueKeepsItsAttemptsOnPostgres` |
| R44 | the job does not warn of money given up | KILLED | reports-job: `TestMoneyThatNeedsAPersonIsSaidOutLoud` |
| R45 | the job's line drops what it gave up | KILLED | reports-job: `TestARunReportsEveryCountEvenWhenItFails` |
| R46 | the job runs hourly | KILLED | reports-job: `TestTheDefinitionFitsItsInterval` |
| N1 | a refusal of a named session is final | KILLED | checkout-unit: `TestACompletionThatMeetsTheOrderItRacedAnswersWithIt`, `TestARefusalBesideARunningCompletionIsNotYet` |
| N2 | a running completion beside a refusal is ignored | KILLED | checkout-unit: `TestARefusalBesideARunningCompletionIsNotYet` |
| N3 | a refusal does not look for the order it raced | KILLED | checkout-unit: `TestACompletionThatMeetsTheOrderItRacedAnswersWithIt` |
| N4 | no completion is ever running | KILLED | checkout-unit: `TestARefusalBesideARunningCompletionIsNotYet`, `TestAReportForAGoneCartWaitsForARunningCompletion` |
| N5 | a finished completion counts as running | KILLED | checkout-unit: `TestASecondPaidSessionIsNotHandedTheFirstOrder`, `TestAnAbandonedCompletionIsNotRunning`; e2e: `TestAPayTRPaymentForACartCompletedOtherwiseIsRefunded` |
| P1 | an abandoned completion counts as running | KILLED | checkout-unit: `TestAnAbandonedCompletionIsNotRunning` |
| N6 | a replay of another session's completion is accepted | KILLED | checkout-unit: `TestASecondPaidSessionIsNotHandedTheFirstOrder` |
| N7 | a cart's payment canceled beside a running completion | KILLED | checkout-unit: `TestARefusalBesideARunningCompletionIsNotYet` |
| N8 | a held session refunded | KILLED | checkout-unit: `TestAHeldSessionIsNotRefunded` |
| N9 | a payment refunded beside a running completion | KILLED | checkout-unit: `TestAReportForAGoneCartWaitsForARunningCompletion` |
| N15 | a report of a closed session is an error to resend | KILLED | checkout-unit: `TestAPaymentForAClosedSessionIsTakenAndLeftToAPerson` |
| N17 | a discovery failure stops the sweep | KILLED | checkout-unit: `TestASweepThatCannotDiscoverStillAttemptsWhatIsDue` |
| N18 | a discovery failure is not said | KILLED | checkout-unit: `TestASweepThatCannotDiscoverStillAttemptsWhatIsDue` |
| N10 | a report of only closed sessions is taken in silence | KILLED | payment-unit: `TestAReportOfOnlyClosedSessionsIsRefused` |
| P5 | a report of closed sessions refused though the money went back | KILLED | payment-unit: `TestAReportOfOnlyClosedSessionsIsRefused` |
| P6 | the provider never holds money | KILLED | payment-unit: `TestAReportOfOnlyClosedSessionsIsRefused` |
| N11 | a settled report is named again | KILLED | payment-unit: `TestASettledReportIsNotNamedAgain` |
| N12 | a new report is due at once | KILLED | payment-unit: `TestASettledReportIsNotNamedAgain`; payment-int: `TestTheReportQueueKeepsItsAttemptsOnPostgres` |
| N13 | discovery opens a report for a canceled session | KILLED | payment-unit: `TestMoneyTakenOnACanceledSessionIsReported` |
| N14 | a canceled session's refund counted as money kept | KILLED | payment-unit: `TestMoneyTakenOnACanceledSessionIsReported` |
| N16 | the second page does not read canceled sessions | KILLED | payment-int: `TestThePendingListingSelectsOnlyTheUnreportedSet`; e2e: `TestAPaymentMadeAfterItsSessionWasCanceledIsReported` |
| N22 | the first due spelled apart from the first retry | KILLED | arch-pin: `TestTheReportQueueAndTheCheckoutAgreeOnTiming` |
| N23 | the closed-session code spelled apart | KILLED | arch-pin: `TestTheReportQueueAndTheCheckoutAgreeOnTiming` |
| Q1 | a taken session answered again and again | KILLED | checkout-unit: `TestATakenSessionIsAnsweredOnceWithItsToken`; e2e: `TestAPayTRCheckoutPlacesAnOrder`, `TestAPayTRCustomerWhoNeverReturnsGetsTheOrder` |
| Q2 | the answer carries no token | KILLED | checkout-unit: `TestATakenSessionIsAnsweredOnceWithItsToken`; e2e: `TestAPayTRCheckoutPlacesAnOrder` |
| Q3 | the answer asks for no secret | KILLED | checkout-unit: `TestATakenSessionIsAnsweredOnceWithItsToken` |
| Q4 | the saga's answer gives a session-paid order its token without the secret | KILLED | checkout-unit: `TestTheStorefrontsOwnCompletionGetsTheTokenForItsSecret` |
| Q4d | the saga's answer spends no secret | KILLED | checkout-unit: `TestTheStorefrontsOwnCompletionGetsTheTokenForItsSecret` |
| Q4e | an order paid without a session gets no token | KILLED | checkout-unit: `TestACompletionThatAsksIssuesTheOrdersToken`, `TestTheInteropAnswersTheTokenOnlyWhenAsked` |
| Q4b | a caller that asks for no token spends the secret | KILLED | checkout-unit: `TestACompletionOfATakenSessionAnswersWithItsOrder`, `TestACompletionThatMeetsTheOrderItRacedAnswersWithIt`, `TestATakenSessionAnswersOnlyItsOwnCompletion` |
| Q4c | the opening hands out no secret | KILLED | checkout-unit: `TestOpenPaymentJSONCrossesTheBoundary`, `TestOpenPaymentOpensACollectionForTheTotalAndASession`; e2e: `TestADrivenProviderCanBeOpenedFirst`, `TestAPayTRCheckoutPlacesAnOrder`, `TestAPayTRCustomerWhoNeverReturnsGetsTheOrder` |
| Q5 | a refusal on a completed cart tells it completed | KILLED | cart-unit: `TestACompletedCartAnswersItsReportedSessionOnce`; e2e: `TestAPayTRCheckoutPlacesAnOrder`, `TestAPayTRCustomerWhoNeverReturnsGetsTheOrder` |
| Q6 | a completed cart's completion reaches the flow without a session | KILLED | cart-unit: `TestEveryStorefrontRouteRefusesACompletedCart` |
| Q7 | a completed cart's session completion never reaches the flow | KILLED | cart-unit: `TestACompletedCartAnswersItsReportedSessionOnce`; e2e: `TestAPayTRCheckoutPlacesAnOrder`, `TestAPayTRCustomerWhoNeverReturnsGetsTheOrder`, `TestAPayTRPaymentInInstallmentsPlacesTheOrderForItsTotal` |
| Q7b | a completed cart reaches the flow without the secret | KILLED | cart-unit: `TestACompletedCartAnswersItsReportedSessionOnce` |
| Q8 | the cancel's refusal says not when to ask again | KILLED | cart-unit: `TestACancelBesideARunningCompletionSaysWhenToAskAgain` |
| Q9 | a completed cart's payment opened again | KILLED | cart-unit: `TestEveryStorefrontRouteRefusesACompletedCart` |
| Q10 | a completed cart's payment canceled at the storefront | KILLED | cart-unit: `TestEveryStorefrontRouteRefusesACompletedCart` |
| Q11 | the secret spent twice | KILLED | go test -tags integration -count=1 -timeout 15m -run 'Pending|PaidPage|Reconciliation|ReportQueue|CompletionSecret' ./internal/modules/payment/: `TestACompletionSecretIsSpentOnceOnPostgres` |
| Q12 | any secret spends the session's | KILLED | go test -tags integration -count=1 -timeout 15m -run 'Pending|PaidPage|Reconciliation|ReportQueue|CompletionSecret' ./internal/modules/payment/: `TestACompletionSecretIsSpentOnceOnPostgres`; e2e: `TestAPayTRCustomerWhoNeverReturnsGetsTheOrder` |
| Q14 | a fault on a completed cart becomes its 404 | KILLED | cart-unit: `TestACompletedCartAnswersItsReportedSessionOnce` |
| Q16 | the plaintext kept | KILLED | go test -tags integration -count=1 -timeout 15m -run 'Pending|PaidPage|Reconciliation|ReportQueue|CompletionSecret' ./internal/modules/payment/: `TestACompletionSecretIsSpentOnceOnPostgres` |
