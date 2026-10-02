# ADR 0339 — The order page cancels an unpaid order

**Summary:** A pending order's page cancels it under `order:write` through
the order module's surface and the service the API's cancel calls, so its
units are written off and their stock comes back; an order with money
collected is refused and settled through its after-sales records.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

An order placed on an offline method owes its total, and a customer who
never transfers is the ordinary end of a bank transfer (ADR 0284). The API
cancels such an order and writes off its units so their stock comes back
(ADR 0285), but the panel could not, so the operator who saw the transfer
never came left the panel to free the shelf.

## Decision

The order module's surface cancels an order through the service the API's
cancel calls, and the order's page offers it, with a reason kept with the
order, to an operator who may write orders while the order is pending.

## Consequences

- An unpaid order is canceled where it is read, and its units come back to
  the shelf through the write-offs the cancel records.
- An order with money collected is refused with the module's reason, drawn
  on its page; its money is returned through a return or a refund, which
  restock what came back.
- A second cancel writes nothing, as the API's does, and the page stops
  offering it once the order is canceled.
- A completed order is refused, and the page does not offer it the cancel.

## Rejected

- Offering the cancel in every status: a completed or canceled order's
  operator would be offered a button that can only fail. Whether a pending
  order's money was collected is the module's to judge, so a paid pending
  order is offered the cancel and refused with the reason.
- A cancel that refunds collected money in the same step: what comes back
  and what is refunded are the after-sales records' to decide.
