# The market that could not sell — measured 2026-09-30

The evidence behind [ADR 0246](../adr/0246-a-cart-and-an-order-say-their-prices-include-tax.md)
and D169.

## 1. The reproduction

A region bound to one country, whose tax region says `prices_include_tax` and
carries a 20% default rate, and one line of a variant priced 12_000. The line
was added and the totals round failed:

```
cart_workflow_totals_after_change_failed: line added (cart_…) but the totals
could not be calculated; the cart's totals are stale, the calculation has to be
run again: cart_totals_inconsistent: the line subtotal is inconsistent (li_…):
subtotal=10000 given, unit_price(12000) x quantity(1) = 12000
```

Every round of such a cart fails the same way, so its totals stay stale and the
checkout, which computes them again, refuses it before a plan exists.

## 2. The chain

| Place | What it held |
|---|---|
| `internal/workflows/cart/tax.go`, `applyTaxResponse` | rewrites the line's subtotal to the extracted base plus the discount (ADR 0086) |
| `internal/modules/cart/service/totals.go`, `validateLineTotals` | subtotal = unit price x quantity: the first refusal |
| `internal/workflows/checkout/plan.go`, `validate` | the same, never reached |
| `internal/modules/order/service/totals.go`, `validateOrderItem` | the same, never reached |
| database CHECK constraints | none involves `unit_price`; they hold total = subtotal - discount + tax (+ shipping), which the rewrite keeps |

ADR 0086's tests call `applyTaxResponse` and `assembleTotals` on lines whose
unit price is zero; none of them hands the result to the cart module.

## 3. The readers of an order's subtotal

| Reader | In an inclusive market | Now |
|---|---|---|
| `internal/workflows/cart/trial.go` | sends the net subtotal as the amount beside the sticker as the unit amount; the promotion module refuses an amount that is not unit amount x quantity | reads unit price x quantity |
| `internal/modules/order/service/interop.go`, `DeliveryFactsJSON` | the goods after discount were the net, where the cart's quote reads the stickers less the discount | reads unit price x quantity less the discount |
| `internal/workflows/invoicing/issue.go`, `lines` | copies the sticker as the unit price beside the net subtotal | a known limit |
| `internal/workflows/giftcardsale/giftcardsale.go` and `internal/modules/order/queries/journal.sql` | the card is issued at its unit price; the journal's gift card debt is the lines' subtotal | a known limit |
| returns, claims, cancellation, the as-of read, the journal's other lines | quantities, caller-given amounts, or the totals identity | unaffected |

## 4. The end-to-end figures

The sticker 11_999, twice, at 20% inside: 23_998 holds 3_999 of tax, since
23_998 x 2000 / 12_000 is 3_999.67 rounded down, and 19_999 is left. The cart's
total, the order's total and the payment collection's amount are 23_998; the
order line keeps 11_999 as its unit price, 19_999 as its subtotal and 3_999 as
its tax.

## 5. The tests

| Test | Holds |
|---|---|
| `TestATaxInclusiveStickerIsWhatTheOrderCharges` (end to end) | the figures above, from the cart through the order to the collection, with both flags stored |
| `TestSetTotalsHoldsAnInclusiveLineToItsStickerLessItsTax` | the cart refuses the inclusive figures without the flag and a tax counted on top with it, and stores the flag |
| `TestAnInclusivePlanIsHeldToItsStickerLessItsTax` | the plan, the same three cases |
| `TestAnInclusiveLineIsHeldToItsStickerLessItsTax` | the order, the same three cases |
| `TestPlaceOrderJSONReadsWhetherThePricesIncludeTax` | the wire name reaches the order's write |
| `TestATaxInclusiveRoundTellsTheCartItsPricesIncludeTax` | the round carries the flag into the body written to the cart |
| `TestATrialPricesAnInclusiveOrderAtItsStickers` | the trial asks about the sticker and bounds the discount by it |
| `TestTheDeliveryFactsReadTheGoodsAsTheCartQuotedThem` | 3300, not 2750 |
| `TestACartSaysWhetherItsPricesIncludeTax`, `TestAnOrderSaysWhetherItsPricesIncludeTax` | the responses carry the flag |

## 6. Mutations

| # | Mutation | Killed by |
|---|---|---|
| C1 | the cart's inclusive branch never taken | `TestSetTotalsHoldsAnInclusiveLineToItsStickerLessItsTax` |
| C2 | the cart's inclusive check relaxed to a ceiling | the same, a tax counted on top |
| C3 | the cart checks its lines without the flag | the same |
| C4 | the cart does not store the flag | the same |
| C5 | the cart's interop drops the flag | `TestATaxInclusiveStickerIsWhatTheOrderCharges` |
| C6 | the cart repository does not write the flag | the same |
| C7 | the cart repository does not read the flag | the same |
| C8 | the cart response drops the flag | `TestACartSaysWhetherItsPricesIncludeTax` |
| W1 | the round does not carry the flag | `TestATaxInclusiveRoundTellsTheCartItsPricesIncludeTax` |
| W2 | the tax step reports every market exclusive | the same |
| W3 | the trial reads the order line's subtotal | `TestATrialPricesAnInclusiveOrderAtItsStickers` |
| W4 | the trial does not sum an order's goods | the same, and `TestATrialAddsWhatThePromotionWouldHaveAddedAndNoMore` |
| K1 | the plan does not take the flag | `TestATaxInclusiveStickerIsWhatTheOrderCharges` |
| K2 | the plan's inclusive branch never taken | `TestAnInclusivePlanIsHeldToItsStickerLessItsTax` |
| K3 | the plan's inclusive check always passes | the same |
| K4 | the order snapshot drops the flag | `TestATaxInclusiveStickerIsWhatTheOrderCharges` |
| O1 | the order's inclusive branch never taken | `TestAnInclusiveLineIsHeldToItsStickerLessItsTax` |
| O2 | the order's inclusive check relaxed to a ceiling | the same |
| O3 | the order checks its lines without the flag | the same |
| O4 | the order's interop drops the flag | `TestPlaceOrderJSONReadsWhetherThePricesIncludeTax` |
| O5 | the order's write drops the flag | `TestAnInclusiveLineIsHeldToItsStickerLessItsTax` |
| O6 | the order repository does not write the flag | `TestATaxInclusiveStickerIsWhatTheOrderCharges` |
| O7 | the order repository does not read the flag | the same |
| O8 | the order response drops the flag | `TestAnOrderSaysWhetherItsPricesIncludeTax` |
| O9 | the delivery facts read the order's subtotal | `TestTheDeliveryFactsReadTheGoodsAsTheCartQuotedThem` |

Twenty-five mutants, all killed. C8 and O8 were listed before any test held
them, and the two response tests were written for them before the run.
