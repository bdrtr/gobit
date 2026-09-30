# ADR 0271 — The panel acts on an order's after-sales records

**Summary:** The order page takes every act the API takes on an existing return,
claim, exchange or replacement, through an `order.admin` surface whose methods
are the API's own, under `order:write`.

- **Status:** Accepted
- **Date:** 2026-09-30

## Context

ADR 0270 put an order's after-sales records on its panel page, and the known
limit that replaced the old one said the panel could not act on them: receiving
a return, settling a claim, funding an exchange or dispatching a replacement was
still an `/admin/v1` call. The API's writes on a record either withdraw a
request through the service or go through the returns flow, because they move
stock, money or a parcel in modules the order module does not know. The panel
knows no module and reaches a module's writes through a narrow surface resolved
from the container (ADR 0011, ADR 0013).

## Decision

The order module registers `order.admin`, whose ten methods are the API's
after-sales writes on an existing record: the three withdrawals through the
service and the rest through the same flow instance the API holds. The order
page offers each record the acts its status allows, and one route,
`POST /admin/ui/orders/{id}/after-sales/{kind}/{record}/{act}` under
`order:write`, takes them and draws the page again with what happened.

## Consequences

- An operator receives a return at a location, refunds it, settles a refund
  claim, funds or refunds an exchange, dispatches a replacement and withdraws
  any of them from the order page.
- The panel refuses and fails where the API does, with the module's own
  sentence: a refusal (422) is drawn on the page to act again from, and a fault
  is the panel's error page.
- The page is drawn again rather than redirected to, because the units
  restocked, the parcel opened and a warning that needs a human are said once.
  A repeated form is answered by the record's status.
- An operator holding `order:write` without `order:read` is told what happened
  and reads nothing of the order (ADR 0260).
- An amount typed into a refund is read in the order currency's scale; empty
  means what the API's zero means.
- Opening a record and a claim's evidence stay in the API.

## Rejected

- One route per act: ten routes and ten scope entries for one privilege and one
  handler shape.
- Redirecting after an act: the outcome would need a store to survive the
  redirect.
- Resolving the returns flow from the panel: the panel would name a workflow
  and bypass the module that owns the record.
