# ADR 0330 — The panel revises a price list's title and window

**Summary:** A price list's row revises its title, description and window
under `pricing:write`, from the terms the row was drawn with; the pricing
module writes them under the list's lock only while they are still the
list's, and records the revision in the list's history.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The panel writes a price list (ADR 0326) and moves its status (ADR 0328),
but a sale written with the wrong dates, or a list that needs a clearer
title, could be corrected only through the admin API's update, which
replaces the whole list with what it is sent. A window is what decides when
a list's prices apply, and the ladder reads it from the list's history
(ADR 0167).

## Decision

The pricing module revises a list's title, description and window
together, under the list's lock and only while they are the ones the
caller read, keeping its type, status and metadata and recording the
revision in its history, and refuses with `pricing_price_list_moved`
otherwise. Each row of the Price lists screen offers the form that does it,
carrying the terms it was drawn with, its moments to the nanosecond.

## Consequences

- A merchant moves a sale's dates, or opens an end, where the lists are
  read, without writing the list again and putting every price back on it.
- Two operators revising one list at once write it once; the second is told
  what the list is now and to draw the list again, and the row comes back
  with what they typed.
- The picker shows a moment to the minute; an end typed as it was shown is
  sent as the moment drawn, so renaming a list written through the API with
  seconds in its window leaves the window where it was.
- A price computed for a past moment finds the window the list had then,
  since the revision writes the history the ladder reads.
- The type stays as written: a sale and an override are chosen in a
  different order, and changing one into the other is a new list.

## Rejected

- Sending the form through the replacing update: it writes the type, the
  status and the metadata back as the form last saw them, losing a status
  switched meanwhile.
- Comparing the list's `updated_at`: a status switch would refuse a rename
  that does not touch it.
- Carrying the moments to the minute the picker shows: a window written
  with seconds through the API would never match, and its row could never
  be revised.
