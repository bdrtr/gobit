# Why the money went back — measured 2026-09-25

The evidence behind [ADR 0187](../adr/0187-a-refund-names-its-cause.md).

## 1. Where refunds are made

| Entry | Cause | What the order side recorded |
|---|---|---|
| `returns.RefundReturn` | a return | nothing on the return; the order summary's totals |
| `returns.SettleClaim` | a claim | the claim completed; the summary's totals |
| `returns.RefundExchangeDifference` | an exchange withdrawn | the exchange withdrawn |
| `POST /admin/v1/payments/{id}/refunds` | none | the summary's totals, through the `payment.refunded` subscriber |

Order cancellation refunds nothing (`CancelOrder` refuses a paid order), a
line cancellation moves no money, a credit line moves no money, and the
checkout saga does not refund a capture it compensates.

No order migration had a refund id column. The three workflow entries reach
the payment module through `Interop.RefundCollection`, which received the
refund rows from the service and returned only their total.

## 2. One transaction or two

`Service.RefundCollection` refunds one capture per transaction
(`RefundPayment`), with no outer transaction, because each refund calls a
provider. A cause written on the order's records afterwards would be one more
transaction per refund, in another module, after the money moved. The
reference column is written by the same `CreateRefund` that writes the row.

## 3. Tests

| Test | Holds |
|---|---|
| `TestEveryRefundOfASplitCarriesTheCause` | a refund over two captures writes the cause on both rows |
| `TestTheMovementsCarryTheCause` | a refund movement carries it, a capture's is empty |
| `TestAnOperatorsRefundNamesNothing` | the admin refund's reference is empty |
| `TestAReferenceIsAnIDNotProse` | a padded reference is refused before money moves |
| `TestARefundKeepsItsCauseInTheRealSchema` | the column round-trips through the repository and the movements query, on PostgreSQL |
| the returns workflow's refund, claim and exchange tests | each names its own id as the reference |

## 4. Mutations

| Mutation | Result |
|---|---|
| the refund row written without the reference | red: two service tests |
| `RefundCollection` passing "" to each refund | red: two service tests |
| a padded reference accepted | red: `TestAReferenceIsAnIDNotProse` |
| the repository not writing the column | red: the integration test |
| the movements query not reading it | red: the integration test |
| the return, the claim, the exchange each passing "" | red: the matching workflow test, each |
