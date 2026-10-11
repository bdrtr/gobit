# ADR 0438 — A shopper's payment is opened before the cart is completed

**Summary:** A storefront opens its cart's payment with `POST /store/v1/carts/{id}/payment-sessions`, and the provider's report that the customer paid (PayTR's callback) completes the cart on the server; the storefront's own completion of that session answers with the same order.
It costs money that moves before stock is reserved, a durable report queue retried with backoff, and a refund when the cart no longer matches.

- **Status:** Accepted
- **Date:** 2026-10-11
- **Amends:** [0020](0020-reconciliation-asks-the-provider-and-reports.md), whose suspect set gains the pending sessions of such a provider, and [0439](0439-an-id-gobit-hands-out-opens-nothing-at-the-storefront.md), whose storefront takes no payment session's id: here it names one under its own cart

Measurement: [measurements/0438](../measurements/0438-what-paytr-could-not-complete.md)

## Context

The documented checkout is one `POST /store/v1/carts/{id}/complete`, and with
PayTR it never placed an order: the completion opened PayTR's session and asked
whether it was paid in the same request, so the iframe token never left the
server, a payment made anyway met a canceled session, and a retried completion
opened a new collection and a new `merchant_oid` (D284). A customer paying in
installments was refused as a provider contract violation and refunded (D285).
The plugin had never stood on the end-to-end ground, so the authorization
matrix and the schema gate had never seen its callback or its pending list
(D286). A shopper who pays and closes the browser never brings the storefront
back to complete the cart, so the payment's report has to.

## Decision

`POST /store/v1/carts/{id}/payment-sessions` recomputes the cart, opens a collection for its total and a session at the named provider and answers with the provider's data, and a completion naming that `payment_session_id` authorizes it, binds the order once the money is held, answers a session an order already took with that order — the storefront once, with the order's access token, for the completion secret the opening handed it — and refuses before the saga a session of another cart, provider or customer, for another total, closed, or beside a gift card or a balance.
A provider whose customer pays on its own page says so through an internal capability, so a completion naming it without a session is refused and one made before the provider reported is refused 409 with `Retry-After`, while the provider's report, taken through `core/plugin`'s `PaymentReports`, completes the cart on the server, retried with backoff from a report queue and refunded when the cart is gone, changed or paid otherwise.

## Consequences

- PayTR's callback places the order, and the mail carries its token. A
  completion paid with an opened session gets the order's `order_token` once,
  for the opening's `completion_secret`, kept as a digest: without it an open
  cart's order is answered tokenless and a completed cart is the unknown
  cart's 404 (ADR 0439). It answers 409 with `Retry-After`, not recorded under
  its `Idempotency-Key`, while the order has not landed.
- One order per cart: the checkout's saga key admits one completion; a second
  running, or refused beside one still running, is answered "not yet"; a
  captured session answers with its order; another paid session of a cart an
  order took is refunded, and nothing refunds a session a completion holds.
- The report queue, `payment_session_reports` (migration 000017), keeps each
  report's attempts and outcome. A failed completion is retried five minutes
  later and at doubling waits, six attempts in all, then given up; a refusal
  that can never succeed is refunded. The `payment-reports` job retries, and
  finds a payment whose report never arrived by asking the provider.
- For PayTR the money moves before stock is reserved. A payment still held with
  no order is reported by reconciliation, whose second page reads the pending
  and canceled sessions of such a provider settled in the last day, newest
  first, up to a thousand, under migration 000017's index; a payment made after
  its session was canceled is reported there, not completed.
- A storefront cancels a payment the cart no longer matches under the open
  cart, `POST /store/v1/carts/{id}/payment-sessions/{payment_session_id}/cancel`,
  refused 409 while a completion runs; the opening's answer names no collection.
- The installment difference is the customer's cost of paying in installments
  and not the shop's money: the order, the collection, every capture and every
  refund through gobit hold the session's amount, and the difference stays on
  the plugin's row and the session as `paid_amount` and
  `installment_difference`, read from PayTR's documentation and not yet
  observed in its test mode. PayTR refuses a session on an order id it reported
  refused or refunded and authorizes nothing a refund gave back.
- PayTR stands on the e2e ground; the matrix classifies the callback ring's
  routes, and `GET /admin/v1/paytr/pending` is described and answers under
  `data`, its `pending` key deprecated.
- `core/plugin` gains `PaymentReportsName`, `PaymentReports` and
  `Host.PaymentReports`. The operator's completion and `core/provider` are
  unchanged; the one-call PayTR completion, which never worked, answers 409.

## Rejected

- PayTR's callback running the saga itself: the plugin would reach into the order flow; it reports, and the checkout decides.
- A failed completion handing back the provider's data: every PayTR order would follow a canceled one and its webhook.
- A store route opening a collection for a client-sent amount: the payment module cannot compute a cart's total.
- `CreateSession` waiting for the callback: the request would outlast the saga budget and the token still never reach the shopper.
- A method on `core/provider.PaymentProvider`: every provider would change for one capability (ADR 0026).
- Retrying a failed completion in memory alone: a process that stops forgets it; the queue survives.
- Canceling a cart's other open sessions at its completion: PayTR cannot void an unpaid page, and a payment past the cancel would land on a session nobody completes.
- 503 for a payment not yet reported: every early completion would be logged and reported as a server fault.
