# ADR 0272 — The panel opens an order's after-sales records

**Summary:** The order page opens a return naming the units that come back, a
claim, an exchange, and the replacement a claim or an exchange sends, through
the `order.admin` surface under `order:write`; the admin API's return takes
its lines as the storefront's does.

- **Status:** Accepted
- **Date:** 2026-09-30
- **Amends:** [0271](0271-the-panel-acts-on-an-orders-after-sales.md), which left opening a record to the API

## Context

ADR 0271 let the panel act on an existing after-sales record and left opening
one to `/admin/v1`. Opening one there turned out to be half a door: the admin
API's return took an amount, a reason and a note but no lines, while the
storefront's named the lines coming back, so a return an operator opened could
never be restocked when it arrived (D186). The service has taken a return's
lines since they were added; only the admin body was never given them.

## Decision

The admin API's return takes `lines`, each an order line, a quantity and its
part of the refund. The `order.admin` surface gains the four openings the API
has, each the service's own, and the order page opens a return with a quantity
box per line, a claim, an exchange, and a replacement for a requested claim of
the replace kind or a requested or funded exchange, under `order:write`
through `POST /admin/ui/orders/{id}/after-sales/{kind}`.

## Consequences

- An operator takes a return over the phone from the order page and it goes
  back to stock when it arrives, as a shopper's does.
- A line left empty or at zero is not named; a quantity that is not a whole
  number, and a replacement with nothing to settle, are refused on the page.
- The panel's replacement sends the order's own lines. Sending another variant
  (ADR 0145), a claim's evidence and a line's part of a return's refund stay in
  the API.
- The page is drawn again after an opening, as after an act, and says what was
  opened.

## Rejected

- A separate screen per kind: the forms need the order's lines and its records,
  which the order page already holds.
- Refusing a return with no lines in the admin API: a record whose lines are
  unknown is the service's own shape, and the storefront's refusal of one is its
  own rule.
