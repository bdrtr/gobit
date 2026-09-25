# What the buyer owes — measured 2026-09-25

The evidence behind [ADR 0188](../adr/0188-the-order-module-keeps-derived-books.md).

## 1. The records the order module keeps

| Fact | Where | Amount | Moment |
|---|---|---|---|
| order placed | `orders` | subtotal, discount, tax, shipping, total; CHECK `total = subtotal - discount_total + tax_total + shipping_total` | `placed_at` |
| order canceled | `orders` | the same row | `canceled_at` |
| credit line | `order_credit_lines` | `amount > 0` | `created_at` |
| line cancellation | `order_line_cancellations` | none, a quantity | `created_at` |
| return, claim | `order_returns`, `order_claims` | a planned `refund_amount`, never updated | received, completed |

`CancelOrder` refuses an order with money paid, so a cancellation reverses an
order nothing was captured for.

## 2. The real schema

`TestTheOrderJournalReadsTheRealRecords` places an order of 6,100 and writes
400 off it, and places another and cancels it. On the order's books the first
owes 5,700 and the second nothing, and every entry balances.

## 3. The window

A scratch PostgreSQL 16 with the module's migrations, 50,000 orders 21 minutes
apart, one in twenty canceled two hours after it was placed, and a credit line
on one in seven. One month's window, `EXPLAIN ANALYZE`:

| Read | Without the indexes | With them |
|---|---|---|
| orders placed (2,125) | Seq Scan, 5.5 ms | Bitmap Index Scan on `orders_placed_at_idx`, 2.0 ms |
| orders canceled (106) | Seq Scan, 3.6 ms | Index Scan on the partial `orders_canceled_at_idx`, 0.06 ms |
| credit lines (304) | Seq Scans, 5.9 ms | Bitmap Index Scan on `order_credit_lines_created_at_idx`, 6.8 ms |

The credit lines read is its join: the planner hash-joins the window's rows
against a sequential scan of orders, with or without the index, and at 7,142
credit lines the scan of that table was never the cost. The index takes the
window off a scan that grows with every credit line written.

## 4. A test that asserted nothing (D136)

`TestAnOrderThatDoesNotAddUpIsAnError` asserted
`errors.HasKind(err, errors.KindInternal)`, and the mutation that removed the
identity check survived it. `KindOf(nil)` was `KindInternal`, so the assertion
held with no error at all. The test now requires the error first, and
`HasKind(nil, kind)` is false in `core/errors`.

## 5. Mutations

| Mutation | Result |
|---|---|
| a cancellation not reversed | red: `TestTheOrderChartOfAccounts` |
| the discount line dropped | red: the chart and the balance tests |
| a credit line's sides swapped | red: `TestTheOrderChartOfAccounts` |
| zero lines written | red: the balance test and `TestAFreeOrderIsNotAnEntry` |
| the identity not checked | survived, until the test required the error (D136) |
| a cancellation ordered before its placement | red: `TestAPlacementComesBeforeItsCancellation` |
| a free order kept as an empty entry | red: `TestAFreeOrderIsNotAnEntry` |
