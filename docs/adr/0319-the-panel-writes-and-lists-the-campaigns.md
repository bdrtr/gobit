# ADR 0319 — The panel writes and lists the campaigns

**Summary:** A Campaigns screen lists the promotion module's campaigns with
their window and how much of their budget is used, and writes one, through
the `promotion.admin` surface under `promotion:read` and `promotion:write`.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

A campaign is the window and the budget its promotions share, and a promotion
stops applying when its campaign's window is closed or its budget is used up.
The panel writes coupons (ADR 0314) and shows a promotion's campaign (ADR
0313), but a campaign could be written and watched only through the admin
API, and the campaign service refused bad input in Turkish.

## Decision

The promotion module's panel surface lists the campaigns, a page at a time in
the order they were written, and writes one with its window and budget. The
panel's Campaigns screen prints each window in UTC and each budget's use in
uses or in its currency's decimals, and its form writes a campaign.

## Consequences

- An operator sees how close a campaign is to its cap before customers find
  its promotions no longer applying.
- The form reads its moments in UTC, as the product's schedule is read (ADR
  0178), and accepts a past start: that campaign is already running.
- A money budget is typed in its currency's decimals, or in minor units when
  the panel cannot read the currency's scale, as a price is.
- A taken identifier is refused by naming it rather than the constraint, and
  the campaign service and repository are now written in English.
- The list keeps the module's order, so the campaign just written is named in
  the address the form lands on rather than found at the top.
- Putting a promotion into a campaign stays on the admin API: the module
  replaces a promotion's whole definition to do it.

## Rejected

- Editing a campaign on the same screen: the module replaces the definition
  and freezes the budget's unit once it is used, so a form has to carry what
  it read; that is its own decision.
- Listing the campaigns newest first: it changes the admin API's order, which
  clients page through.
