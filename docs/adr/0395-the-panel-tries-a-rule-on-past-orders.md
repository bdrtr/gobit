# ADR 0395 — The panel tries a rule on past orders

**Summary:** A promotion's page, the Price lists screen and the Taxes screen
offer their subject's trial on a past period's orders, drawn from the report
its module's admin endpoint answers with, to an operator who may read the orders.

- **Status:** Accepted
- **Date:** 2026-10-05

## Context

A promotion, a price list and a tax rate can each be tried on a period's
orders through the admin API (ADR 0176, ADR 0220, ADR 0387), and the panel
offered none of the three, so an operator who keeps a coupon, a list or a rate
on its screen left the panel to learn what it would have done. The panel
imports no module and resolves each module's surface by name (ADR 0004), and
ADR 0378 made `tax.admin` carry only a rate's correction.

## Decision

`promotion.admin`, `pricing.admin` and `tax.admin` each answer the trial their
module's endpoint runs, its report as JSON after the endpoint's own refusals,
and the panel draws it at `/admin/ui/promotions/{id}/trial`,
`/admin/ui/price-lists/{id}/trial` and `/admin/ui/taxes/rates/{id}/trial`. Each
screen asks for its module's read privilege and `order:read`, takes the period
as two days, the last one whole and ending now at the latest, and writes
nothing.

## Consequences

- An operator tries a coupon from its page, a list from its row and a rate
  from its row, and reads per currency what it would have changed, the orders
  it moves most, each linked to its page, and what the trial assumed.
- The panel reads each report as its module writes it, unknown fields
  refused, so a field a module renames fails the screen instead of drawing a
  zero; each report shape is drawn from its surface's own JSON and held end to
  end.
- A tax rate is tried at a value typed as a percent and with one rule added; a
  default rate is tried at a value only, since it takes no rule. Dropping a
  rule stays the API's.
- A province's rate, which no cart reaches, and a rate under an external
  provider, whose table cannot be amended, offer no trial. A rule on a stacked
  rate is refused on the screen with the module's 409, because the tax region
  entity does not publish what a rate stands on.
- The period is bounded in the days the form offers, at most the flows' 93,
  and refused at the form beyond them. The flows bound elapsed time, so a
  period of 93 days across the autumn change of the clock starts an hour late.
- A refusal is drawn on the screen with its reason and the form as typed; a
  failure the operator cannot act on is not shown.
- The three trial interfaces are resolved like every panel surface and pinned
  against their producers in `internal/arch`.

## Rejected

- A link to the admin endpoint: it answers JSON and takes moments, which the
  operator would read raw and type by hand.
- The panel asking the cart flows itself: each module's surface keeps the
  refusals its endpoint makes before an order is read.
- A period of moments: the operator reads trade by the day, as the sales
  report does.
- Offering the trial without `order:read`: the report is orders.
- A rule form on every rate: on a default rate it is refused before the trial
  runs, so the form would offer what always fails.
