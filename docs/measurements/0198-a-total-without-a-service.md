# A total without a service — measured 2026-09-26

The evidence behind [ADR 0198](../adr/0198-an-order-remembers-the-delivery-it-was-sold.md).

## 1. Where the delivery was dropped

| Hop | Before this change |
|---|---|
| `cart_shipping_methods` | option id, name, amount, free-form data |
| the cart's snapshot (`CartSnapshotJSON`) | each method's id and amount only |
| the checkout's `Snapshot` | no methods: "Unrecognized fields (shipping methods, for one) are silently skipped" |
| the order's `interopSnapshot` | no methods: "the shipping methods … are of no use to the order" |
| `orders` | `shipping_total` |

`grep shipping_option internal/modules/order/migrations/*.up.sql` found the
column only on claim replacements, which name the option they ship on.

## 2. The reader that needed it

`POST /admin/v1/orders/{id}/fulfillments` required `shipping_option_id`, and the
operator had nothing on the order to say which option the shopper had paid
for. The replacement dispatch names its own option, from the claim or exchange,
and is unchanged.

## 3. Why the order can hold the sum

`workflows/cart/totals.go` computes `ShippingTotal` as the sum of the cart's
methods (`shippingTotalOf`), and the checkout refuses a snapshot whose revision
is not the totals' (`CodeCartChanged`). So the methods the order receives add up
to the shipping total it receives, and the order refuses them when they do not,
as it refuses lines that do not add up to the subtotal.

## 4. The end-to-end path

`internal/e2e/sold_delivery_test.go`: a cart with one method on a
storefront-visible spy option charging 3,000, checked out on the storefront.

| Read | Observed |
|---|---|
| `GET /admin/v1/orders/{id}` `shipping_methods` | one: the spy option, its name, 3,000 |
| the order's `shipping_total` | 3,000 |
| a parcel opened with only an idempotency key | the spy carrier's `Create` has the option the shopper chose |

## 5. Mutations

| # | Mutation | Killed by |
|---|---|---|
| S1 | the checkout drops the methods | the checkout's unit test, e2e |
| S2 | the cart's snapshot drops the name | e2e on the first run; the cart's unit test since |
| S3 | the order ignores the methods | the order's unit test, e2e |
| S4 | the sum not held | the order's unit test |
| S5 | the methods not read back | the order's unit test, e2e |
| S6 | no default option | the flow's unit test, e2e |
| S7 | the first of several methods used | the order's unit test |
| S8 | the methods not written | the order's unit test, e2e |

S2 survived the unit lane: nothing checked the cart's snapshot for the method's
name, and the order refusing a nameless method was visible only end to end.
`TestTheSnapshotSaysWhichDeliveryWasChosen` holds it now.
