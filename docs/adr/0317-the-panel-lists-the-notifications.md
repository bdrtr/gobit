# ADR 0317 — The panel lists the notifications

**Summary:** A Notifications screen lists the notification module's delivery
log, the failed deliveries first or one order's, and sends a failed order
confirmation again. It reads and writes through a new `notification.admin`
surface, under `notification:read` and `notification:write`.

- **Status:** Accepted
- **Date:** 2026-10-01
- **Amended by:** [0386](0386-a-completed-order-says-so-on-the-bus.md): the button is on a failed completion notice too, rebuilt from the order as the confirmation is

## Context

The notification module logs every delivery it attempts, and ADR 0243 and ADR
0245 let an operator send a failed order confirmation again, through the admin
API. A customer who says no confirmation arrived is the panel's operator's
question, and the panel had no screen for it: finding the delivery took a
filter on the API, and the resend another call.

## Decision

The notification module registers a panel surface that lists the deliveries
by status or by order, each marked when the operator may send it again, and
sends one again. The panel's Notifications screen lists the failed deliveries
when none is chosen, finds an order's by its id, and puts a resend button on
the deliveries the module would send again.

## Consequences

- An operator answers "did my confirmation go?" by the order's id, and sees
  the provider's reason on a failed delivery.
- The button is on a failed order confirmation, or one a dead attempt left
  pending (ADR 0245), and on nothing else: another module's message is sent
  again by the module that holds its content.
- A resend the provider refuses again is reported as its outcome, failed with
  the reason on its row, rather than as a failure of the screen.
- A resend returns to the list it was pressed on, the status tab or the
  order's, with its outcome in the address, so a reload repeats the sentence,
  not the resend; two operators pressing at once send it once, as the module
  already guarantees.
- The surface marks a delivery resendable by the module's own rule, so the
  screen holds no copy of it.

## Rejected

- Reading the log through a read provider: the read layer cannot tell a
  storefront from an operator, and the log is the module's private record.
- A button on every failed delivery, refused by the module for the other
  kinds: it would offer what cannot be done.
