# ADR 0314 — The panel writes a coupon

**Summary:** The Promotions screen writes a draft coupon with its discount in
one form, under `promotion:write`. The promotion module writes the coupon and
its discount in one transaction, and the operator lands on its page.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

ADR 0311 to 0313 let an operator list, publish, pause and read promotions in
the panel, and none of them let one be written: a coupon took two admin API
calls, one for the promotion and one for its discount. The two calls are two
transactions, so a discount refused after the promotion was written left a
coupon that gave nothing, with its code taken. A shop's most common
promotion is a code worth a percentage or an amount off.

## Decision

The promotion module writes a draft coupon and its discount after validating
both, in one transaction, and the `promotion.admin` surface offers it with the
discount in basis points or minor units. The Promotions screen's form takes
the code, the percentage or the amount with its currency, what it applies to,
how it is spread and an optional usage limit, and opens the coupon's page.

## Consequences

- A coupon is written whole or not at all: a discount the database refuses
  takes the coupon with it, and its code stays free.
- A percentage is typed as one, with at most two decimals; an amount in its
  currency's decimals, or in minor units when the panel cannot read the
  currency's scale, as a variant's price is (ADR 0309).
- A code already taken is refused as "a promotion with the code … exists",
  not with the constraint's name, and the form comes back as typed.
- Every coupon starts as a draft, and the list's publish move (ADR 0312) puts
  it on sale; nothing written in the form reaches a shopper until then.
- The form writes standard coupons only. An automatic promotion, a buy-get
  promotion, rules and campaigns stay with the admin API.
- The module's validation messages reach the operator as they are, so the
  two files that carried them in Turkish were translated.

## Rejected

- Two surface calls from the panel: the half-written coupon the admin API
  allows would be the panel's to leave behind.
- Publishing from the form: a typo in the amount would be on sale at once.
- Rules in the same form: the rules have their own vocabulary, and a coupon
  without one applies to every cart that brings its code, the common case.
