# ADR 0289 — An unpaid offline order expires by its method's wait

**Summary:** A shop can give each offline method a wait in days. A job cancels,
through the shop's own cancel, the order whose session of that method is still
authorized past the wait in a payment that captured nothing. A method given no
wait never expires.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

ADR 0284 placed orders that owe an offline method's money and hold their stock.
Only the shop's hand ended one whose money never came (ADR 0285), and the known
limits said so. A bank transfer that has not arrived in a few days usually never
will, and its stock sits off the shelf meanwhile. Cash on delivery is paid at
the door after the parcel has left, so a wait counted from the placement would
cancel an order on its way. The jobs here carry out a moment someone set in
advance (ADR 0177, ADR 0214, ADR 0258) and decide nothing on their own.

## Decision

`PAYMENT_OFFLINE_WAIT_DAYS` gives named offline methods a wait of one to 365
days, and every other method none. The `offline-order-expiry` job reads, every
quarter hour, the sessions of those methods still authorized past their wait in
payments that captured nothing, and cancels each one's order through
`CancelPlacedOrder`.

## Consequences

- The canceled order's stock comes back (ADR 0285), and its session is closed
  when the payment module hears the cancel (ADR 0288).
- The wait counts from the session's opening, the checkout's moment. Money that
  arrives after the session is closed cannot be recorded against it; the shop
  returns it outside gobit.
- A payment that captured anything is never read. A gift card beside a transfer
  makes the order the shop's to decide, and its cancel would be refused anyway.
- An order the cancel refuses, completed or paid between the read and the
  cancel, is counted on the job's line and left. The read continues by key, so
  such an order does not hold back the ones after it.
- A wait for a method the installation does not offer, or outside one to 365
  days, stops the startup, in the configuration and in the payment service.
- The job's line counts the orders that are canceled, not the ones the run
  canceled: a second cancel is not an error, so an order whose session the
  payment module has not closed yet is counted again.
- No method has a wait by default, so an upgrade cancels nothing.

## Rejected

- One wait for every offline method: cash on delivery's orders would be canceled
  on their way.
- Sparing orders with a live parcel instead of a wait per method: the job would
  read three modules to guess what the shop says in one setting.
- Releasing the stock and leaving the order: the order would owe for goods it no
  longer holds.
