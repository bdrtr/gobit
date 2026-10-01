# ADR 0320 — The panel puts a promotion into a campaign

**Summary:** A promotion's page offers the live campaigns and puts the
promotion into the chosen one, or out of any, through a narrow write that
changes the campaign alone and only from the campaign the page was read in.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

ADR 0319 put the campaigns on a screen and left putting a promotion into one
on the admin API, where the module does it by replacing the promotion's whole
definition: a form would have to send back every field it read, and would undo
an edit made meanwhile. A coupon written in the panel (ADR 0314) could not be
placed under a campaign's window and budget without leaving it.

## Decision

The promotion module sets a promotion's campaign alone, from the campaign the
caller read to a live one or to none, and refuses when the promotion moved
since or the campaign is not live. The promotion's page offers the first
hundred campaigns under `promotion:write` and carries the campaign the
promotion names.

## Consequences

- A coupon is put under a campaign's window and budget where it was written,
  and taken out the same way.
- Two operators choosing at once do not undo each other: the second is told
  the promotion moved and to draw the page again, as a status switch is (ADR
  0312).
- A soft-deleted campaign is refused, since a promotion in it would be skipped
  by the computation without anybody choosing that.
- A promotion whose campaign was deleted under it names that campaign on its
  page, which drew it as in none (D205), and is moved out from it.
- The page offers at most a hundred campaigns, the module's page ceiling, and
  says so when there are more; the admin API places a promotion in any.
- A campaign list that cannot be read leaves the page standing without the
  form.

## Rejected

- Sending the whole promotion back through the replacing update: it would
  write fields the operator did not touch, and lose an edit made meanwhile.
- Choosing the campaign on the campaigns' screen: a campaign holds many
  promotions, and the choice belongs to the promotion's page, where its rules
  and discount are read.
