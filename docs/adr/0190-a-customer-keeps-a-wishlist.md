# ADR 0190 — A customer keeps a wishlist

**Summary:** A proven shopper saves product variants to a wishlist of up to 200,
and the operator reads it. It costs one table in the customer module, and the
list holds variant ids that the catalog answers for when it is shown.

- **Status:** Accepted
- **Date:** 2026-09-26

Measurement: [measurements/0190](../measurements/0190-one-persons-bookmarks.md)

## Context

A shopper had no way to keep a product for later except a cart, which carries a
region, a currency and a price that are fixed when the line is added. A list
that belongs to a person can only be served once the storefront knows who is
asking: ADR 0043 made every storefront route that names a customer require a
proven identity, and ADR 0127 ships one to bind.

## Decision

The customer module keeps a wishlist of up to 200 variant ids per customer,
saved by `PUT` and removed by `DELETE` on
`/store/v1/customers/{id}/wishlist/{variant_id}`, both repeatable and reached by
the proven customer alone, and listed newest first on the storefront and on the
admin path. The wishlist is declared personal data: a person's file lists it and
an erasure deletes it.

## Consequences

The count is taken under the customer row's lock, so two saves cannot both
take the last place. A full list refuses a new variant with `409
customer_wishlist_full` and still answers one already on it, and the listing is
not paged.

The variant id is checked for its form only, because the product module owns it
(ADR 0001). A variant that was deleted or never existed stays on the list until
the shopper removes it. The storefront's catalog read does not take variant ids
yet, so this record keeps the list and does not yet show it.

A variant id in a cart line names a thing, and the cart does not declare it. A
wishlist row exists only because the person chose the variant, so beside their
customer id it is a preference they stated, and nothing else refers to it.

A soft-deleted customer's wishlist stays in the table, unreachable, as the
addresses do, until an erasure deletes it.

Fifteen storefront routes now require a proven customer, three of them the
wishlist's. The primary key is the table's only index.

## Rejected

- **Checking the variant against the catalog when it is saved.** It needs a
  product port in the customer module for an answer that goes stale when the
  variant is deleted, and the catalog decides visibility when the list is read.
- **A module of its own.** The list needs the customer row's lock and the
  customer's erasure, and both are here.
- **Several named lists per customer.** Nothing in the repository asks for more
  than one.
- **A copy of the title and price on each item.** It is stale after the next
  price change, and the catalog read is current.
- **An index ordered for the listing.** It measured as large as the table and
  saved 0.03 ms on a full list.
