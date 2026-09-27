# ADR 0212 — A sweep issues the cards a lost delivery did not

**Summary:** A scheduled job issues, every five minutes, the gift cards that the
paid, uncanceled orders of the last week sold and nothing issued, through the
same door as the capture. Neither way issues for a canceled order.

- **Status:** Accepted
- **Date:** 2026-09-27
- **Amends:** [0210](0210-a-sold-gift-card-is-issued-when-its-order-is-paid.md), whose cards were issued only by the capture's delivery

Measurement: [measurements/0212](../measurements/0212-a-delivery-that-was-lost.md)

## Context

ADR 0210 issues a sold card when payment.captured reaches the gift card sale
flow. A handler's error is logged and not delivered again, the in-memory bus
runs handlers after the publisher returns, and a process that stops while a
handler runs loses the event; an order whose flow failed before its cards were
made then had none. A card is named by its sale and issued once, so a second way
in cannot make one twice. ADR 0017 refuses scheduled compensations; the outbox
relay and the scheduled publisher write because they do what was already
promised.

## Decision

A `gift-card-sweep` job runs every five minutes, reads the gift card lines of
the orders placed in the last seven days, asks the payment module which of their
sales made a card, and hands each order missing one to the flow's door. That
door issues only for an order whose collection is captured in full and that is
not canceled, for the sweep and the capture alike.

## Consequences

A lost delivery is made good within five minutes, and the code is mailed by
whichever of the two ways made the card. A card whose mail failed is not helped:
the card exists, and the operator still replaces its code.

A pass is one windowed read of the order lines, one read of the payment module
per five hundred sales, and nothing more for an order whose cards exist. The
lines are read through a new `is_giftcard` filter on the read layer's
`order_line_item`, served by a partial index (order migration 000029).

A capture delivered late, or a sweep, after the order was canceled issues
nothing; an order canceled after its cards were made still leaves them
spendable.

An order not captured in full is counted as waiting in the job's line and left
alone, and an order older than a week is not looked at: a card no run could
issue in a week of runs is a fault the logs have reported every five minutes.

The flow gains a constructor that does not subscribe, `SweeperFromContainer`,
from which the composition root builds the job. A mutation that built the job
and discarded it left the job registration gate green (D147); the gate now
counts only a job handed to the registry.

## Rejected

- **Redelivering a failed handler's event.** It changes the bus's contract for
  every subscriber, and it would not bring back an event a stopped process held.
- **A sweep over every order ever placed.** Its pass grows with the shop's
  history for the few orders a delivery lost.
- **Issuing each unit on every pass and letting the sale's name refuse it.** A
  transaction per card, every five minutes, for cards that exist.
- **Keeping where the last pass ended.** The runner hands a job no state between
  runs, and a window read again is cheap.
