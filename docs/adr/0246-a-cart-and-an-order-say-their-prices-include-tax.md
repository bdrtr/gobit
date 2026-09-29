# ADR 0246 — A cart and an order say their prices include their tax

**Summary:** The cart and the order record whether their prices included their
tax, and every check of a line's subtotal reads it, so such a market sells.

- **Status:** Accepted
- **Date:** 2026-09-30

Measurement: [measurements/0246](../measurements/0246-the-market-that-could-not-sell.md)

## Context

[ADR 0086](0086-a-price-can-include-its-tax.md) lets a market quote its prices
with their tax inside, and makes a line's subtotal what is left of the sticker
once the tax is taken out, so the totals identity lands on the sticker. The cart
module, the checkout's plan and the order module each held a line's subtotal to
its unit price times its quantity, and nothing told them the prices included the
tax. The cart refused to write such a cart's totals, so nothing could be sold in
the market (D169). ADR 0086 asserted the sticker at the computed total, on lines
with no unit price, and no test took an inclusive cart into the cart module.

Two readers of an order took its subtotal for the goods the shopper was quoted:
the promotion trial, whose request the promotion module refuses when an amount
is not its unit price times its quantity, and the facts a new delivery is
quoted on.

## Decision

The cart and the order record whether the prices of their totals included their
tax, written with the totals, and every check of a line's subtotal reads it: the
subtotal is unit price times quantity, or that less the line's tax. A reader
that wants the goods as the shopper was quoted reads unit price times quantity.

## Consequences

- A tax-inclusive market sells: the sticker is what the cart shows, the order
  charges and the payment collects, asserted from the cart to the collection.
- The flag chooses which identity holds and relaxes neither: with it, a line
  whose tax was added on top of its sticker is refused; without it, a line
  short by its tax is refused.
- The cart and the order answer `prices_include_tax`; a storefront showing a
  unit price beside a subtotal needs it. Both columns default to false, which
  is true of every row before them, since no inclusive cart could be written.
- The promotion trial and a new delivery's facts read the goods as unit price
  times quantity, the pre-tax figure in either kind of market, and need no flag.
- Two readers stay wrong in an inclusive market and are listed in
  `docs/known-limits.md`: an invoice prints the sticker as the unit price beside
  the net subtotal, and a gift card line the tax rules tax is issued at its
  sticker while the journal owes its net.

## Rejected

- **Accepting either identity without a flag.** It passes an exclusive line
  short by exactly its tax, and a reader still cannot tell the two apart.
- **Keeping the subtotal gross and changing the identity.** Every total check,
  CHECK constraint and journal entry reads Total = Subtotal - Discount + Tax,
  and the flag would have to enter each of them.
- **The flag on each line.** A cart has one market, and the tax module answers
  it once per request.
- **Rewriting the unit price to the net.** A net unit price is rarely a whole
  minor unit.
