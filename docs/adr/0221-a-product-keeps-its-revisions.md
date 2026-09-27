# ADR 0221 — A product keeps its revisions

**Summary:** Every write to a product's content records its admin view as a
numbered revision, which the admin surface lists, reads and writes back.

- **Status:** Accepted
- **Date:** 2026-09-28

Measurement: [measurements/0221](../measurements/0221-what-a-product-was.md)

## Context

A product's own fields, variants, options, images, tags, categories and
attribute values were overwritten in place, and nothing said what a product was
before an edit. The admin audit log records the request, not what it changed
(ADR 0037); two operators saving one product overwrite each other (ADR 0013);
pricing kept its history (ADR 0167) and the catalog did not. Seventeen service
writes reach the product's tables, most without the product's row lock, and
only the product's own and the schedule's writes publish an event.

## Decision

Every write through a product's own admin routes, and the schedule's pass,
takes the product's row lock and, when the product's admin view changed, appends
a revision holding the view without timestamps and the fields that changed,
numbered by a `version` the product carries.
`GET /admin/v1/products/{id}/revisions` lists them, `GET .../revisions/{version}`
reads one, and `POST .../revisions/{version}/restore` writes back its own fields,
collection, type, tags, categories and attribute values as a new revision.

## Consequences

The view is assembled in the service, so the revision is taken there, in the
write's transaction: up to nine reads and a JSONB row per write. Writers of one
product now wait for each other. The comparison is over canonical JSON, so a
write that changes nothing appends nothing and JSONB's key order is no change.
A revision names the HTTP request that made it, the key the audit log names its
caller under; a job's names none.

A product created from here on starts its history at its creation; one written
before starts at its first write since, with what it was as revision 1, unless
that write is the schedule's pass, whose statement has already changed the row.
Removing a collection, type, tag, category or attribute changes every product
carrying it without a revision of each; relations, channels, prices and stock
links are not in the view.

A restore writes its fields exactly, NULLs included, which PATCH cannot. What
was removed since is left out and named in `dropped`, a handle taken since is
refused with 409, and the status, schedule, variants, options and images stay as
they are. It publishes `product.updated`.

Revisions are only appended; a product row removed outright takes them with it.
Two arch gates hold the SQL to inserts and every service write reaching a table
of the view to a revising transaction or a named exemption. The storefront does
not answer the version; nothing checks it on a write yet.

## Rejected

- **A staged copy the storefront does not read.** Every catalog read would
  choose between two copies of each table of the view.
- **Triggers writing the revisions.** The view is assembled in Go, and a trigger
  fires per row; ADR 0167 kept procedural code out of the schema.
- **Differences instead of snapshots.** Reading a revision would replay every
  one before it.
- **Recording from `product.updated`.** Variant, option, image and attribute
  writes publish none, and an event after commit can be lost.
- **Restoring variants, options and images.** Carts, orders, prices, stock and
  uploads hold them by id.
