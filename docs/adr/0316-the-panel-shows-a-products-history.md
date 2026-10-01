# ADR 0316 — The panel shows a product's history

**Summary:** A product's history screen lists its revisions newest first and
restores an older one at the version the page was read at. Both go through
the product module's admin surface, under `product:read` and `product:write`.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

ADR 0221 records a revision for every write that changes a product's admin
view, and lets the admin API list them and restore one; ADR 0222 refuses a
write made at a version the product has moved past. The panel offered
neither: an operator who wanted yesterday's title back read the API, and the
revisions recorded for the panel's own edits were invisible in the panel.

## Decision

The product module's admin surface lists a product's revisions without their
snapshots and restores one at a stated version, refused as an edit is when
the product was written since. The panel's history screen, linked from the
product page, shows each revision's version, moment, changed fields and
request, and gives every revision but the current one a restore form.

## Consequences

- An operator brings back an earlier title, description, handle or category
  set in one press, and the restore is itself a revision that can be undone.
- Two operators restoring at once cannot both succeed: the second is refused
  on the history with the module's reason, and the page shows the new state.
- The page says what a restore left out because it was removed since, from
  the address it lands on, so a reload repeats the sentence, not the restore.
- A restore writes what the module's restore writes: the status, the
  schedule, the variants, the options and the images stay as they are.
- The request id is shown as recorded; finding who made it is the audit
  log's, which the panel does not read.

## Rejected

- Showing a revision's snapshot and a field-by-field difference: the changed
  fields name what moved, and a snapshot reader would duplicate the admin
  API's for a rare need.
- Restoring without the version read: a restore overwrites the whole
  descriptive content, the write ADR 0222 exists for.
