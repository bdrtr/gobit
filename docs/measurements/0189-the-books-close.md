# The books close — measured 2026-09-26

The evidence behind [ADR 0189](../adr/0189-a-refunds-cause-gives-back-revenue.md).

## 1. The closure, on the production wiring

`TestTheBooksCloseForAReturnedOrder` in `internal/e2e` places an order through
the checkout with the manual provider, so the collection is captured. A
storefront return is opened for one of its units, received at the stock
location and refunded in part through `POST .../returns/{id}/refund`. Both
journals are then read over the window and narrowed to that order and its
collection:

| Journal | Entries for the order |
|---|---|
| order (ADR 0188, 0189) | `order_placed`, `return_refunded` |
| payment (ADR 0186) | `capture`, `refund` |

Summed over the two, the order's receivable is 0. The `sales_returns` debit is
the refunded amount, and the `sales` credit is the order's subtotal.

## 2. What is left open

| Refund | Entry on the order's books |
|---|---|
| names one of the order's returns | `return_refunded`: Dr `sales_returns`, Cr `receivable` |
| names one of the order's claims | `claim_refunded`: Dr `claim_allowances`, Cr `receivable` |
| names an exchange | none |
| names nothing, an operator's | none |

`TestARefundIsBookedAgainstItsCause` scripts one refund of each of the first
three rows; the exchange's refund makes no entry.

## 3. Mutations

| Mutation | Result |
|---|---|
| the order module's wiring leaves the refunds unread | red: the e2e test |
| the return names no cause on its refund | red: the e2e test |
| the order module reading the reference under another key | red: the e2e test |
| a refund in another currency booked | red: `TestARefundInAnotherCurrencyThanItsOrderIsAnError` |

The third row is the contract the compiler cannot see: the payment interop
writes `reference`, and the order's port decodes it by that name.
