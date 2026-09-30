# ADR 0209 — A gift card pays first and a provider the rest

**Summary:** A cart can be completed with a gift card's code beside a payment
provider: the card holds what it has, up to the total, and the provider pays the
rest. The provider's hold is captured before the card's.

- **Status:** Accepted; amended by [0269](0269-a-balance-pays-part-of-an-order.md), which lets the customer's store credit and points pay first after the card
- **Date:** 2026-09-27
- **Amends:** [0208](0208-a-gift-card-is-a-code-with-a-balance.md), whose card declined a payment it could not cover

Measurement: [measurements/0209](../measurements/0209-two-tenders-one-order.md)

## Context

ADR 0208's card had to cover the whole order, and the user chose to let a
provider pay what the card does not. The checkout opened one session, held the
whole amount on it and captured it. The payment module already leaves the part
of a collection an authorized session did not hold open to another session
(ADR 0118), and the provider contract allows a partial authorization. The
capture step rolls the saga back only when the collection proves nothing was
captured.

## Decision

The completion takes an optional `gift_card_code` beside `payment_provider_id`;
the card's session is opened first and holds the card's balance up to the total,
and the provider's session is opened for the rest, or not at all when the card
covers the order. The provider's hold is captured before the card's, and a
failure before any capture releases both holds.

## Consequences

A gift card now holds what it has rather than declining; an empty card still
declines, and the payment stops there instead of charging everything to the
provider. Alone, a card that does not cover the order holds what it has and the
checkout's full-payment rule refuses it with 409
`checkout_workflow_payment_underauthorized`, releasing the hold.

The card beside a provider is checked before the order as a card alone is: 422
for a code that opens no card, 409 for a card in another currency. The code
stays out of the execution record, as the payment's data does.
`payment_provider_id` cannot be `gift_card` beside a code, so an order has one
card at most.

The provider's capture is the one that can be ambiguous, so it goes first: if it
fails and the collection shows nothing captured, both holds are released and
the saga rolls back. The card's capture is this installation's own ledger; if it
fails after the provider's money was taken, the execution stops for a person,
as an unverifiable capture does.

A refund of the order draws the newest capture first (ADR 0118), which is the
card's: a partial return goes back onto the card before the provider. Whether
that order is right stays ADR 0118's open question.

Store credit and loyalty points still pay the whole order or nothing.

## Rejected

- **Sizing the card's session from its balance.** The balance can move between
  the read and the hold; the partial authorization decides under the card's
  lock.
- **Capturing the card first.** A provider failing after it would leave the
  card's money taken, and undoing it is a refund, which the saga never does.
- **Paying everything with the provider when the card holds nothing.** It is a
  payment the customer did not choose.
- **A list of tenders.** A tender can share an order only if it holds part of
  one, and the card is the only tender that does.
