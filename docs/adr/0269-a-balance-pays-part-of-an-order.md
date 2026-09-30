# ADR 0269 — A balance pays part of an order

**Summary:** A storefront completion names the customer's store credit and
points in `pay_first_with`; after the gift card, each holds what it has of what
is still unpaid, and the provider pays the rest. A balance paying alone still
declines a shortfall.

- **Status:** Accepted
- **Date:** 2026-09-30
- **Amends:** [0209](0209-a-gift-card-pays-first-and-a-provider-the-rest.md), which rejected a list of tenders because the card was the only one that held part of an order

## Context

The known limits said a person-bound tender paid the whole order or none of
it: the checkout opened one session for the whole remaining amount and failed
the step when the hold fell short, so "part in points, the rest by card" was a
checkout decision no record had made. ADR 0209 let a gift card pay first,
because a card holds what it has on every session. Store credit and points
decline a session their balance does not cover, and a test holds that decline
to a refusal that writes nothing to the ledger.

## Decision

The completion takes an optional `pay_first_with` naming `store_credit`,
`loyalty_points` or both, whose sessions open after the gift card's and before
the provider's, each holding what its balance has of what is still unpaid,
and the provider pays the rest or is not asked. A balance holds part of a
session only when its opener asks with `partial: true` in the payment's data,
which the checkout does for a balance that pays first, so a balance paying
alone still declines a shortfall.

## Consequences

- A customer pays part in points, part in credit and the rest by card at the
  storefront. A balance of a cart that names nobody is refused before the
  order is opened, as a card is (409 `payment_*_no_customer`); a balance named
  twice, another provider, or the provider that pays the rest is 422.
- A balance that holds nothing stops the payment with its decline and releases
  what the tenders before it held, as an empty card does.
- The provider's hold is captured first, then the card's and the balances' in
  the order they held; a capture failing after money moved stops the execution
  for a person. A refund draws the newest capture first (ADR 0118), so the
  last balance named is the first one refunded.
- The authorization's record names every hold in `first_holds`, and a record
  written before names its card in the older fields and is read the same way;
  the capture's record lists the other captures in `other_payment_ids`.
- Migration 000014 of the payment module adds `partial` to the two balance
  session tables. An operator opening a session on the admin surface can ask
  for a partial hold the same way; a `partial` that is not a boolean is 422.
- The order is fixed: the card first, then the balances as named, and one card
  per order.

## Rejected

- Holding part on every session of a balance, as the card does: a balance paying
  alone would answer a shortfall with the checkout's
  `payment_underauthorized` after writing and releasing a hold, where it
  declines and writes nothing.
- Sizing the balance's session from a balance the checkout reads: ADR 0209's
  reason, the balance can move between the read and the hold.
- Letting any provider pay first: one that needs the client's payment data has
  none to open with.
- Passing over a balance that holds nothing: a payment the customer did not
  choose.
