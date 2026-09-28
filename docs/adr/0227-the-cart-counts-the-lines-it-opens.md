# ADR 0227 — The cart counts the lines it opens

**Summary:** The cart module holds the ceiling of 100 lines and asks it where it
decides a line is new, on an add and on a merge alike, under the cart's lock.

- **Status:** Accepted
- **Date:** 2026-09-28

Measurement: [measurements/0227](../measurements/0227-a-line-past-the-ceiling.md)

## Context

The cart workflow held the ceiling and asked a snapshot, taken outside the
cart's lock, whether the added variant was already in the cart. Since ADR 0223 a
line is a variant and its properties, so the same variant with new properties
opened a line past the ceiling, and the merge opened lines with no ceiling at all
(D154). Every write reprices every line of a cart in one request to pricing,
which refuses more than 1,000: a cart grown past that could never be priced
again, and so never bought.

## Decision

The ceiling moves into the cart service, which asks it in the one function that
creates a line, called by the add and by the merge, against the living lines it
counts under the cart's lock. The workflow no longer asks it, and a merge that
would open lines past it is refused whole.

## Consequences

The ceiling asks the question a line's identity answers, a variant and its
trimmed properties, because the same service call decides both: a full cart
raises a line whose properties it holds and refuses a new one, from an add or a
merge. Two additions racing on a cart one line below the ceiling open one line
between them, where the snapshot could let several through. A removed line frees
its place.

The refusal keeps the code the workflow answered,
`cart_workflow_line_limit_reached`, and its class, so a client handling it
changes nothing; a refused merge answers the same code and moves no line. A
refused add has read the catalog and pricing before the cart refuses it, since
the workflow no longer knows the ceiling. A test holds every call that creates a
line to the ceiling's function, so a third path that opens lines cannot be
written past it the way the merge was.

A cart opened before the ceiling with more lines keeps them, is priced and can
be bought: the totals round and the quantity writes still ask no line count.

## Rejected

- **The workflow's check keyed on the variant and its properties.** The workflow
  would restate the cart's reading of properties, and the merge would still be
  outside it.
- **A merge that carries every line past the ceiling.** The merged cart would
  cost what the ceiling exists to bound, and merges repeated could pass pricing's
  ceiling.
- **A count inside the insert statement.** The service already holds the cart's
  lock where it decides a line is new, and a second place would count.
- **A code of the cart module's own for the refusal.** Clients handle the code
  the workflow answered.
