# A card that was bought — measured 2026-09-27

The evidence behind [ADR 0210](../adr/0210-a-sold-gift-card-is-issued-when-its-order-is-paid.md).

## 1. What a sale could stand on

| Part | State |
|---|---|
| `is_giftcard` | read by the cart's promotion rules only; no order, payment or flow code acted on it |
| an order's lines | the read layer's `order_line_item`: order, variant, quantity, unit price; filtered by `order_id`, paged by the order module's cap |
| a line's product | the variant entity's `product_id`, then the product's `is_giftcard` |
| an order's collection | the `order_payment` link, order to collection, read backwards |
| `payment.captured` | carries the collection id only; the amounts are read from the collection |
| a handler's error | logged; no backend delivers the event again (docs/extending.md) |
| the notification module | sends at once through its provider, records a (template, reference) pair once, keeps no data and does not retry; the default provider logs the data's keys, never its values |
| a gift card's code | shown once, kept as a digest (ADR 0208) |
| the earn base | a collection's captures less refunds, excluding the loyalty tender |

## 2. On the production wiring

`TestABoughtGiftCardIsMailedAndSpent`: a customer buys a 5,000 TRY gift card
product with the manual provider. The capture event reaches the flow, which
issues a card of 5,000 and mails `gift_card.issued` to the order's address with
the code, the amount, the currency and the order. The purchase earned points.
The customer spends the card on a second order: the card drops by that order's
total, and the points do not move. An operator replaces the code through the
admin API; the balance stays, and the old code is answered 422 at a storefront.

`TestAnOrderWithoutAGiftCardIssuesNone`: an ordinary order's capture reaches
the flow, which mails nothing for half a second after the order's confirmation
and issues no card.

## 3. On the real schema

`TestTwoDeliveriesOfOneSaleMakeOneCard`: two concurrent issues of one sale make
one card, one issue row and one code.

`TestASoldCardIsTheOrdersAndNotTheJournals`: the payment journal books an issued
card's issue and not a sold one's.

`TestAReplacedCodeStopsTheOldOne`: after a replacement the gift card provider
answers `payment_gift_card_unknown` to the old code and accepts the new one.

`TestTheSaleConstraintsAreTheLastDefence`: a sold card without its sale, an
issued card naming one, a source outside the vocabulary and a sale named twice
are each refused by their constraint.

## 4. The rollback tests counted steps

`db.MigrateDown` rolls back a number of STEPS, not down to a version. ADR 0208's
rollback test passed 8, read as "to version 8"; it held while 000009 was the
first step back, and with 000010 in the tree it rolled 000010 back first. The
first draft of this record's test passed 9 and survived a mutation that removed
000010's refusal: 000010 went back cleanly and 000009 then stopped, which the
test took for the refusal it meant. Both now roll back one step at a time and
name the constraint that refused.

## 5. Mutations

| # | Mutation | Killed by |
|---|---|---|
| T1 | a sale issued twice | the concurrency test |
| T2 | a partly captured collection issuing | the flow's test |
| T3 | a found card mailed again | the flow's test |
| T4 | the card worth the whole line | the flow's test |
| T5 | every line taken for a gift card | the flow's tests |
| T6 | the order's first page only | the flow's test |
| T7 | one card per line | the flow's tests |
| T8 | the flow wired on the ground and not in production | the new wiring gate (D146) |
| T9 | the flow not subscribed | the end-to-end test |
| T10 | a card's spending earning points | the service's test, the end-to-end test |
| T11 | a sold card booked as a grant | the journal test |
| T12 | a replaced code keeping the old digest | the replacement test |
| T13 | the replaced code not answered | the API's test |
| T14 | a sold card without its sale | the constraint test |
| T15 | 000010's rollback not stopping | the rollback test, once it rolled back one step |
| T16 | a sale named by nothing | the service's test |
| T17 | one mail per order rather than per card | the flow's test |

T2 and T5 first failed to compile and were written again. T8 and T15 survived
their first run; §4 and D146 are what they found.
