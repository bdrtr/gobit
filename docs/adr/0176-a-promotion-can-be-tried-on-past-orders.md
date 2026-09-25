# ADR 0176 — A promotion can be tried on past orders

**Summary:** An operator can ask what an unpublished promotion would have done
to the orders of a period, and gets each order it would have discounted with
what it would have added. Nothing is written, and what the trial had to assume
is published with the answer.

- **Status:** Accepted
- **Date:** 2026-09-25

Measurement: [measurements/0176](../measurements/0176-a-campaign-before-it-runs.md)

## Context

A merchant publishing a promotion learns what it costs by running it. The engine
could price a cart and say why a promotion did not apply (ADR 0110). It could
not price a promotion that was not active, and nothing could hand it past
purchases. The feature list's C2 row asks for that rehearsal.

Three facts made it answerable. Orders keep what each line was charged and
discounted. Discounts add up rather than compound and are capped at the line.
And the checkout redeems every promotion it applied under the cart's id.

## Decision

`GET /admin/v1/promotions/{id}/trial?from=&to=` rebuilds every order placed in
the period and not canceled as the purchase it was. It prices the promotion
alone against each order as if it were active, automatic and free of its limit
and campaign, and reports what it would have added to the discount each order
got.

## Consequences

The purchase is built by the cart flow's own request builder and priced by the
engine's own elimination and arithmetic, so a rule sees an order as it saw the
cart. The promotion changes, the rules do not: its method, currency, context
rules and target rules still decide.

What it adds to a line is min(actual + trial, subtotal) − actual. That holds in
any order of application, so no other promotion is recomputed and today's
state of them does not leak into the answer. An order the promotion itself was
redeemed on is counted apart and not priced again.

What an order did not keep is read as it is today — the products' categories,
tags and collection, and the customer's groups. The cart's own metadata is gone,
so a rule on a `cart.` attribute matches no order. The report lists every
assumption beside the figures.

The period is at most 93 days and 5000 orders, and has to be in the past; each
is refused rather than answered in part. The endpoint needs `order:read` beside
`promotion:read`, because its answer is orders.

A promotion with no method, or whose mechanic and method disagree, is refused
once with 409, not reported as skipped on every order.

The new prose went into new files. The promotion module is still on the
language ledger, and the one test file the endpoint table forced open was
translated whole.

## Rejected

- **Replaying the order's promotions with the new one added.** Needs every
  promotion as it stood then; the cap makes it unnecessary.
- **Reading carts instead of orders.** A cart is overwritten in place and most
  never become an order (C8).
- **A POST with a body, like `compute`.** Nothing is sent but a period, and the
  question is a read.
- **Cutting a period at 5000 orders.** A report over the first N would read as
  a report over the period.
