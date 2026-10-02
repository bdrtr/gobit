# ADR 0361 — The order list narrows to a status

**Summary:** The order list narrows to one of the order module's statuses,
pending, completed, archived or canceled, through the order entity's
status filter, beside its other narrowings, and keeps the status across
its pages.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The order list narrows to the orders awaiting their payment, an
operator's, and one customer's (ADR 0294, ADR 0298, ADR 0358), but not to a
status: the canceled orders, or the completed ones still to be archived,
were found only by reading every page. The order entity filters by status.

## Decision

The order list's form offers the order module's statuses and asks the
order entity for the orders in the one chosen; every status is listed when
none is chosen or one the module does not have is asked for.

## Consequences

- An operator finds the canceled orders, or the completed ones to archive,
  on one list.
- A status narrows together with the other boxes and one customer.
- The statuses offered are the four the order module has; a new one is
  added here with it.

## Rejected

- Tabs per status, as the other lists have: the order list's narrowings
  are a form's boxes, and a tab would drop them on every press.
