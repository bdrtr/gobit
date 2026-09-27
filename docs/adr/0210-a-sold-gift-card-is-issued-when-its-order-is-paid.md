# ADR 0210 — A sold gift card is issued when its order is paid

**Summary:** Once an order's checkout collection is fully captured, a flow issues
a gift card for each unit of its gift card lines, worth the unit price, and mails
each code to the order's address. An operator can give a card a new code.

- **Status:** Accepted
- **Date:** 2026-09-27

Measurement: [measurements/0210](../measurements/0210-a-card-that-was-bought.md)

## Context

ADR 0208 and 0209 spend cards an operator issued; a product flagged
`is_giftcard` could be sold and nothing came of it. The user chose to issue the
card when the order's money is in, to mail its code to the order's address, to
earn points on the purchase and not on the spending, and to make the card worth
the unit price. A code is shown once and kept nowhere. The notification module
sends at once, records a (template, reference) pair once and does not retry, and
a bus handler's error is logged rather than delivered again.

## Decision

A gift card sale flow issues, when a checkout's collection is fully captured,
one card per unit of each gift card line, worth the line's unit price and named
by its line and unit, and mails each new card's code with the `gift_card.issued`
template to the order's address. An operator replaces a card's code through
`POST /admin/v1/gift-cards/{id}/code`, and money captured through a gift card no
longer earns loyalty points.

## Consequences

A card records its source, `issued` or `sold`; a sold card names its sale and a
sale names one card, so a capture delivered twice finds the card without its
code, and a code is mailed once.

The code reaches the buyer by mail only. A mail that fails, an order with no
address, or a flow stopped between the card and the mail leaves a card whose
code nobody holds; the notification log shows it, and the operator replaces the
code. The balance, holds and history stay with the card, and the old code stops.

A handler's error is not delivered again, so an order whose flow failed before
its cards were made has none, and nothing issues them later yet.

The value is the unit price before discount; a promotion that should not
discount cards says `is_giftcard ne true`. A collection captured in part waits
for the capture that completes it (ADR 0209). A gift card line is stocked and
shipped like any other line; nothing marks it digital.

Buying a card earns points on the money captured; spending one earns none,
issued cards' included. The payment journal books an issued card's issue and not
a sold one's, which is the order's sale; the order's books still count it as
sales, until they learn which of an order's lines were cards.

A notification provider has to know `gift_card.issued` to deliver the code.
Rolling back migration 000010 stops while a sold card exists.

A mutation that took the flow out of production alone left every test green,
because the end-to-end ground wires flows on its own (D146); a gate now holds
production to every flow the ground wires.

## Rejected

- **Issuing inside the checkout saga after the capture.** A failure there cannot
  be undone, and it would stop a paid order for a person over its card.
- **The code kept encrypted, to mail it again.** ADR 0208's reason: whoever holds
  the key reads every card.
- **A new card for a lost code.** The old card's holds and refunds would land on
  a card nobody can spend.
- **Points on the spending.** The payment module does not know which part of a
  purchase was a card, so the purchase could not be left out.
