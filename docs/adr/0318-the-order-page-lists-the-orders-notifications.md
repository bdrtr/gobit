# ADR 0318 — The order page lists the order's notifications

**Summary:** The order page lists what the notification module sent for the
order, read through `notification.admin` only for an operator who also holds
`notification:read`, and links to the Notifications screen on the order.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

ADR 0317 put the delivery log on a screen of its own, found by the order's id.
The operator who asks whether a customer was told is usually already on the
order's page, which shows the payment and the parcels under their modules'
privileges (ADR 0251) and said nothing of the order's notifications.

## Decision

The order page lists the order's deliveries, newest first with their status
and the provider's reason, through the notification module's panel surface for
an operator holding `notification:read`. It links to the Notifications screen
listing the order's deliveries, and the resend stays on that screen.

## Consequences

- An operator reads whether the confirmation went where the order is, without
  carrying its id to another screen.
- An operator without `notification:read` is told the privilege the section
  needs, as for the payment and the parcels, and the surface is not asked.
- A read that fails leaves the order on screen and says the notifications
  could not be read; an installation without the surface has no section.
- The page reads at most ten deliveries and says when the order has more; the
  link lists them all.
- A resend has one button and one way back, to the list it was pressed on
  (ADR 0317).

## Rejected

- A resend button on the order page: it would need a second way back, and a
  second place to say what a refused resend means.
- An order link in the read layer: the notification module publishes no read
  provider, and the log is its private record (ADR 0317).
