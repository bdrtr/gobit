# A card taxed twice — measured 2026-09-30

The evidence behind [ADR 0247](../adr/0247-a-gift-card-is-not-taxed-when-it-is-sold.md)
and D170.

## 1. The reproduction

On the production wiring, a 5,000 TRY gift card in the e2e market taxed at 20%,
before the change:

```
card cart: subtotal=5000 tax=1000 total=6000
```

`TestABoughtGiftCardIsMailedAndSpent` then spent the card on a 2,000 product,
and that cart was taxed at 20% as well. The card was issued at 5,000 (its unit
price, ADR 0210), and by the journal's rule (ADR 0211) the order credits
`gift_card` the line's subtotal, 5,000, and `tax_payable` its tax, 1,000.

Where the prices include their tax (ADR 0246) the same card line would hold
833 of tax inside its 5,000 (5,000 x 2,000 / 12,000, rounded down): the card is
issued at 5,000 and the journal's gift card debt, the line's subtotal, would be
4,167.

## 2. Where a card line met the tax

| Path | Before | Now |
|---|---|---|
| the tax module's (`applyModuleTax`) | every line sent | a card line is not sent; the answer is matched against the lines that were |
| the region's flat rate (`applyRegionTax`) | every line taxed | a card line skipped; with neither module installed the products are read once when the rate taxes, never at a zero rate |
| the checkout's plan | no rule | a card line with tax refused, `checkout_workflow_gift_card_taxed`, before any reservation |
| the order's line check | no rule | a card line with tax refused, `order_totals_inconsistent` |

The cart's product read is lenient (a failure prices without the facts), and
the checkout's is strict (ADR 0211), which is why the refusal sits in the plan:
a round that could not read the products cannot tell a card, and the plan can.

## 3. The tests

| Test | Holds |
|---|---|
| `TestABoughtGiftCardIsMailedAndSpent` (end to end) | the card's cart comes to 5,000 with no tax, and its order line carries none |
| `TestATaxModuleIsNotAskedAboutAGiftCard` | the card line is not sent, keeps no tax, and in an inclusive market keeps its sticker as its subtotal |
| `TestARegionRateLeavesAGiftCardUntaxed` | with neither module, the rate reads the products once and leaves the card untaxed |
| `TestTheFactsAreNotReadWithoutAConsumer` | with neither module and a zero rate, nothing is read |
| `TestATaxedGiftCardOpensNoOrder` | the plan refuses a taxed card before an order or a collection |
| `TestAGiftCardLineCarriesNoTax` | the order refuses a taxed card line and places an untaxed one |

## 4. Mutations

| # | Mutation | Killed by |
|---|---|---|
| G1 | card lines sent to the tax module | `TestATaxModuleIsNotAskedAboutAGiftCard` |
| G2 | no line is a card | the same, and `TestARegionRateLeavesAGiftCardUntaxed` |
| G3 | the region's rate taxes cards | `TestARegionRateLeavesAGiftCardUntaxed` |
| G4 | the region's rate never reads the products | the same |
| G5 | the region's rate reads the products at a zero rate | `TestTheFactsAreNotReadWithoutAConsumer` |
| G6 | the checkout takes a taxed card | `TestATaxedGiftCardOpensNoOrder` |
| G7 | the order takes a taxed card | `TestAGiftCardLineCarriesNoTax` |
| G8 | the tax module's answer not written back | every module-path totals test |
| G9 | the answer matched against every line | the same |

Nine mutants, all killed. G3's first spelling deleted the skip and left its map
unused, which failed to compile; it was rewritten to keep the map and never
skip, and a test failed on the rewrite.
