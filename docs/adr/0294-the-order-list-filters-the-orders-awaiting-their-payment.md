# ADR 0294 — The order list filters the orders awaiting their payment

**Summary:** An order awaits its payment when it is not canceled and what was
collected for it falls short of its total less its credits. The order module
filters its listing by that rule, and the admin API and the panel's order list
offer the filter.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

ADR 0284 placed orders that owe an offline method's money, and ADR 0287 let the
panel record that money from the order's page. An operator still had no way to
see which orders were waiting for it short of opening each one. The order's
summary keeps what was collected and what was refunded, and the admin API
reports an `outstanding` amount under `order:read`. That amount adds refunds
back, so an order paid and then refunded for returned goods reads as owing its
whole total again.

## Decision

The order listing takes an `awaiting_payment` filter: true keeps the orders not
canceled whose collected total is below their total less their credits, and
false keeps the others. `GET /admin/v1/orders?awaiting_payment=true` and the
panel's order list, through a box, offer it.

## Consequences

- An operator finds the telephone and bank transfer orders still owed in one
  list, partly paid ones included: a gift card beside an unpaid transfer
  leaves the order awaiting the rest.
- An order paid and then refunded does not await its payment, and a credit
  lowers what is awaited, as it lowers what is owed.
- A completed order can still await its payment; cash on delivery is
  completed by the shop when it pleases.
- The filter is the order module's own record of the money, read under
  `order:read` as the summary already is. The payment's collection stays
  behind `payment:read` on the order page (ADR 0251).
- The rule is written twice, in the query and in the test double that mirrors
  it; a test against a real PostgreSQL holds the query, the count included.

## Rejected

- Filtering on `outstanding`: refunds would bring paid and returned orders back
  into the list.
- Pending orders with nothing collected: a split payment's transfer would leave
  the list the moment its gift card was captured.
- Asking the payment module for the orders with an awaited session: the order
  listing would read another module for every page, and the order already
  holds what it was paid.
