# The cart's bag and a price — measured 2026-10-05

The evidence behind [ADR 0403](../adr/0403-a-carts-metadata-chooses-no-price.md)
and gaps D260 and D261. Taken on the tree at `97d25ee2` (ADR 0400, D259).
Files are named with `grep -rl`; no line numbers are given, because they move.

## 1. Who writes the bag

- `POST /store/v1/carts` decodes `metadata` from the body
  (`createCartRequest` in `internal/modules/cart/api/store.go`) and hands it to
  the flow as it is. The route is guarded by the publishable key, which every
  browser of the storefront holds and which is no secret (`core/http/auth.go`,
  ADR 0008).
- `POST /admin/v1/carts` takes `metadata` too
  (`internal/modules/cart/api/admin_write.go`).
- No other route writes a cart's metadata: `POST /store/v1/carts/{id}` takes
  the e-mail and the customer, not the bag.

ADR 0111's Consequences say "the shopper must not write the left-hand side of
a discount rule". Before this record, the `CartAttributePrefix` godoc
(`internal/workflows/cart/catalog.go`) said "the shopper does not write the
cart's metadata", and the interop's `Metadata` godoc
(`internal/modules/cart/service/interop.go`) repeated ADR 0111's sentence.

## 2. Where the bag went

At `97d25ee2`, `ruleContext` in `internal/workflows/cart/catalog.go` wrote the
region, the bag under `cart.`, the sales channel, the customer, the company and
the groups, and six callers used it:

| Caller | File | Rounds | Snapshot carried the bag |
|---|---|---|---|
| the line's opening price | `add_line_item.go` (add-ons reuse it) | price | yes |
| the totals' batch price | `totals.go` | price | yes |
| the wishlist quote | `quote.go` | price | no: built from the region and the customer |
| the price list trial | `list_trial.go` | price | no: an order keeps no bag |
| the discount request | `discount.go` | promotion | yes |
| the promotion trial | `trial.go` | promotion | no |

Pricing took any attribute name (`validateRule` in
`internal/modules/pricing/service/validate.go`) and matched it exactly
(`matchRule` in `internal/modules/pricing/service/calculate.go`, reading through
`internal/core/condition`). So a price rule on `cart.arm` chose a cart's line
and totals price, while the quote, which ADR 0216 says is "the unit price their
cart would be charged", never met it. The price list trial published
`no_cart_metadata` among its assumptions from ADR 0397 on (gap D253).

## 3. Reproduction

On the unchanged tree, with the tests of this record added:

- `TestTheCartsBagReachesNoLinePrice` and `TestTheCartsBagReachesNoTotalsPrice`
  (`internal/workflows/cart/rule_context_test.go`) failed: the single price
  call and the batch price request each carried `cart.arm`.
- `TestAPriceRuleCannotNameTheCartsBag`
  (`internal/modules/pricing/service/service_test.go`) and
  `TestARuleOnTheCartsBagIsRefused` (`internal/modules/pricing/api/api_test.go`)
  failed: every write accepted `cart.arm`.
- The list trial's test failed on `no_cart_metadata`.
- `TestAnOldCartRuleRidesThroughEveryKeepingWrite`
  (`internal/modules/pricing/service/admin_test.go`) passed, and is the guard
  that the refusal does not reach the writes below.

## 4. The writes that carry a set's other prices

| Write | Path | Revalidates what it carries |
|---|---|---|
| The panel's price form and the interop `SetUnitBasePrices` | `reviseUnitBasePrices` in `internal/modules/pricing/service/admin.go` | yes, through `buildPrices` |
| The catalog import | `internal/modules/product/service/import_prices.go` → `SetUnitBasePrices` | same path |
| The panel's list price added or removed | `revise` in `internal/modules/pricing/service/admin_list_prices.go` | yes, through `buildPrices` |
| `SetBasePrices` | `internal/modules/pricing/service/interop.go` → `SetPrices` | writes no rule |

The panel's list price form writes `customer_group_id` alone, and no internal
path adds a `cart.` rule. The three writes that take a rule a caller wrote are
`CreatePriceSet`, `SetPrices` and `CreatePriceRule`, behind
`POST /admin/v1/price-sets`, `POST /admin/v1/price-sets/{id}/prices` and
`POST /admin/v1/prices/{price_id}/rules` (`internal/modules/pricing/api/admin.go`).

## 5. What an old rule does after the change

`better` in `calculate.go` ranks a price on a list above a base price, then
the price naming the buyer, then the one with more rules, and only then the
narrower quantity range and the lower amount. A `cart.` rule's price stays
on its set and matches no cart, since no cart's price context names the
attribute; `GET /admin/v1/price-sets/{id}/calculate?attr_cart.arm=B` still
matches it. Deleting the rule alone leaves its price to everybody its other
rules match, and a list price would beat the base for them; removing the price
is the cleanup. The rules to look at are listed by
`SELECT price_id, attribute FROM price_rule WHERE attribute LIKE 'cart.%' AND deleted_at IS NULL`.

## 6. What stays open (D261)

`discount.go` still reads the bag, so a promotion ruled on a `cart.`
attribute is met by any caller who opens a cart with that metadata through the
publishable key.
`TestACartsOwnDataCanRuleAPromotion` (`internal/e2e/coupon_test.go`) is the
feature ADR 0111 shipped, and it still passes.
