# A cheaper courier — measured 2026-09-26

The evidence behind [ADR 0199](../adr/0199-a-delivery-can-be-changed-before-it-ships.md).

## 1. What a quote reads

The cart's quote (`internal/workflows/cart/shipping.go`, `quoteRequestFor`) and
the order's delivery change (`fulfilling.ChangeDelivery` over the order's
`DeliveryFactsJSON`) ask the same listing, `fulfillment.interop`
`ListOptionsJSON`, which trusts the facts it is handed because only in-process
flows reach it.

| Fact | Cart's quote | Order's change |
|---|---|---|
| region, currency | the cart's | the order's |
| country | the region's one country, empty when it has several | the current shipping address's |
| subtotal | line subtotals less line discounts | `subtotal − discount_total` |
| item count | the cart's quantities | the lines' quantities as sold |
| weight | 0: the cart carries none | 0 |
| shipping profiles | not sent | not sent |
| admin-only options | not asked | asked |
| return options | refused if listed | refused if listed |

A line canceled after the sale (ADR 0113) still counts: the facts are the sale's.

## 2. How money moves against a placed order

| Path | Where | What it can do |
|---|---|---|
| credit line | `order_credit_lines`, ADR 0105 | lower what is owed, under the order's lock, up to the order's total |
| the sale's collection | `order_payment` link, one to one (ADR 0117) | the checkout's money only |
| an exchange's funding | `returns.FundExchangeDifference` | bind a collection captured elsewhere to an exchange; outside the order journal (ADR 0188) |

A cheaper change needs the first row and nothing else. A dearer one needs a
collection bound to the order for a reason other than the sale, and books that
the exchange's funding does not write; neither exists.

## 3. The books

Before this change every row of `order_credit_lines` was a `credit_line` entry
debiting `credit_allowances`. `JournalCreditLines` now joins the change that
names the credit line, through the partial unique index on
`order_delivery_changes.credit_line_id`; a credit a change wrote comes back as
a `delivery_changed` fact under the change's id, and the chart of accounts
debits `shipping` for it. Each credit line is still read once.

## 4. Two changes at once

`TestTwoChangesAreEachPricedAgainstTheOneBefore` (order module, real
PostgreSQL): the sold delivery costs 2,500. The first change, to 1,000, is held
after it has locked the order and written its credit; the second, to 400, is
seen WAITING on that lock in `pg_stat_activity`. Released, the second is priced
against 1,000: a difference of −600 and 2,100 credited in all. With the
change's own lock taken away (D7 below) the test fails: the second change reads
the method before the first commits, and the only lock left is the credit
line's, taken after that read.

`TestTheDeliveryChangeConstraintsAreTheLastDefence` writes past the service:

| Row | Refused by |
|---|---|
| a positive difference | `order_delivery_changes_costs_no_more` |
| a negative difference with no credit line | `order_delivery_changes_credit_when_cheaper` |
| no difference with a credit line | `order_delivery_changes_credit_when_cheaper` |
| a second change naming the same credit line | `order_delivery_changes_credit_line_uniq` |

## 5. The end-to-end path

`internal/e2e/delivery_change_test.go`, on an order checked out on a spy carrier
option of 3,000:

| Step | Observed |
|---|---|
| an option of 3,001 | 409 `order_delivery_costs_more` |
| an option the listing does not know | 409 `fulfilling_option_unavailable` |
| an admin-only option of 1,000 | 200 |
| the same option again | 200, still one change |
| the order's method | the sold option, and one change with −2,000 |
| its credit lines | `delivery_change`, naming the change |
| both timelines | `order.delivery_changed` |
| the order journal | `delivery_changed` under the change's id, 2,000 debited to `shipping` |
| a parcel opened naming no option | handed to the carrier on the 1,000 option |
| a change while that parcel is pending | 409 `fulfilling_parcel_underway` |

## 6. Mutations

| # | Mutation | Killed by |
|---|---|---|
| D1 | priced against the sold method, not its latest change | the order's unit test |
| D2 | a dearer change not refused | the order's unit test, e2e (the table's CHECK refuses the row instead) |
| D3 | no credit line for a cheaper change | unit, integration, e2e |
| D4 | the credit off by one | unit, e2e |
| D5 | the same option written again | unit, e2e |
| D6 | the order's status not held | unit |
| D7 | the order not locked by the change | integration |
| D8 | the first change taken as current | unit |
| D9 | the change's credit booked as a credit line | integration, e2e |
| D10 | the change debiting credit_allowances | unit, integration, e2e |
| D11 | the parcels not checked | the flow's unit test, e2e |
| D12 | admin-only options not asked for | the flow's unit test, e2e |
| D13 | the quote's currency not checked | the flow's unit test |
| D14 | a return option taken | the flow's unit test |
| D15 | the quoted amount dropped | the flow's unit test, e2e |
| D16 | the facts' subtotal before discount | unit |
| D17 | the facts without a country | unit |
| D18 | a parcel defaulting to the sold option | unit, e2e |
| D19 | the change hidden from the shopper | unit, e2e |
| D20 | every change listed under every method | the API's unit test |
| D21 | the changes not read back | unit, e2e |
| D22 | the entry keeping the credit line's id | integration, e2e |
| D23 | the credit index not unique | integration |
| D24 | the timeline not composing the entry | e2e |

D15 and D20 survived the first run. The flow's test quoted an option of 0, the
value a dropped amount decodes to, and the API's test had one method, under
which every change is its own. The option costs 900 now and the order has two
methods.
