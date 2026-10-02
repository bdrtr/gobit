# ADR 0338 — The panel changes how much a discount gives

**Summary:** A promotion's page changes its discount's value under
`promotion:write` through `promotion.admin`, from the type and the value the
page was drawn with: a percentage typed as a percent, a fixed amount in its
currency's decimals.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The panel writes a coupon with its discount (ADR 0314), switches it, limits
it to categories and groups (ADR 0315, ADR 0321) and puts it into a
campaign (ADR 0320), but raising a sale from ten per cent to fifteen took
the admin API, whose method write replaces every field of the discount
with what it is sent.

## Decision

The promotion module changes a discount's value alone, in one UPDATE that
matches the type and the value the caller read, after checking the value as
a new discount of that type is checked, and refuses with
`promotion_discount_revised` otherwise. The promotion's page offers the
form to an operator who may write promotions, carrying the type and the
value it was drawn with.

## Consequences

- A merchant changes how much a promotion gives where it reads the
  promotion, its target, allocation and quantities untouched.
- Two operators changing one discount at once write once; the second is
  told what the discount is now and gets back what they typed.
- A discount whose type changed since it was read is refused, so a value
  meant as a percent is never written as an amount, or the other way.
- What carts already computed is recomputed at the next computation, as any
  change to a promotion is.
- Changing the type, the target or the quantities stays on the admin API.

## Rejected

- Writing through the API's method write: it replaces the discount and
  writes back the fields the form did not show.
- Typing basis points: the shop thinks in per cent, and a factor of a
  hundred is the slip the form would invite.
