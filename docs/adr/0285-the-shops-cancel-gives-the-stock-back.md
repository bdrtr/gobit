# ADR 0285 — The shop's cancel gives the stock back

**Summary:** When the shop cancels an order the checkout placed, the same
transaction writes off every unit not yet returned or written off. Those units
come back to the shelf through the flow that puts written-off units back. The
saga's cancel of an order it is unwinding writes nothing off.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

The checkout's last step confirms the reservations, which deducts the stock, so
a placed order holds units that are off the shelf. A line written off puts its
units back through `order.line_canceled` and the flow of ADR 0134. Canceling the
whole order only stamped its status: it published nothing and wrote nothing
off. D70 named the hole in both cancels, and its fix closed only the partial
one (D195). An order with money on it cannot be canceled, so the hole was
reached only by an order that was placed and owed everything. ADR 0284 makes
that the ordinary end of a bank transfer nobody sends.

## Decision

`POST /admin/v1/orders/{id}/cancel` calls `CancelPlacedOrder`, which writes a
line cancellation and its event for each line's units not yet returned or
written off, in the cancel's transaction. `CancelOrder`, the checkout's
compensation for an order whose stock is still only reserved, keeps stamping the
status alone.

## Consequences

- A canceled order's units come back as a written-off line's do: what a live
  parcel holds stays out until the parcel is canceled, and a shipped unit never
  comes back.
- The order's timeline and line cancellations show what the cancel wrote off,
  under the cancel's reason or "order canceled" when it gave none.
- A gift card line is left as it is: it moved no stock, and a card is closed in
  the payment module.
- A second cancel writes nothing, and a refused cancel — a completed order, or
  one with money collected — rolls back what it began.
- An offline order's payment session stays authorized after the cancel. Nothing
  is held behind it, and `POST /admin/v1/payment-sessions/{id}/cancel` closes
  it.

## Rejected

- Writing off in `CancelOrder` too: the saga's cancel would record cancellations
  for units nobody deducted, and the restock flow would warn on every
  compensation that it found no sale.
- An `order.canceled` event for the restock flow to expand: the flow would have
  to compute per-line remainders the order module already holds, outside the
  lock that keeps them true.
