# ADR 0340 — The order page completes and archives an order

**Summary:** A pending order's page marks it completed, and a completed
order's page takes it into the archive, under `order:write` through the
order module's surface and the transitions the API calls.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The panel ships an order (ADR 0324), invoices it (ADR 0335) and cancels an
unpaid one (ADR 0339), but closing an order took the admin API: an order
delivered and paid stayed pending in the panel's lists, and a closed one
stayed in the daily lists for want of the archive.

## Decision

The order module's surface completes and archives an order through the
service's transitions, and the order's page offers the one its status
takes: a pending order is marked completed, a completed one archived.

## Consequences

- An order is closed where it is read, and leaves the daily lists when it
  is archived, its completion's moment kept.
- A second press is refused by the module, completion being a forward step
  and not a compensation, and the page then offers the next move.
- Completing an order does not check that it was paid or shipped, as the
  API's does not; whether an order is done is the shop's to say.
- A canceled or archived order is offered neither move.

## Rejected

- Completing an order only when it is paid and shipped: the module does
  not hold that rule, and a panel that held it alone would disagree with
  the API about which orders can be closed.
