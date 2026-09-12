# First run: from an empty database to a paid order

This document is EXECUTED. `internal/smoke/first_run_test.go` reads the block
below out of this file, hands it to a shell against the real binary and asserts
every status it prints — so a step that moved, a field that was renamed or a jq
path that no longer resolves fails a lane rather than a reader.

It answers one question: what does an operator have to create before a shopper
can buy something? The answer used to live in two test harnesses. It is fifteen
calls, and eleven of them were written down nowhere.

The security document walks the same first four steps and stops at reading the
catalog — which, on an empty database, is an empty list. This document continues
from there to an order.

## What each step is for

The four that are not obvious, because they are BINDINGS rather than records:

- **The countries a region serves.** `POST /store/v1/carts` carries no region and
  no currency: the server derives both from the country. A region with no country
  bound to it serves nobody, and every cart is refused with "the country is not
  served by any region".
- **The price set bound to the variant.** A price set is a record of prices; a
  variant with no binding to one has no price, and the cart workflow refuses to
  add a line it cannot price. The storefront never sends an amount, so there is
  no way around this.
- **The inventory item bound to the variant.** A variant with no binding is
  counted OUT OF STOCK, and its cart can never become an order. The item is what
  holds the quantity; the binding is what says this variant is that item.
- **The stock level at a location.** The item with no level has nothing anywhere.

And one that IS obvious and still catches people: the publishable key is born
bound to a sales channel, and a key with no channel is refused on the store
surface. The rule and its reasoning are in
[`security.md`](security.md#the-rule-is-applied-on-add-to-cart-too).

## The tax trap, measured

A region carries `automatic_taxes` and a `tax_rate_bps`, and setting them is NOT
how a one-country region gets taxed. The cart flow asks which country the region
resolves to; when that is exactly ONE country the TAX MODULE is the authority for
it, and its answer — including "this country has no tax region" — is taken as it
is, with no falling back to the region's rate. The rate you set is then dead
configuration.

The server does say so, in the log and not in the response:

    WARN the country's tax region is not configured; the tax was computed as zero
         cart_id=… country_code=TR tax_source=tax_unconfigured

An operator watching a storefront rather than a log sees a cart with no tax and
nothing else. The reasoning for taking tax's answer as it is — rather than
sliding back to the region whenever tax happens to be unconfigured — is written
where the choice is made, in `internal/workflows/cart`'s tax godoc, and it is
sound: a rate that moves according to which countries somebody has configured is
worse than one that is plainly absent.

Measured, on the real binary: a region with one country bound, `automatic_taxes`
true and `tax_rate_bps` 2000 produced a cart with `tax_total: 0`. The same body
with TWO countries bound produced 12800 on the same amounts — because a region
that resolves to no single country cannot name a jurisdiction, and the flow then
uses the region's rate as the previous authority.

So the block below creates a tax region and a default rate. That is the path an
installation actually wants: the rate lives where the jurisdiction does.

## The payment provider

The block completes the cart with the `manual` provider, which ships with the
framework and records an outcome the caller names. It is what a first run has:
an installation with no Stripe account can still take an order end to end and
see the amounts. A real provider replaces this one call and nothing else — the
cart, the totals and the order are the same records.

## The block

Every id is captured into a variable, so the only lines this prints are a status
per binding, the cart's total, and the order. A reader who sees a status other
than the one in the comment knows which call broke — the alternative is a later
call answering with an error envelope and nothing saying why.

```bash
# 0) The first administrator (only on an empty database)
ADMIN_BOOTSTRAP_EMAIL=admin@example.com \
ADMIN_BOOTSTRAP_PASSWORD='…' make run

# 1) Log in -> token
TOKEN=$(curl -s localhost:9000/admin/v1/auth/login \
  -H 'content-type: application/json' \
  -d '{"email":"admin@example.com","password":"…"}' | jq -r .data.token)

# 2) The region: its currency, and a fallback tax rate in basis points
REGION=$(curl -s localhost:9000/admin/v1/regions \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d '{"name":"Default region","currency_code":"TRY","automatic_taxes":true,"tax_rate_bps":2000}' \
  | jq -r .data.id)

# 3) The countries it serves -> 201. Without this no cart can be opened at all.
curl -s -o /dev/null -w '%{http_code}\n' localhost:9000/admin/v1/regions/$REGION/countries \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d '{"country_code":"TR"}'

# 4) Tax: a region in the TAX module for that country, and its default rate.
#    Without these the cart is untaxed — see "The tax trap" above.
TAXREGION=$(curl -s localhost:9000/admin/v1/tax-regions \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d '{"country_code":"TR"}' | jq -r .data.id)

curl -s -o /dev/null -w '%{http_code}\n' localhost:9000/admin/v1/tax-rates \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d "{\"tax_region_id\":\"$TAXREGION\",\"name\":\"VAT\",\"rate_bps\":2000,\"is_default\":true}"

# 5) The sales channel and the storefront's key
SC=$(curl -s localhost:9000/admin/v1/sales-channels \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d '{"name":"Default storefront"}' | jq -r .data.id)

PK=$(curl -s localhost:9000/admin/v1/api-keys \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d "{\"title\":\"storefront\",\"type\":\"publishable\",\"scopes\":[],\"sales_channel_ids\":[\"$SC\"]}" \
  | jq -r .data.key)

# 6) A product and one variant under it
PRODUCT=$(curl -s localhost:9000/admin/v1/products \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d '{"handle":"first-run-product","title":"First run product","status":"published"}' \
  | jq -r .data.id)

VARIANT=$(curl -s localhost:9000/admin/v1/products/$PRODUCT/variants \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d '{"title":"First run product"}' | jq -r .data.id)

# 7) A price in the region's currency, then BOUND to the variant -> 200
PRICESET=$(curl -s localhost:9000/admin/v1/price-sets \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d '{"prices":[{"currency_code":"TRY","amount":32000,"min_quantity":1}]}' | jq -r .data.id)

curl -s -o /dev/null -w '%{http_code}\n' -X PUT \
  localhost:9000/admin/v1/variants/$VARIANT/price-set \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d "{\"price_set_id\":\"$PRICESET\"}"

# 8) A location, an inventory item, the binding -> 200, and the quantity -> 200
LOCATION=$(curl -s localhost:9000/admin/v1/stock-locations \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d '{"name":"Default warehouse"}' | jq -r .data.id)

ITEM=$(curl -s localhost:9000/admin/v1/inventory-items \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d '{"sku":"FIRST-RUN-1","title":"First run product"}' | jq -r .data.id)

curl -s -o /dev/null -w '%{http_code}\n' -X PUT \
  localhost:9000/admin/v1/variants/$VARIANT/inventory-item \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d "{\"inventory_item_id\":\"$ITEM\"}"

curl -s -o /dev/null -w '%{http_code}\n' \
  localhost:9000/admin/v1/inventory-items/$ITEM/levels \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d "{\"location_id\":\"$LOCATION\",\"stocked_quantity\":5}"

# 9) The shopper: a guest cart from the country, then a line -> 201
CART=$(curl -s localhost:9000/store/v1/carts \
  -H "x-publishable-api-key: $PK" -H 'content-type: application/json' \
  -d '{"country_code":"TR","email":"shopper@example.com"}' | jq -r .data.id)

curl -s -o /dev/null -w '%{http_code}\n' localhost:9000/store/v1/carts/$CART/line-items \
  -H "x-publishable-api-key: $PK" -H 'content-type: application/json' \
  -d "{\"variant_id\":\"$VARIANT\",\"quantity\":2}"

# 10) The cart's total, computed by the server: 2 x 32000 plus 20% tax
curl -s localhost:9000/store/v1/carts/$CART \
  -H "x-publishable-api-key: $PK" | jq -r .data.total

# 11) Pay and become an order. expected_total is MANDATORY: the amount the
#     customer approved is declared, and a cart whose total has moved since is
#     refused with 409 rather than charged.
ORDER=$(curl -s localhost:9000/store/v1/carts/$CART/complete \
  -H "x-publishable-api-key: $PK" -H 'content-type: application/json' \
  -d '{"payment_provider_id":"manual","payment_data":{"manual_outcome":"authorize"},"expected_total":76800}' \
  | jq -r .data.order_id)

echo "order $ORDER"
```

## What this does NOT set up

The tax here is ONE default rate for one country. A real installation writes
rates per province and per product class, and can stack and compound them; the
tax module's surface does all of that and this block uses the smallest corner of
it.

The **search index** is empty. It is fed by events, and every row above was
written through the API, so the index is in step — but `gobit seed`, which writes
bulk SQL for the load rig, does not produce events and leaves it behind. That
cost is written down in `internal/rig`'s own godoc.

Nothing here creates a **customer**. The cart is a guest's, which is the
storefront's default path; gobit issues no customer identity of its own (see
[`known-limits.md`](known-limits.md)).
