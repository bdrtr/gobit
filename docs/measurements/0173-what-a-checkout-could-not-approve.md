# What a checkout could not approve — measured 2026-09-25

The evidence behind [ADR 0173](../adr/0173-a-storefront-write-leaves-the-cart-priced.md).

## 1. How it was found

The storefront example's checkout, while it was being written, was driven in
a real browser against the example binary, on a database the executed first run
had prepared, with one flat shipping option of 4900 added. The page took an
address, listed the option, added it, and showed the payment step:

```
Total 38400 TRY (minor units), shipping 0, tax 6400
```

The cart read straight after, with the store key:

```
{"subtotal":32000,"shipping_total":0,"tax_total":6400,"total":38400,
 "totals_stale":true,"shipping_methods":[{"name":"Standard","amount":4900,…}]}
```

Placing the order with the manual provider sent `expected_total: 38400` and was
refused:

```
cart amount differs from the approved amount: approved 38400, calculated 43300
```

43300 is 32000 + 6400 tax + 4900 shipping, which is not taxed. The completion
was right and the page was right about what it read; the cart could not tell
the page what it would charge.

## 2. Which writes left the totals stale

Every storefront cart write, by what it calls. A write through the cart
service's `mutate` frame raises the revision; only a flow can compute totals.

| Write | Calls | Repriced before this record |
|---|---|---|
| `POST /store/v1/carts/{id}/line-items` | line flow | yes |
| `PATCH /store/v1/carts/{id}/line-items/{line_item_id}` | line flow | yes |
| `POST /store/v1/carts/{id}/promotions` | coupon flow | yes |
| `DELETE /store/v1/carts/{id}/promotions/{code}` | coupon flow | yes |
| `POST /store/v1/carts/{id}/shipping-methods` | shipping flow | **no** — the flow quoted and wrote, and stopped |
| `POST /store/v1/carts/{id}` | service | **no** |
| `PUT /store/v1/carts/{id}/shipping-address` | service | **no** |
| `PUT /store/v1/carts/{id}/billing-address` | service | **no** |
| `DELETE /store/v1/carts/{id}/line-items/{line_item_id}` | service | **no** |
| `DELETE /store/v1/carts/{id}/shipping-methods/{shipping_method_id}` | service | **no** |
| `POST /store/v1/carts/{id}/merge` | service | **no** |

Two readings of the table. Removing a line with `DELETE` left the cart stale,
while `PATCH` to quantity zero removes the same line through the flow and
reprices. And the cart flows' surface said, in its godoc, that
`CalculateTotals` "is NOT here and will not be", so no handler could have
repriced even by choice.

No end-to-end test added a shipping method through the storefront:

```
$ grep -rl "shipping-methods" --include='*_test.go' internal
internal/modules/cart/api/describe_internal_test.go
internal/modules/cart/api/api_test.go
```

Both are the cart module's own tests, over a fake flow. The one real-stack test
with a shipping method (`TestShippingDoesNotEnterTaxBase`) adds it through the
service and calls `CalculateTotals` itself, which is the step the storefront
had no way to take.

## 3. The gate, and what bites it

`TestNoStorefrontWriteLeavesTheCartStale` (`internal/e2e`) walks the router for
every non-GET route under `/store/v1/carts/{id}`, leaves out the completion and
the delete, and requires a scenario for each: eleven writes, eleven scenarios.
Each opens a cart with a line, makes the write so that it changes the cart, and
reads `totals_stale`.

| Mutation | Result |
|---|---|
| the shipping flow does not reprice | 1 of 11 red: `POST …/shipping-methods` |
| `RepriceAfter` does not reprice | 6 of 11 red: exactly the six service writes |
| a scenario removed | red: the route has no scenario |
| an exclusion renamed | red: the exclusion names no route, and the route it hid has no scenario |

The first-run block now adds the address and the option before reading the
total. With the shipping flow not repricing, the smoke lane reads 76800 where
the document's numbers give 81700 and fails on the total. The order itself is
still placed, because the completion recomputes: the defect was never a wrong
charge, it was a page that could not show the right one.
