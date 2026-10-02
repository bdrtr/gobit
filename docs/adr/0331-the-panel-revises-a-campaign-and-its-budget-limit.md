# ADR 0331 — The panel revises a campaign and its budget limit

**Summary:** A campaign's row revises its name, description, window and
budget limit under `promotion:write`, from the terms the row was drawn with;
the promotion module writes them in one conditional statement only while
they are still the campaign's, keeping its identifier, its budget's unit
and its counter.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The panel writes a campaign (ADR 0319), and a promotion stops applying when
its campaign's budget is used up or its window closes. A merchant whose
sale sold faster than planned could raise the budget, or move the window,
only through the admin API's update, which replaces the campaign's whole
definition with what it is sent and lets two operators undo each other.

## Decision

The promotion module revises a campaign's name, description, window and
budget limit together, in one UPDATE that matches the terms the caller read
and a limit that fits the budget's type, and refuses with
`promotion_campaign_revised` when another writer changed them since. Each
row of the Campaigns screen offers the form that does it, carrying the
terms it was drawn with, its moments to the nanosecond and its limit in
minor units or uses.

## Consequences

- A merchant raises an exhausted budget, lowers one to stop a campaign
  early, or moves its window, where the campaigns are listed.
- Two operators revising one campaign at once write it once; the second is
  told what the campaign is now and to draw the list again, and the row
  comes back with what they typed.
- The counter stays the redemption flow's: a revision never writes
  `budget_used`, and a redemption racing it is judged against whichever
  limit the row holds when its own statement runs.
- A campaign with a budget keeps a limit and one without takes none, as on
  a new campaign; the form offers a limit only where the budget has one.
- An end typed as it was shown is sent as the moment drawn, as on a price
  list (ADR 0330).

## Rejected

- Revising the identifier: outside systems know the campaign by it, and a
  rename would orphan their references.
- Revising the budget's type or currency: the counter is held in that unit,
  and the admin API already guards changing it.
- Reading the campaign and writing it under a lock: one conditional
  statement makes the same decision with no history to record.
