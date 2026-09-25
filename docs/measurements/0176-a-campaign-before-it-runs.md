# A campaign before it runs — measured 2026-09-25

The evidence behind [ADR 0176](../adr/0176-a-promotion-can-be-tried-on-past-orders.md).

## 1. What already existed

| Need | Where it already was |
|---|---|
| What each sold line was charged and discounted | `order_line_items.unit_price`, `subtotal`, `discount_total` |
| Sales of a period | the `order_line_item` Query entity's `placed_from`/`placed_to` filters (B14), paged 100 at a time, newest first |
| The order's region, customer, currency, cart | the `order` Query entity, by id |
| A purchase in the engine's terms | the cart flow's `discountRequestFor`, split for this record into a context read and `discountRequestWith` |
| Whether a promotion already discounted a purchase | `promotion_redemption.reference` — the checkout redeems every promotion it applied, automatic ones too, under the cart's id |
| One decision point for "may this apply" | `skipReasonOf` (ADR 0110) |

Nothing new was stored. The trial reads the order module through the Query
layer the cart flow already held as `Catalog`, and calls the promotion module
through the surface it already called.

## 2. Why no other promotion is recomputed

The engine's own rules, written in its godoc: percentages are taken off the
line's ORIGINAL amount and do not compound; a line's total discount cannot
exceed the line; the part a cap cuts off is dropped, not moved. So on a line
with an actual discount `a`, a subtotal `s` and the trial's standalone discount
`p`, the discount with the promotion added is `min(a + p, s)` whichever order
they were applied in — only which promotion the cut is credited to changes. The
trial adds `min(a + p, s) − a` and never recomputes the order's own promotions,
whose state today may not be their state then.

Unit case: a 1000 line discounted 900 and a 500 line discounted 0, the trial
priced at 200 and 100 → it adds 100 + 100 = 200, not 300.

## 3. What a purchase cannot tell the trial

| Fact | At the moment of sale | Read by the trial as |
|---|---|---|
| lines, prices, quantities, actual discount | kept on the order | as kept |
| region, currency, customer | kept on the order | as kept |
| product id, collection, categories, tags, flags | not kept | today's catalog |
| the customer's groups | not kept | today's groups |
| the cart's metadata (ADR 0111) | overwritten with the cart | absent: a `cart.` rule matches nothing |

These three, with the promotion module's four (`active`, `automatic`,
`no_usage_limit`, `no_campaign`), are the report's `assumptions`.

## 4. End to end

`TestAPromotionCanBeTriedOnTheOrdersItWouldHaveDiscounted` (`internal/e2e`): a
draft coupon, 10% on one variant only this scenario sells. Two orders of two
units at 12 340 were placed, and a third whose payment was declined, which the
saga places and cancels. The report named exactly the two, each with 2 468
added; the canceled one was counted apart. The promotion stayed a draft with no
redemption. An identity with `promotion:read` alone got 403, and a period
ending an hour from now got 422.

## 5. What bites

| Mutation | Result |
|---|---|
| the promotion is not made active | e2e red (a draft is skipped); unit red |
| the added discount ignores the actual one and the cap | unit red: 300 where 200 is right |
| the order privilege is not asked | e2e red: the promotions-only identity got the report |
| canceled orders are priced | e2e red: the canceled order appears in the report |
| an already-redeemed purchase is priced again | unit red |

A first attempt at the last one did not compile, and was re-run as one that
does; a mutation that does not build proves nothing about the tests.

## 6. The language ledger

The promotion module was on the ledger almost whole. New code went into new
English files. Single lines were added to `api.go`, `describe.go` and
`module.go`, and those lines are English. The endpoint table in
`describe_internal_test.go` could not take a row without new entries in its
Turkish-named struct, so that file was translated whole and left the ledger.
