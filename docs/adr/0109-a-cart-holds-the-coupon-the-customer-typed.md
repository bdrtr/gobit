# ADR 0109 — A cart holds the coupon the customer typed, and the order spends it

**Summary:** A cart can hold coupon codes; the flow refuses a code the promotion
module cannot use, carries the rest into the discount round, and the checkout
saga redeems what applied. It costs a table, a saga step, and a checkout in
flight across the upgrade that cannot be recovered.

- **Status:** Accepted
- **Date:** 2026-09-10

## Context

The promotion engine has taken coupon codes since it was built. Nothing sent it
any. The cart had nowhere to keep a code, so the discount request's `codes` array
was always empty and only AUTOMATIC promotions reached a cart: a merchant could
publish a coupon and watch every customer who typed it get nothing.

The other half was quieter. `RedeemPromotion` is what moves a usage counter and a
campaign's budget, and **nothing in this repository ever called it.** A coupon
limited to one use could not be limited at all, because nothing counted.

The cart workflow's package comment named the three points a coupon would be
wired into and left them empty. This record fills all three.

## Decision

`cart_promotion_code` holds the codes a cart carries. `ApplyPromotionCode` asks
the promotion module whether a code is usable, writes it only then, and reprices
the cart; the codes travel into the round on the cart's snapshot.

`redeem_promotions` becomes the saga's second step, keyed on the CART id, and
releases what it took when the checkout rolls back.

## Consequences

The code is checked BEFORE it is written. The reverse order leaves a cart holding
an unusable code for as long as the round takes and has to unwrite it afterwards;
the failure of that unwrite leaves a coupon nothing will honor.

A coupon that discounts NOTHING is still applied. A valid code whose target
matches no line is not invalid, only useless today, and it may start working when
another item is added — the promotion module's own rule, not a second one.

The coupons are spent before the order is opened. A promotion whose last use was
taken while the shopper was paying has to refuse the checkout, and refusing it
after an order exists means canceling one that should never have been placed.

**A checkout half finished at the moment of the upgrade cannot be recovered.**
The engine matches step names against the record, and a five-step record
disagrees with a six-step definition; the operator gets "manual intervention".

The cart restates two of the promotion module's limits — twenty codes and
sixty-four characters — because it cannot import them. Drift is safe in one
direction only, and this is that direction: a code this module accepts and that
one cannot use comes back unmatched. What binds the numbers is an end-to-end test
that fills a cart to the cart's ceiling and prices it.

The code is stored ASCII-only, narrower than the promotion module's own alphabet
and deliberately a different rule. The column asserts `code = upper(code)`, and
`upper()` folds what the cluster's CTYPE knows; ASCII folds identically
everywhere, so Go and the database cannot disagree.

ADR 0107 said only the LINES move on a merge. The codes move too, as a union.

## Rejected

**Passing the codes to `CalculateTotals`.** The flow is entered from three
places; a code given to one of them makes the discount appear and disappear
depending on which ran last.

**Storing which promotions applied on the cart.** The breakdown travels with the
totals the checkout computes and lands on the plan, which the recovery path
replays. A stored copy would be a second source of truth that can go stale.

**Recomputing the discount at redemption time.** It books a figure into a
campaign's budget that nobody was shown.

**Soft-deleting a removed code.** The row is a binding, and it should leave
nothing behind.
