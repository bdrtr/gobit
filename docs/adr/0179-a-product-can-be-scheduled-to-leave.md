# ADR 0179 — A product can be scheduled to leave

**Summary:** A draft or a published product can be given a moment to be
archived, so a limited-time product arrives on one day and leaves on another
without anybody being there. The schedule becomes one resource with two moments,
and the same pass that publishes also archives.

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

ADR 0177 scheduled a draft's publication and left the other direction out. A
limited-time drop, a seasonal product and a sale that ends at midnight all need
a product taken off the storefront at a moment. Taking a product off meant an
operator archiving it by hand at that hour.

## Decision

A draft or a published product carries an optional `archive_at` beside
`publish_at`, later than it when both are set. `PUT
/admin/v1/products/{id}/schedule` replaces both moments — one left out is taken
off — and the minute's pass publishes what is due and then archives what is due.

## Consequences

The schedule is one resource. A body that sets only `archive_at` on a scheduled
draft takes its publication moment off. That is PUT's meaning, and the admin
answer shows what the product holds afterwards. Clearing both remains the
DELETE, and an empty body is refused rather than read as a DELETE.

Archiving is the same kind of change as publishing. The status changes at the
moment, the product gets the `product.updated` event archiving by hand gives,
and every visibility answer that reads the status follows. A draft whose two
moments both passed while the scheduler was down is published and then
archived in one pass. It gets the event of each, in that order.

A hand change spends what it overtakes. Publishing a draft spends its
publication moment and keeps its moment to leave. Archiving a product spends
both, in the statement that changes the status. Two constraints hold the pair:
a moment to leave only on a draft or a published product, and a moment to leave
after the moment to arrive.

The panel's edit form gains "Archive at (UTC)", checked with the publication
moment before anything is written. The storefront never learns when a product
will be gone, for the same reason it never learns when one arrives.

## Rejected

- **A second endpoint for the moment to leave.** Two resources for one
  schedule, and a product given both would need two writes to be consistent.
- **Moving a product back to draft instead of archiving it.** Draft means
  "not yet", archived means "no longer"; a product at the end of its time is
  the second.
- **A job of its own.** A second minute's pass over the same table, with the
  same lock, for the other half of the same schedule.
