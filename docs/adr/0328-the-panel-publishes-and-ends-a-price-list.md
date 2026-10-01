# ADR 0328 — The panel publishes and ends a price list

**Summary:** A price list's row offers its status's one move — publish a
draft, end an active list, reopen an ended one — which the pricing module
makes under the list's lock and only from the status the row was drawn in,
recording it in the list's history, under `pricing:write`.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The panel writes a price list as a draft (ADR 0326), and a draft's prices
apply nowhere. Publishing it, or ending a sale, took the admin API's update,
which replaces the list's whole definition: a form would have to send back
every field it read, and two operators would undo each other.

## Decision

The pricing module switches a list's status alone, from the status the
caller read to another, refusing when the list has moved since, and records
the switch in the list's history as an edit is recorded (ADR 0167). The
Price lists screen offers each row its one move and carries the status it
was drawn in, as the promotions screen does (ADR 0312).

## Consequences

- A merchant publishes a list, ends it when the season does, and reopens it
  next season, where the lists are read.
- Two operators pressing at once move a list once; the second is told the
  list moved and to draw the list again.
- A price computed for a past moment still finds the list's status as it was
  then, since the switch writes the history the ladder reads (ADR 0167).
- The list's other fields are written back as they stand under its lock, so
  an edit made through the API meanwhile is kept.

## Rejected

- Sending the whole list back through the replacing update: it would write
  fields the operator did not touch, and lose an edit made meanwhile.
- A narrow UPDATE without the history: the ladder would price the past with
  a status the list never had at that moment.
