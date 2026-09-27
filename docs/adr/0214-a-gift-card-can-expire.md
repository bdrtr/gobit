# ADR 0214 — A gift card can expire

**Summary:** A gift card may carry a moment after which it pays nothing: the one
an operator names at issue, or else the installation's validity in days, zero
by default and meaning never. A job closes a card whose moment has come, voiding
what it held as an operator's close does.

- **Status:** Accepted
- **Date:** 2026-09-27
- **Amends:** [0208](0208-a-gift-card-is-a-code-with-a-balance.md), whose card paid for ever, and [0210](0210-a-sold-gift-card-is-issued-when-its-order-is-paid.md), whose mail did not say when the card stops

Measurement: [measurements/0214](../measurements/0214-a-card-that-ran-out.md)

## Context

A card paid for as long as its code was presented. The user chose an
installation validity with a per-card moment, and that a card whose moment has
come is closed and its balance written off. ADR 0213 closes a card under its
lock, voids what it held and books it by the card's source. The runner runs
jobs on an interval, and ADR 0017 admits a scheduled write that does what was
already decided, as the scheduled publisher does.

## Decision

A card is made with the moment its operator names, which has to be ahead, or
else `PAYMENT_GIFT_CARD_VALIDITY_DAYS` after its issue, or none when that is
zero, the default; the moment never moves. From it the card opens no payment
and takes no refund (409 `payment_gift_card_expired`), and a `gift-card-expiry`
job closes it every fifteen minutes through ADR 0213's close with the reason
`expired`.

## Consequences

The validity counts from the same `now()` the card is stamped with. A sold card
takes the installation's validity, and its mail carries `expires_at`, empty for
a card that never expires; the admin read and the issue's answer carry it too.

The storefront refuses a card from its moment, whatever the job has done; the
job only decides how soon the books show it. A card an authorized payment still holds is closed by a later run, and
a payment opened before the moment may still be taken.

Changing the validity changes the cards made afterwards; an issued card cannot
be made to never expire while the validity is set, and nothing extends a
moment. Cards made before this record never expire.

The ceiling is a hundred years, held in the configuration and the payment
service alike and bound by a gate. Rolling back payment migration 000012 stops
while a card has a moment.

## Rejected

- **A validity on the gift card product.** It would copy a term through the
  catalog, the order and the payment module for what one setting and a moment at
  issue already say.
- **Refusing an expired card and keeping its balance.** The books would owe a
  debt nobody can claim; the user chose the close.
- **Closing a card inside the payment that finds it expired.** A payment would
  write a void as a side effect of being refused.
