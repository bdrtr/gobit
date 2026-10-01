# ADR 0284 — An offline method is paid when the shop says so

**Summary:** A shop names its offline payment methods — a bank transfer, cash on
delivery — and each is a provider whose session the checkout authorizes and
does not capture, so the order is placed owing that part until the shop
captures it.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

Every order the checkout placed was paid in full at the checkout: the saga
captured each session it authorized before it closed the cart. A shop that takes
a bank transfer or cash at the door had no tender for it. The manual provider
captured whatever it was told and, since ADR 0283, production does not register
it. An operator taking an order by telephone hit the same wall. The order
module already reads an order's paid total from the payment events, and the
admin can capture a session, so a payment that arrives later had somewhere to
land. It had no way to begin.

## Decision

`PAYMENT_OFFLINE_METHODS` names the offline methods, each registered as a
provider that keeps no ledger and says its money comes later. The checkout
records that answer on its plan, authorizes the session without capturing it,
and places the order owing that part, which the completion reports as
`outstanding`.

## Consequences

- A shopper can pay a gift card or a balance now and the rest by transfer; the
  first tenders are captured at the checkout, and the transfer's session stays
  authorized until `POST /admin/v1/payment-sessions/{id}/capture` records the
  money, when `payment.captured` raises the order's paid total.
- A checkout that captured nothing has moved no money, so a fault in its
  verification rolls the saga back rather than stopping it for a person.
- Reconciliation leaves the offline sessions out of its suspects: one that stays
  authorized for a week is the method working, and the oldest-first page would
  otherwise fill with them before reaching a card's session.
- An order that owes is placed with its stock deducted, and a shopper can place
  as many as the shop will take. Nothing cancels one that is never paid; a shop
  that offers a method watches for them.
- No method is offered by default; a name outside lower-case letters, digits and
  underscores, or one that is already a provider, stops the startup.

## Rejected

- One `offline` provider with the method in the session's data: the client
  would choose the method's name, and an order could not say which method it
  was promised with.
- A ledger table for the provider: every amount it would hold is the payment
  module's session, checked before each call.
- Capturing at the checkout and refunding when the money does not come: the
  order would read as paid for a week with nothing paid.
- Publishing the capability in `core/provider`: nothing outside the repository
  implements it yet (ADR 0026).
