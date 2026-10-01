# ADR 0329 — The panel renames and re-ranks a customer group

**Summary:** A customer group's row on the Customer groups screen renames and
re-ranks it under `customer:write`, from the name and rank the row was drawn
with; the customer module writes both only while they are still the group's.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The panel writes a customer group with its rank (ADR 0323), and the rank
decides which of a customer's groups prices them (ADR 0049). A group named
in a hurry, or a rank set before the next group arrived, could be changed
only through the admin API's partial update, which writes whatever it is
sent over whatever the group has become.

## Decision

The customer module revises a group's name and rank together, writing them
only while the group still has the name and the rank the caller read, and
refuses with `customer_group_moved` otherwise. Each row of the Customer
groups screen offers the form that does it, carrying what the row was drawn
with, as a price and a count do (ADR 0280).

## Consequences

- A merchant renames a group and moves it up or down the ranking where the
  groups are listed.
- Two operators revising one group at once write it once; the second is told
  what the group is now and to draw the list again, and the row comes back
  with what they typed.
- Price rules and promotion rules name a group by its id, so a rename leaves
  every rule on the group.
- A re-rank decides a customer's price wherever their groups are read in
  rank order (ADR 0049), from the next read on.
- The admin API's partial update stays as it is; an integrator who needs the
  same guard reads the group and compares, as before.

## Rejected

- Sending the form through the partial update: it would write over a
  revision made meanwhile, which is what a drawn value exists to prevent.
- A version column on the group: the name and the rank are the whole of what
  the form writes, and comparing them needs no schema change.
- One form per field: a rename and a re-rank are one decision about the
  group, and one form compares both against what was read.
