# What a cart is priced in — measured 2026-10-05

The evidence behind [ADR 0397](../adr/0397-a-cart-is-priced-in-the-channel-it-was-opened-in.md).
Taken on the tree at `21f452f2` (ADR 0396, D252), before the change. Files are
named with `grep -rl`; no line numbers are given, because they move.

## 1. A rule may name any attribute

A price rule is a triple, `(attribute, operator, rule_values)`. The table's
only check on the attribute is that it is not empty
(`price_rule_attribute_check CHECK (attribute <> '')` in
`internal/modules/pricing/migrations/000001_pricing_init.up.sql`), and the
service refuses only a blank one (`internal/modules/pricing/service/validate.go`).
So `sales_channel_id eq sc_A` could be written before this record. It matched
no cart: a rule naming an attribute the context does not carry does not match
(`internal/core/condition`), and the context was built in one function,
`ruleContext` in `internal/workflows/cart/catalog.go`, from the region, the
cart's metadata under `cart.`, the customer, their company and their head
group. No channel.

## 2. Every write reprices the whole cart, under the principal that made it

`grep -rl "CalculateTotals\|RepriceAfter\|\.repriced(" internal --include='*.go'`
outside tests names `internal/modules/cart/api/api.go`,
`internal/modules/cart/api/store.go`,
`internal/modules/cart/api/telephone_surface.go`,
`internal/modules/cart/module.go`, and in `internal/workflows/cart`
`add_line_item.go`, `interop.go`, `promotion_code.go`, `shipping.go`,
`totals.go` and `update_line_item.go`, plus the checkout's `deps.go` and
`plan.go`. A round fetches every line's unit price again; no stored amount is
trusted (`Snapshot` godoc, `internal/workflows/cart/snapshot.go`).

What the request's principal holds on each write:

| Write | Surface | Channels on the principal |
|---|---|---|
| open a cart | `POST /store/v1/carts` | the publishable key's set |
| add or raise a line, remove one, set an address, add a shipping method, a coupon, a merge | `/store/v1/carts/...` | the publishable key's set |
| complete | `POST /store/v1/carts/{id}/complete` | the publishable key's set |
| open a cart | `POST /admin/v1/carts` | none; the operator holds no channel |
| add a line | `POST /admin/v1/carts/{id}/line-items` | the one the body claims |
| set an address, add a shipping method, remove a line | `/admin/v1/carts/{id}/...` | none |
| complete | `POST /admin/v1/carts/{id}/complete` | the one the body claims |
| the panel's telephone order | `cart.admin` | as the admin route it mirrors |

An operator's cart is therefore repriced under at least two different
principals between its first line and its completion. A channel read off the
request would price the line write at the channel's price and the address write
after it at the region's, and the completion compares `expected_total` against
its own round (ADR 0286). That is why the channel is the cart's.

## 3. The ladder ties a channel-only price with a region-only one

`CalculatePrice` (`internal/modules/pricing/service/calculate.go`) ranks
survivors by list priority, then the buyer rank (only `customer_id` and
`company_id` count, `PriceRule.BuyerRank` in
`internal/modules/pricing/models/models.go`), then the number of matched rules,
then the quantity range, then the amount, then the id. A region price is an
ordinary base price whose one rule is `region_id eq ...`.

| Candidate A | Candidate B | Winner |
|---|---|---|
| `sales_channel_id eq sc_A`, 1000 | `region_id eq reg`, 800 | B: one rule each, the cheaper |
| `region_id eq reg` and `sales_channel_id eq sc_A`, 1000 | `region_id eq reg`, 800 | A: two rules beat one |
| a sale list price, any rules | a channel base price | the list (priority) |
| an override contract on `customer_id` | a channel base price | the contract |

Pinned by `TestAChannelOnlyPriceTiesWithTheRegions`
(`internal/modules/pricing/service/channel_price_test.go`).

## 4. A country is in at most one region

`internal/modules/region/migrations/000001_region_init.up.sql` puts a single
`region_id` column on the `country` row, and its comment calls the rule
structural. The currency is `region.currency_code`. A link from a region to a
channel could therefore only narrow which countries a channel sells to; it
could never give one country a second currency. Selling one country in two
currencies by storefront needs a country in several regions, chosen by
channel, which undoes that column.

## 5. The trials priced without the cart's metadata and did not say so

The price list trial (ADR 0220) builds its context with
`w.ruleContext(ctx, Snapshot{RegionID, CustomerID, CurrencyCode})`
(`internal/workflows/cart/list_trial.go`): no metadata, because an order does
not keep it. A live cart's price round reads the metadata through the same
`ruleContext` (`totals.go`, `add_line_item.go`). The promotion trial published
`no_cart_metadata` (`internal/workflows/cart/trial.go`); the price list trial
published `todays_prices`, `todays_price_set_links`, `todays_customer_groups`,
`list_active_without_window` and `before_discounts`, and its godoc said the
context was "the same context a cart's own price reads". That is D253.

Five living sentences said a cart's metadata enters no calculation:
`docs/commerce-flows.md`, the `Carts.OpenCart` godoc
(`internal/workflows/cart/deps.go`), `CreateCartInput.Metadata`
(`internal/workflows/cart/create_cart.go`), the storefront request's godoc
(`internal/modules/cart/api/store.go`) and a test's godoc
(`internal/modules/cart/api/api_test.go`).

## 6. The storefront's product reads drop every ruled price

`listablePrices` (`internal/modules/pricing/service/provider.go`) keeps a price
only when it has no rule, because the read carries no context to evaluate one.
A channel price is ruled, so the channel-scoped product reads (ADR 0044) show
the price without the channel; it appears at the cart.

## 7. Records at their limit

`docs/adr/0146-an-operator-can-build-a-cart.md` is 80 lines, the limit
`TestAGovernedADRFitsInEightyLines` holds, so the amendment is appended to its
Status line rather than given a header line of its own.
`docs/adr/0216-a-wishlist-item-can-ask-for-its-price.md` is 71 lines and takes
an `- **Amended by:**` line.

## 8. The merge

`MergeCart` (`internal/modules/cart/service/merge.go`) moves lines only; the
target's email, addresses, shipping and metadata stay. The repricing after it
(`RepriceAfter`, `internal/workflows/cart/interop.go`) reads the target's
snapshot, so the moved lines take the target's channel. A refusal across
channels was measured against the upgrade: every cart opened before migration
000010 names none, so a login merge of a guest cart opened through a
single-channel key into such a cart would have answered 409.
