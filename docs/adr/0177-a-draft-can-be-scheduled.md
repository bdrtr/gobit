# ADR 0177 — A draft can be scheduled

**Summary:** An operator can give a draft product a moment, and a job publishes
it then, within a minute, with the event a publication by hand produces. The
moment is published on the admin surface alone.

- **Status:** Accepted
- **Date:** 2026-09-25

Measurement: [measurements/0177](../measurements/0177-a-launch-at-nine.md)

## Context

A product is a draft, published or archived, and a launch at nine meant an
operator publishing it at nine. The feature list's B2.5 row asks for a
scheduled publication.

Every other window in the repository is judged when it is read — a campaign's
dates by the computation, a price list's by the ladder — and the job package
says why that is usually better than a job. A product is the exception. Its
visibility is decided by its status in the product service's reads and in the
search plugin, which learns from events. A moment judged at read time would
have to be taught to each of them, and none would hear it arrive.

## Decision

A draft carries an optional `publish_at`, set and taken off through `PUT` and
`DELETE /admin/v1/products/{id}/schedule`, and a job publishes every due draft
once a minute through one locked statement. The status stays the only thing a
reader asks, and the job's publication emits the same `product.updated` event
as one by hand.

## Consequences

Precision is the job's interval: a draft for 09:00 goes live between 09:00 and
09:01. A launch larger than one pass's 500 finishes over the following
minutes, earliest moment first.

Only a draft may carry a moment, and a CHECK constraint holds it. The update
that changes a status clears the moment in the same statement, so publishing
or archiving a scheduled draft by hand cannot trip the constraint or publish
it twice. A moment has to be in the future; publishing now is a status change.

The statement chooses and locks its rows with `SKIP LOCKED`. A draft someone is
editing is left for the next pass, and two passes cannot publish one product
twice. A draft deleted or archived before its moment is not published.

It is the first job whose write a shopper sees. It stays on the permitted side
of ADR 0017, because it does what an operator scheduled at the moment they
named and undoes nothing.

The moment is not in the product's own JSON. The storefront answers with a type
that embeds the product, and `encoding/json` does not let an outer field tagged
`-` hide an embedded one, so the admin surface adds the field through a wrapper
of its own. The storefront never learns an unannounced product's launch date.

Unscheduling is its own `DELETE` because the product PATCH cannot set a field
back to empty. There is no scheduled unpublication yet, and the admin panel's
product form does not show the moment yet.

## Rejected

- **Judging the moment at read time.** Every visibility decision and the search
  index would learn it, and no event would mark the launch.
- **A `scheduled` status.** A new word in front of every reader, for a product
  that is a draft until the moment.
- **Accepting a past moment as "publish now".** It reads like a date nobody
  meant; the status change already says it.
