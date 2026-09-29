# ADR 0247 — A gift card is not taxed when it is sold

**Summary:** A gift card line carries no tax; the goods the card buys are taxed
when it is spent, so the card's value is taxed once.

- **Status:** Accepted
- **Date:** 2026-09-30
- **Amends:** [0211](0211-an-order-books-a-sold-gift-card-as-a-debt.md), which left a card line taxed as the tax rules say

Measurement: [measurements/0247](../measurements/0247-a-card-taxed-twice.md)

## Context

ADR 0211 left a gift card line "taxed as the tax rules say; nothing exempts it".
A 5,000 card in a market taxed at 20% cost 6,000, and the goods the card buys
are taxed again when it pays for them: the card's value was taxed twice (D170).
Where the prices include their tax (ADR 0246), the card is issued at its sticker
while the order's journal owes the sticker less its tax. A merchant could rate
cards at zero in the tax rules, and nothing said so.

## Decision

A gift card line carries no tax: the cart neither sends it to the tax module nor
applies the region's rate to it, and the checkout and the order refuse a card
line that carries tax. The goods a card buys are taxed when it is spent.

## Consequences

- A card costs its value, and the order's journal owes what the card holds, in
  either kind of market.
- With neither the promotion nor the tax module installed, the round reads the
  products when the region's rate taxes, to know which lines are cards; a zero
  rate reads nothing (ADR 0009).
- A round that cannot read the products taxes every line, as it prices every
  line without its facts. The checkout reads the flag strictly and refuses a
  taxed card with `checkout_workflow_gift_card_taxed`, before anything is
  reserved or charged, and the next attempt computes the totals again.
- An order placed before keeps its taxed card line, and its books read it as
  recorded.
- A jurisdiction that taxes a card when it is sold has no switch here; nothing
  has asked for one.

## Rejected

- **Leaving it to a zero rate in the tax rules.** The default then taxes money
  twice, and a merchant who never wrote the rule finds out at reconciliation.
- **Taxing the card and not the goods it buys.** The goods' order is priced
  before its payment is chosen, and its invoice has to carry the tax.
- **A setting to tax cards when sold.** A capability with no consumer.
