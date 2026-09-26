# Money for a faster courier — measured 2026-09-26

The evidence behind [ADR 0200](../adr/0200-a-dearer-delivery-is-paid-before-it-changes.md)
and D142.

## 1. What an exchange's funding checked

`returns.FundExchangeDifference` (ADR 0120) read the named collection and held
it to three things; the order module then held the exchange's row.

| Checked | Where |
|---|---|
| the collection was opened for exactly the difference | the flow |
| it is in the order's currency | the flow |
| captured less refunded equals the difference | the flow |
| the exchange is `requested` and owes a positive difference | the order module, under the exchange's lock |
| no other exchange names the collection | `order_exchanges_payment_collection_uniq` |

Not checked: whose money it was. `payment_collections.reference` is documented
in the payment module's first migration as "the identifier of the caller's OWN
record (a cart or an order)", and the checkout writes the cart's id there. The
exchange's end-to-end fixture opened its collections with the reference
`"exchange difference"`, and they funded. A collection opened for another
order, or the checkout's own for an exchange of the order's whole total, passed
the same three checks (D142).

## 2. Who reads a collection's money

| Reader | How it finds the collection | Sees an upgrade's collection? |
|---|---|---|
| the order's summary (ADR 0121) | backwards over `order_payment` | no |
| the order's timeline, payment view | forwards over `order_payment` | no |
| the payment journal (ADR 0186) | every capture and refund in the window | yes: the capture credits receivable |
| the order journal (ADR 0188) | the order's own rows | the `delivery_upgraded` entry debits receivable |

An exchange's capture reaches the payment journal and has no debit on the
order's side; an upgrade's has one, so receivable closes over the pair.

## 3. The locks

No flow of the order module took an exchange's lock before the order's: an
exchange is opened under the order's lock, and its transitions take the
exchange's alone. The funding now takes the order's first.

`TestAnExchangeWaitsForAChangeTakingItsCollection` (real PostgreSQL): a
delivery change holds the order's lock with a collection it found free; the
exchange's funding naming the same collection is seen WAITING in
`pg_stat_activity`, and once the change commits it is refused with
`order_payment_collection_taken`. With the funding taking the exchange's lock
alone (P6 below) it reads the collection free and funds itself.

`TestTheDeliveryChangeConstraintsAreTheLastDefence`, past the service:

| Row | Refused by |
|---|---|
| a positive difference and no collection | `order_delivery_changes_paid_when_dearer` |
| no difference and a collection | `order_delivery_changes_paid_when_dearer` |
| a second change on the same collection | `order_delivery_changes_payment_collection_uniq` |

`TestARollbackRefusesADatabaseHoldingAPaidDelivery`: rolling back to 000025
fails on `order_delivery_changes_costs_no_more` rather than dropping the
collection a change named.

## 4. The end-to-end path

`internal/e2e/delivery_change_test.go`, on an order sold a delivery of 3,000,
moving to an admin-only option of 4,500:

| Step | Observed |
|---|---|
| no collection | 409 `order_delivery_costs_more`, `details.difference` 1,500 |
| a collection of 1,500 opened for another record | 409 `fulfilling_collection_not_the_orders` |
| a collection of 1,400 opened for the order | 409 `order_delivery_payment_mismatch` |
| a collection of 1,500 opened for the order and captured | 200 |
| the method's change | the collection named, a difference of 1,500 |
| the order journal | `delivery_upgraded`, 1,500 debited to receivable |
| the payment journal, that collection | 1,500 credited to receivable |
| a parcel opened naming no option | on the 4,500 option |

`internal/e2e/exchange_funding_test.go` opens its collections for the order
now, and one opened for `"exchange difference"` is refused with 409.

## 5. Mutations

| # | Mutation | Killed by |
|---|---|---|
| P1 | what the collection holds not held to the difference | the order's unit test, e2e |
| P2 | a change skipping the taken check | unit, integration (the unique index) |
| P3 | a collection accepted on a change that costs no more | unit |
| P4 | the collection not recorded on the change | unit, integration, e2e (the CHECK) |
| P5 | an exchange skipping the taken check | unit, integration |
| P6 | an exchange's funding not locking the order | integration |
| P7 | a repeated change accepting another collection | unit |
| P8 | the collection's reference not checked | the flow's unit test, e2e |
| P9 | its currency not checked | the flow's unit test |
| P10 | what it holds not checked against what it was opened for | the flow's unit test |
| P11 | what it holds not handed to the order module | the flow's unit test, e2e |
| P12 | an exchange's collection's reference not checked | the returns flow's unit test, e2e |
| P13 | the journal not reading dearer changes | integration, e2e |
| P14 | a dearer change booked the other way | unit, integration, e2e |
| P15 | the collection's index not unique | integration |
| P16 | the rollback not putting 000025's rule back | integration |
| P17 | the refusal without its details | unit, e2e |
| P18 | the details naming the amount as the difference | unit, e2e |
| P19 | the endpoint dropping the collection | the API's unit test, e2e |
| P20 | the repository reading changes and not exchanges | integration |
| P21 | the reference answering the collection's id | the payment unit test, e2e |

Every mutation was killed on the first run.
