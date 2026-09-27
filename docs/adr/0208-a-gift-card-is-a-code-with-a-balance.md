# ADR 0208 — A gift card is a code with a balance

**Summary:** An operator issues a gift card, a code holding a balance in one
currency, and whoever presents the code pays with it at checkout through a
`gift_card` provider. The code is shown once and kept as its digest.

- **Status:** Accepted
- **Date:** 2026-09-27
- **Amended by:** [0209](0209-a-gift-card-pays-first-and-a-provider-the-rest.md): a card holds what it has rather than declining, and a provider pays the rest
- **Amended by:** [0213](0213-an-operator-closes-a-gift-card-and-a-card-line-is-final.md): an operator can close a card, which then pays nothing and takes no refund

Measurement: [measurements/0208](../measurements/0208-a-code-with-a-balance.md)

## Context

A product can be flagged `is_giftcard` and nothing reads the flag. The user
chose a card that is a code with a balance, rather than store credit for a named
customer, and chose to build spending before selling. The module already spends
two balances, store credit and points, on one state machine. Both take their
owner from the collection's customer, because a payment's data is the client's.
The checkout's early refusal (ADR 0175) knew only the provider and the customer.

## Decision

A gift card is a row holding one currency and the SHA-256 of an 80-bit code,
with a ledger of signed rows, issued through `POST /admin/v1/gift-cards` and
spent by a `gift_card` provider that finds the card from the code in the
payment's data. The balance tenders' machine takes an owner resolver, and the
checkout's early refusal hands the provider the payment's currency and data, so
an unknown code or a card in another currency opens no order.

## Consequences

The issue's answer carries the code once, as `XXXX-XXXX-XXXX-XXXX`; no endpoint
shows it again, and a lost code is a lost card. The shop keeps the digest and the
last four characters. Case, dashes and spaces do not matter, and O, I and L are
read as 0, 1 and 1.

The code is the credential: a guest pays with it, and the provider is registered
in every installation, unlike store credit's. A malformed code and an unissued
one get the same answer, 422 `payment_gift_card_unknown`; a card in another
currency gets 409 `payment_gift_card_currency`. The code is written to no payment
session and no workflow record. There is no storefront endpoint that reads a
card's balance.

A card is spent as store credit is: the authorization holds, the capture writes
no row, a cancellation releases, and a refund goes back onto the card. Its lock
is the card's own row, which exists from the issue on, so D118 cannot arise.

The journal gains `gift_card`, owed to the cards' holders, and
`gift_card_granted`, what the cards the shop issued cost it. A capture through a
card debits `gift_card`. Money captured through a card earns loyalty points, as
store credit's does; selling cards will have to decide whether a purchase or its
spending earns them.

In this record a card has to cover the whole order; paying the rest with
another provider is the next record. A card has no expiry, cannot be disabled,
and is not reissued. Rolling back migration 000009 stops while a card exists.

The machine's session and ledger entry name an owner rather than a customer: the
customer for store credit and points, the card for a gift card.

Building the ledger found two payment gates counting the ledgers by hand
(D145); both now derive them.

## Rejected

- **Store credit for a named recipient.** It needs a customer account, and a
  guest could not spend it.
- **A slow hash of the code.** Eighty random bits cannot be walked back from
  SHA-256; a slow hash would cost every payment and buy nothing.
- **The code kept encrypted, to show it again.** Whoever holds the key could read
  every card, and the one moment the code needs to exist is its issue.
- **A storefront balance lookup.** It would answer every code a guesser tries;
  in this record only the operator reads a card's balance.
- **A machine of the card's own.** Its four verbs would be the store credit's;
  only the owner differs.
