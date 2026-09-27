# ADR 0213 — An operator closes a gift card, and a card line is final

**Summary:** A return or a write-off that names a gift card line is refused,
since the line's cards were mailed and are their holders'. An operator closes a
card through an admin endpoint: what it held is voided, and it pays nothing and
takes no refund from then on.

- **Status:** Accepted
- **Date:** 2026-09-27
- **Amends:** [0208](0208-a-gift-card-is-a-code-with-a-balance.md), whose card paid whenever its code was presented, and [0211](0211-an-order-books-a-sold-gift-card-as-a-debt.md), which booked a returned card line as a sales return

Measurement: [measurements/0213](../measurements/0213-a-card-that-was-closed.md)

## Context

A paid order's gift card line has issued cards whose codes were mailed (ADR
0210). A paid order cannot be canceled, but a return names lines and may send
money back, and a write-off records units as not delivered, while the codes
still pay. Claims and exchanges carry an amount and no lines. The card ledger
knew an issue, a hold, a release and a refund, and nothing closed a card. The
user chose that a card line is not returned and that an operator can close a
card.

## Decision

A return request or a line write-off naming a gift card line is refused with
409 `order_gift_card_line_final`. `POST /admin/v1/gift-cards/{id}/disable`
closes a card with a reason under the card's lock, voids what it held in the
same transaction and is refused while a payment session holds part of it, and
a closed card opens no payment and takes no refund (409
`payment_gift_card_disabled`).

## Consequences

The payment journal books the void: an issued card's balance goes back to
`gift_card_granted`, and a sold card's to a new `gift_card_forfeited` account.
Across the two journals a closed sold card is owed nothing.

A refund of a collection that reaches a closed card's capture stops there. The
collection refunds its newest capture first (ADR 0118), so an order the card
paid part of is refunded capture by capture through the payment's own refund
endpoint.

Closing a closed card changes nothing, a closed card gets no new code, and
nothing reopens one.

A claim still refunds an amount on an order whose cards stand; closing a card is
how its value is taken back. A card line of an unpaid order cannot be written
off either, since the sale flow issues a card for every unit bought.

The ledger door gate names the close as the gift card ledger's second service
door. Rolling back payment migration 000011 stops while a closed card exists,
and the gift card rollback tests derive their step counts from the database's
version: a count written as a number stopped reaching its migration once a
later one existed. A
mutation that removed 000011's refusal left its test green, because the
migration's error carries the down file's text (D148); the rollback tests now
assert the constraint as the server quotes it.

## Rejected

- **Voiding a line's cards on its return.** A return would have to choose which
  of the line's cards, and to reopen them when it is withdrawn.
- **A negative balance, as points take one (ADR 0165).** A card is its holder's,
  and a debt on it is owed by nobody the shop can ask.
- **Leaving a refund's money on a closed card.** The card pays nothing, so the
  money would stay owed to nobody.
- **Canceling a card's open holds when it is closed.** A payment in flight would
  lose its tender; the close waits for the payment to end instead.
