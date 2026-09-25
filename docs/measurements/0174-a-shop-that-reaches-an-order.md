# A shop that reaches an order — measured 2026-09-25

The evidence behind [ADR 0174](../adr/0174-the-example-shop-checks-out.md).

## 1. The run

The example binary against a fresh database, prepared by the executed block of
`docs/first-run.md` as it stands after ADR 0173 — region, country, tax, channel
and key, one product with a price and five in stock, a shipping profile and a
flat option of 4900 — plus a second option, Express at 9900, created by hand.
Chrome drove the pages; the server log is the source for every call below.

| Page | What the shopper did | What the page showed |
|---|---|---|
| `/shop` | — | `First run product — 32000 TRY (minor units)` |
| product | Add to cart | the cart: one line at 38400, tax 6400 |
| checkout | e-mail and address, Continue | `Standard — 4900`, `Express — 9900` |
| checkout | Ship this way (Standard) | `Total 43300 … shipping 4900, tax 6400` |
| checkout | Place the order with `loyalty_points` | the refusal (section 3) |
| checkout, again | the form again, Express | `Total 48300 … shipping 9900, tax 6400` |
| checkout, again | the form again, Express again | the same total; nothing was re-added |
| checkout | Place the order with `manual` | `Order order_… was placed for 48300 TRY` |

The order, read through the admin API: status `pending`, total 48300, shipping
total 9900, the e-mail the form took. No console error and no content-policy
violation on any page.

## 2. What the shop calls

Thirteen entries in the script's route table, and no other mention of the store
prefix in the file.

| Entry | Route |
|---|---|
| regions | `GET /store/v1/regions` |
| products | `GET /store/v1/sales-channels/{sales_channel_id}/products` |
| product | `GET /store/v1/sales-channels/{sales_channel_id}/products/{id}` |
| openCart | `POST /store/v1/carts` |
| cart | `GET /store/v1/carts/{id}` |
| updateCart | `POST /store/v1/carts/{id}` |
| addLine | `POST /store/v1/carts/{id}/line-items` |
| shippingAddress | `PUT /store/v1/carts/{id}/shipping-address` |
| addShipping | `POST /store/v1/carts/{id}/shipping-methods` |
| removeShipping | `DELETE /store/v1/carts/{id}/shipping-methods/{shipping_method_id}` |
| complete | `POST /store/v1/carts/{id}/complete` |
| shippingOptions | `GET /store/v1/shipping-options` |
| paymentProviders | `GET /store/v1/payment-providers` |

The draft wrote the two catalog templates as `{id}` and `{handle}`. The routes
are bound as `{sales_channel_id}` and `{id}`, so a gate comparing strings would
have refused them; the templates were rewritten as bound. The draft also read a
country's `iso_2`, a field the store region does not have; it reads `code`.

`TestTheShopCallsOnlyBoundRoutes` (`internal/arch`), mutated:

| Mutation | Result |
|---|---|
| `PUT …/shipping-address` written as `POST` | red: no such route |
| `{sales_channel_id}` written as `{id}` | red: no such route |
| the regions entry split into `"GET " + "/store/v1/regions"` | red: thirteen mentions of the prefix, twelve entries |

## 3. A guest who picks a person's tender

The store endpoint lists `loyalty_points`, `manual` and `store_credit`. With a
guest cart and `loyalty_points`, the server log reads, in order:

```
msg="order placed" workflow=complete_cart … amount=43300
msg="notification NOT SENT: the 'log' provider only records" … template=order.placed
msg="workflow: a step failed, compensation is starting" … step=authorize_payment
    error="payment_loyalty_points_no_customer: …"
msg="compensation: order canceled"
msg="compensation: stock reservations released"
status=409
```

The refusal is right and it is late. An installation with a mail provider sends
the `order.placed` mail for an order canceled a moment later. The page shows the
refusal's message. The list endpoint takes no cart, so it cannot answer which
providers this cart may use.

## 4. The way back

Returning to the checkout after that refusal, and choosing the option already
on the cart, was refused before this record's last change:

```
this shipping option has already been added to the cart
```

The shopper had no way forward on the page. The checkout now removes the cart's
methods for other options and adds the chosen one only when it is missing; the
second and third rows from the bottom of section 1 are that path.

## 5. Sizes

| File | Lines |
|---|---|
| `storefront/assets/storefront.js` | 610 |
| `storefront/pages.go` | 188 |
| `storefront/storefront.go` | 126 |
| `README.md` | 79 |
| `storefront/templates/page.gohtml` | 54 |
| `main.go` | 55 |
