# A screen that showed amounts — measured 2026-09-26

The evidence behind [ADR 0196](../adr/0196-the-panel-shows-where-an-order-goes.md).

## 1. What the order provider offered

Before this change, `internal/modules/order/service/provider.go` offered the
order row's own fields: id, display id, status, region, customer, e-mail,
currency, cart, the five amounts and the four moments. Its filters were `id`,
`customer_id`, `region_id` and `status`. Nothing about addresses, and nothing
naming another order.

The panel's order page asked for eleven of them and showed them under two
headings: the order and its amounts.

## 2. Who reads the read layer

`core/query` has no HTTP route (`grep` for a query path under `internal`,
`core` and `cmd` finds none). Its callers are the panel's screens, the order
module's own timeline and payment views, the cart and checkout flows, the
product store and the search plugin's reindex. So a field the order provider
offers reaches code in the installation and nothing else.

## 3. The reads the page makes

| Read | Fields | Filter |
|---|---|---|
| the order | its row, the three address fields, `adds_to_order_id` | `id` |
| the parent, when there is one | id, display id | `id` |
| the additions | id, display id, status, currency, total, placed at | `adds_to_order_id`, limit 25 |

The provider reads addresses once for the whole page, and only when an address
field is asked for; `TestTheReadLayerCarriesWhereAnOrderGoes` counts the reads.

## 4. The assembly

The panel's own tests fake the read layer, and a fake accepts any field name.
`TestThePanelShowsWhereARealOrderGoes` (`internal/app`) builds the panel from a
real installation's container, places an order with two addresses, an addition
to it, and corrects its street, then reads both pages:

| Page | Observed |
|---|---|
| the parent | the corrected street and not the old one, the billing company, "corrected …", a link to the addition |
| the addition | "Adds to" linking the parent by its number, "no shipping address" |

## 5. Mutations

| # | Mutation | Unit | Assembly |
|---|---|---|---|
| V1 | a panel field name misspelled | killed | killed |
| V2 | the shipping field reads the billing address | killed | killed |
| V3 | addresses read for every listing | killed | — |
| V4 | the earliest correction read as the latest | killed | survived |
| V5 | the additions filter ignored | killed | killed |
| V6 | a failed additions read fails the page | killed | — |
| V7 | the parent not read | killed | killed |
| V8 | the first shipping row read, current or not | killed | killed |

V4 survived the first run: every test corrected once, so the earliest and the
latest were the same moment. The provider test corrects twice now.
