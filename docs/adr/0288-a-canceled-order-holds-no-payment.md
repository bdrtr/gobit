# ADR 0288 — A canceled order holds no payment

**Summary:** Every cancel of an order, the shop's and the checkout's
compensation, publishes `order.canceled`. The payment module answers it by
closing each session of the order's collection that is still authorized.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

ADR 0285 gave a canceled order's stock back and left its payment session
authorized, for `POST /admin/v1/payment-sessions/{id}/cancel` to close. ADR
0287 then printed that session on the order page with a button that records the
money, so a canceled order offered to take payment. A cancel published nothing.
`order.placed` goes out when the checkout writes the order, and an order the
checkout unwound or the shop canceled was never announced as ended, not to a
webhook receiver and not to the payment module. The order module may not touch
a payment session (ADR 0006); the payment module defines the `order_payment`
link.

## Decision

Both cancels write `order.canceled`, carrying the order and the moment, into the
outbox in the cancel's transaction and publish it after the commit. The payment
module subscribes to it and cancels each session of the order's collection that
is still authorized.

## Consequences

- An unpaid offline order the shop cancels closes its promise, and the order
  page stops offering to record it once the event is handled.
- A card's authorization left behind a canceled order, by a checkout that died
  between authorizing and capturing, is released instead of held on the
  shopper's card.
- The close is eventual. Between the commit and the handling, a record or a
  capture can still land; it takes the locks the close takes, so only one of the
  two happens, and a capture that wins is logged as an error and refunded
  through the API.
- A webhook receiver can subscribe to `order.canceled`. It carries no reason,
  because the reason is operator text with no redaction rule.
- The checkout's compensation publishes it too. Its own steps already cancel
  the sessions it authorized, and the payment module's close of those changes
  nothing.
- A cancel whose event cannot be written fails, as a write-off does. ADR 0285's
  compensation still writes nothing off; it now writes this event.

## Rejected

- Closing only the offline methods' sessions: a card's authorization behind a
  canceled order holds the shopper's money for the same reason.
- A flow above the two modules: the payment module already reads the link it
  defines, and the flow would hold a single call.
- Closing the session in the order API's handler: the order module may not call
  the payment module, and a close made after the commit is lost with the
  process.
- Hiding the panel's form on a canceled order: the session would stay
  authorized, and the API's capture would still take it.
