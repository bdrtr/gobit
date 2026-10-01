# ADR 0323 — The panel writes and lists the customer groups

**Summary:** A Customer groups screen lists the groups newest first with their
rank, read through the `customer_group` entity under `customer:read`, and
writes one with its name and rank through `customer.admin` under
`customer:write`.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The panel limits a promotion to customer groups (ADR 0321) and puts a customer
into one (ADR 0322), but a group itself could be written only through the
admin API, so a merchant starting a wholesale tier left the panel for its
first step. The customer module's checks refused bad input partly in Turkish.

## Decision

The panel's Customer groups screen lists the groups a page at a time, newest
first, with their rank and the day they were written, and its form writes a
group with a name and a rank. The customer module's panel surface writes the
group through the module's service, which keeps a name unique among live
groups.

## Consequences

- A segment's whole path is in the panel: the group, its members, and the
  promotions limited to it.
- A blank rank is zero, the default every group starts at; any whole number is
  a rank, a negative one placing a group in front without renumbering the
  others (ADR 0049).
- A name another live group holds is refused by the module, which says so.
- The customer module's input checks are written in English now and have left
  the language ledger, so the screen's refusals read in the panel's language.
- A group is not renamed, re-ranked or deleted here; those stay on the admin
  API until a screen needs them.

## Rejected

- A screen of its own per group listing its members: an operator's question
  is about a customer, answered on the customer's page (ADR 0322).
- Reading the groups through the surface: the group entity already publishes
  them, and the panel's reads go through the read layer.
