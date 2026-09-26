# A swap outside the books — measured 2026-09-27

The evidence behind [ADR 0203](../adr/0203-an-exchanges-difference-is-on-the-books.md).

## 1. What each journal read of an exchange before

| Movement | Payment journal (ADR 0186) | Order journal (ADR 0188, 0189) |
|---|---|---|
| the difference captured on its collection | capture, receivable credited | nothing |
| the exchange funded | — | nothing |
| the difference sent back (`RefundExchangeDifference`) | refund, receivable debited; the row names the exchange (ADR 0187) | nothing: `JournalCauses` knew returns and claims |

So over the two journals an order with a funded exchange owed minus its
difference, and one whose exchange was funded and sent back owed nothing only
because the two payment movements cancelled out while its books never saw the
sale they paid for.

`WithdrawFundedOrderExchange`, which the refund ends with, keeps `funded_at`:
its statement sets only the status, `canceled_at` and `updated_at`, and its
comment says why. An entry read from `funded_at` therefore stays where it was
after the money goes back.

## 2. The end-to-end path

`TestTheBooksCloseForAnExchange` (`internal/e2e/books_test.go`): an order
checked out and captured, an exchange of 7,800 opened, collected on a
collection opened for the order, funded, then sent back. Receivable is summed
over the order's entries and over the payment journal's entries for both
collections.

| Step | Order entries | Receivable | Sales |
|---|---|---|---|
| funded | `order_placed`, `exchange_funded` | 0 | subtotal + 7,800 |
| sent back | + `exchange_refunded` | 0 | subtotal |

`TestAnExchangesDifferenceIsOnTheRealBooks` (the order module, real
PostgreSQL) reads the funded entry through the new window query and the
exchange through `JournalCauses`.

## 3. Mutations

| # | Mutation | Killed by |
|---|---|---|
| X1 | funded exchanges not read | integration, e2e |
| X2 | an exchange not a cause (`WHERE false` on its arm) | integration, e2e |
| X3 | a refund naming an exchange booked as a return's | unit, e2e |
| X4 | the difference credited to shipping | unit, integration, e2e |
| X5 | the refund debited to sales_returns | unit, e2e |

X2's first run broke the SQL rather than the rule and was run again with a
valid statement; both were killed.
