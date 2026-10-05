# Who reads the cart's bag — measured 2026-10-06

The evidence behind [ADR 0407](../adr/0407-a-carts-metadata-reaches-no-rule.md)
and gap D261. Taken on the tree at `d46741c2` (ADR 0404, ADR 0405).
Files are named with `grep -rl`; no line numbers are given, because they move.

## 1. Who writes the bag

- `POST /store/v1/carts` decodes `metadata` from the body
  (`internal/modules/cart/api/store.go`) under the publishable key, which every
  browser of the storefront holds.
- `POST /admin/v1/carts` takes it too
  (`internal/modules/cart/api/admin_write.go`).
- In process, `OpenCartForCountry` and `CreateCartInput.Metadata` carry it
  (`internal/workflows/cart/interop.go`, `internal/workflows/cart/create_cart.go`),
  and the cart module's interop opens the cart with it
  (`internal/modules/cart/service/interop.go`).
- Not the panel's telephone order, which opens a cart with no metadata
  (`internal/modules/cart/api/telephone_surface.go`); not
  `POST /store/v1/carts/{id}`, which takes the e-mail and the customer; not a
  merge, which moves lines; and no `.graphqls` schema names a cart's metadata.
  The bag is written once, when the cart opens.

## 2. Who read it

At `d46741c2`, `addCartMetadata` in `internal/workflows/cart/catalog.go` wrote
the bag's string values under `cart.`, at most 32 of them, through
`ruleContext`, which two callers used: the discount round
(`internal/workflows/cart/discount.go`) and the promotion trial
(`internal/workflows/cart/trial.go`), whose snapshot is rebuilt from an order
and never carried a bag. Every other round already took the price context
(ADR 0403).

Nothing else read it: the checkout's snapshot of the cart has no metadata field
(`internal/workflows/checkout/plan.go`), so the order keeps no copy; no plugin
and no event carries it.

## 3. Who writes a promotion rule

`AddPromotionRule` (`internal/modules/promotion/service/rule.go`) is the only
write, behind `POST /admin/v1/promotions/{id}/rules`
(`internal/modules/promotion/api/admin.go`) and the panel's surface
(`internal/modules/promotion/admin_surface.go`), whose two forms
(`internal/adminui/promotion.go`) write `category_tree_ids` and
`customer_group_id` alone. Rules are inserted and soft deleted; no write takes
inline rules, copies or rewrites one.

## 4. Who consumed a `cart.` promotion rule

Tests alone: the e2e test of ADR 0111's feature in
`internal/e2e/coupon_test.go`, five tests in
`internal/workflows/cart/rule_context_test.go`, and the producer's test in
`internal/modules/cart/service/interop_promotion_test.go`. The storefront at
`~/Documents/gobit-storefront` sends no cart metadata, and neither does any
program under `examples/`.

## 5. Reproduction

On the tree at `d46741c2`, with the tests of this record added:

- `TestTheCartsBagReachesNoRound` failed: both discount requests carried
  `cart.brand`, `cart.customer_group_id` and `cart.region_id`.
- `TestATrialLeavesOutWhatItMustNotPrice` failed: the assumptions listed
  `no_cart_metadata`.
- `TestAPromotionRuleCannotNameTheCartsBag` failed: a context, a target and a
  buy rule on `cart.brand` were each written.
- `TestARuleOnTheCartsBagIsRefused` failed: 201 for 422.
- `TestTheRuleWriteDescribesTheReservedAttribute` failed: the rule write
  described no 422 of its own.
- `TestARuleOnTheCartsBagSaysItHoldsOnNoCart` failed: the page carried no mark.
- `TestACartsMetadataMeetsNoPromotion`, on the production wiring: "a new rule
  on the bag is refused" answered 201 for 422; "an old rule meets no cart" gave
  the cart a `discount_total` of 2500 for 0; "the channel carries the brand"
  passed, as the witness that the replacement already worked.

## 6. What an old rule does

`internal/core/condition/condition.go` matches no operator against an absent
attribute, `ne` and `nin` included, so a promotion with a context rule on
`cart.` applies to no cart once the context stops carrying the bag.
`POST /admin/v1/promotions/compute` takes its context from the request, so it
still matches such a rule when asked with the attribute; the e2e test asserts
it. Removing the rule makes the promotion apply wherever its other rules hold,
which is why the record gives the order: the condition moves first.
