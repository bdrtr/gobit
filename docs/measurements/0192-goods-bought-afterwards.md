# Goods bought afterwards — measured 2026-09-26

The evidence behind [ADR 0192](../adr/0192-an-order-can-add-to-another.md).

## 1. What an addition reuses

Measured by reading the checkout from the cart's opening to the order's write,
before anything was written. Each row is a thing an addition needs and the
place that already does it for every order.

| Need | Where it already happens | New for an addition |
|---|---|---|
| a price per line | the cart workflow's ladder (`AddLineItem`) | nothing |
| tax, discounts, coupons | the cart's totals round | nothing |
| an operator on the telephone | `POST /admin/v1/carts` (ADR 0146) | one body field |
| a shopper on the storefront | `POST /store/v1/carts`, customer proven (ADR 0125) | one body field |
| stock taken as a sale | the saga's reservation step | nothing |
| money | the saga's authorize and capture, the order's own `order_payment` link | nothing |
| books | the order journal's `order placed` entry (ADR 0188), the payment journal's capture | nothing |
| an invoice | the order's own, from its series (ADR 0024) | nothing |
| the link to the parent | none | `carts.adds_to_order_id`, `orders.adds_to_order_id` |
| the parent's rules | none | the order module's check, at opening and at the write |

The saga's steps run reserve → redeem → **create order** → authorize →
capture → clear. A refusal at the order's write comes before any payment, so no
earlier check had to be added to the checkout.

## 2. The lock

`TestAnAdditionWaitsForACancellationAndIsRefused` holds a cancellation of the
parent after its `FOR UPDATE` and before its write, starts the addition, and
waits until `pg_stat_activity` shows exactly one session blocked by the
cancellation's backend. Then it releases the cancellation.

With the parent read under `FOR SHARE`, the addition waits, reads the canceled
parent, and is refused with `order_addition_parent_not_pending`; no row is left.

The same test with `checkParent` reading the parent through `GetOrder` instead
of `ShareLockOrder`, run by hand before the table below: the addition **still
waited** (the `Eventually` on the blocked session passed), because the insert's
foreign key takes `FOR KEY SHARE` on the parent and that conflicts with
`FOR UPDATE`. But it had read `pending` before it reached the insert, so once
the cancellation committed the insert went through, and an addition to a
canceled order was written:

```
--- FAIL: TestAnAdditionWaitsForACancellationAndIsRefused (0.04s)
    Error:     An error is expected but got nil.
    Messages:  an addition to an order canceled while it waited has to be refused
```

So "the addition waited" is not evidence of the rule. The lock has to be taken
before the status is read.

## 3. The gate that asked

`TestNoStorefrontWriteStoresAnUnjudgedIdentityClaim` failed on the first run:
`cart.carts.adds_to_order_id` ends in `order_id`, the prior-record name
ADR 0051 refused on reviews, and fourteen storefront routes reach the table. It
is recorded as CONFINED, because the order module accepts the id only when the
order's customer is the cart's proven customer.

## 4. The end-to-end path

`internal/e2e/order_addition_test.go`, on the production wiring, one variant at
10,000 minor units in the taxed region (20%), ten on the shelf:

| Step | Observed |
|---|---|
| parent placed, one unit | order pending, total 12,000 |
| admin cart opened with the parent's id | 201, the cart carries `adds_to_order_id` |
| addition checked out on the storefront | order with `adds_to_order_id` = parent, total 12,000 |
| `GET /store/v1/orders/{id}` of the addition | carries `adds_to_order_id` |
| `GET /admin/v1/orders?adds_to_order_id=` | count 1, the addition |
| the parent | total 12,000, no `adds_to_order_id`, pending |
| sellable | 8 |
| guest storefront cart naming the parent | 409 `order_addition_needs_customer` |
| another customer / an addition / no such order | 409 / 409 / 404, the order module's codes |
| parent completed after the cart opened, then checkout | 409 `order_addition_parent_not_pending`, no addition, sellable back to 9 |

## 5. Mutations

Each applied alone, the named lanes run with `-count=1`, the file restored.

| # | Mutation | Unit | Integration / e2e |
|---|---|---|---|
| M1 | the checkout's wire tag misspelled | killed | killed |
| M2 | the cart snapshot's wire tag misspelled | killed | killed |
| M3 | the check at the cart's opening skipped | killed | killed |
| M4 | the pending rule dropped | killed | killed |
| M5 | the merge rule dropped | killed | — |
| M6 | the order record drops the field | killed | killed |
| M7 | the repository ignores the list filter | — | killed |
| M8 | `FOR SHARE` removed from the generated SQL | — | killed |
| M9 | the customer rule dropped | killed | killed |
| M10 | the currency rule dropped | killed | — |
| M11 | the no-chains rule dropped | killed | killed |
| M12 | the guest rule dropped | killed | killed |
| M13 | the write's check skipped | killed | killed |

M6 survived the unit lane on the first run; only the e2e test saw it.
`TestAnAdditionSaysWhatItAddsTo` was added and kills it there too.

## 6. Not measured, not decided

- Shipping an addition with its parent. Each order opens its own parcels.
- A list of additions on the parent's record; the listing answers it.
- The parent's timeline naming its additions.
