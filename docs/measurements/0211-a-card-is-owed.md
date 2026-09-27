# A card is owed — measured 2026-09-27

The evidence behind [ADR 0211](../adr/0211-an-order-books-a-sold-gift-card-as-a-debt.md).

## 1. Where the flag was

| Reader | What it did with `is_giftcard` |
|---|---|
| the cart's totals | read it with the products' other facts for promotion and tax rules; a failed read prices the cart without it, and no read is made when neither a promotion nor a tax module is wired |
| the checkout | read the variants' title and stock flags in one query; nothing of the product |
| the order line | had no such column |
| the gift card sale flow (ADR 0210) | walked each paid order's lines to their variants, then to their products, at capture |
| the order journal (ADR 0188) | credited an order's whole subtotal to `sales` |
| the payment journal | debits `gift_card` when a card is spent, and books a sold card's issue nowhere (ADR 0210) |

A card sold for 5,000 and spent for 2,000 therefore stood on the two journals
as 5,000 of sales and a `gift_card` debit of 2,000 that nothing had credited.

## 2. On the production wiring

`TestABoughtGiftCardIsMailedAndSpent` now also reads the order and both
journals. The line of the card's order carries `is_giftcard` true. Its placement
has no `sales` line and credits `gift_card` 5,000; the capture that spent the
card debits it by the second order's total, and what the two journals leave on
`gift_card` equals the card's balance read from the payment module.

`TestAnOrderWithoutAGiftCardIssuesNone` still issues nothing: the line's flag is
false, and the flow no longer reads the catalog.

## 3. On the real schema

`TestASoldGiftCardIsADebtOnTheRealBooks` places an order of a 3,000 line and a
gift card line of two cards at 2,500, and another one it cancels. The order read
back names the card line, and only it, as a card. The placement credits `sales`
3,000 and `gift_card` 5,000; the cancellation debits `gift_card` 5,000.

The rollback of 000028 drops the column; `TestMigrationIsReversible` rolls every
migration back and forth.

## 4. In the checkout

`TestTheStockFlagsRideOnTheTitleQuery` now counts two catalog reads: the
variants, with `product_id` added to the fields, and every product of the cart
in one batch. `TestAGiftCardLineIsPlacedAsOne` reads the snapshot sent to the
order as raw JSON: `is_giftcard` true on the card's line, false on the other. A
product missing from the answer is a not-found `checkout_workflow_variant_unknown`, a
failed product read keeps its kind under `checkout_workflow_catalog_read_failed`,
and a flag that is not a bool is refused; none of the three places an order.

## 5. Mutations

| # | Mutation | Killed by |
|---|---|---|
| U1 | the order's snapshot without the flag | the checkout's test, the end-to-end test |
| U2 | the plan without the flag | the checkout's test, the end-to-end test |
| U3 | a product the catalog lacks read as no card | the checkout's test |
| U4 | a flag that is not a bool read as false | the checkout's test |
| U5 | a failed product read taken for a missing product | the checkout's test |
| U6 | the variants read without their product | the checkout's read test, the end-to-end tests |
| U7 | the order's interop dropping the flag | the end-to-end test |
| U8 | the order service dropping the flag | the module's integration test, the end-to-end test |
| U9 | the line's insert dropping the flag | the module's integration test, the end-to-end test |
| U10 | the line's read dropping the flag | the module's integration test, the end-to-end test |
| U11 | the placement summing every line as a card | the module's integration test |
| U12 | the cancellation ignoring the cards | the module's integration test |
| U13 | the whole subtotal credited to sales | the journal's test, the module's integration test, the end-to-end test |
| U14 | card lines beyond the subtotal booked | the journal's test |
| U15 | every line taken for a card | the flow's tests, the end-to-end test |
| U16 | a line flag that is not a bool read as false | the flow's test |
| U17 | the flow not asking for the flag | the flow's tests, the end-to-end test |
| U18 | the read layer's line without the flag | the provider's test, the end-to-end test |
| U19 | the order views without the flag | the API's test |
| U20 | the two journals spelling the account apart | the account gate |

U11 left the end-to-end test green: its card's order has one line, so the sum of
every line is the sum of the card lines. The module's integration test places a
card beside another line and kills it.

U15 first failed to compile, which is not a kill, and was written again. U17
first left the flow's tests green, because their fake answered every field
whatever was asked; it now answers the fields asked for, as the read layer does,
and the tests kill it.
