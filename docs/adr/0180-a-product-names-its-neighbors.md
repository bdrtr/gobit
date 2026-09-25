# ADR 0180 — A product names its neighbors

**Summary:** An operator can list, for a product, what goes with it, what the
better one is and what to buy instead, in their own order. The storefront reads
one of those lists at a time and shows only the products it may show.

- **Status:** Accepted
- **Date:** 2026-09-25

Measurement: [measurements/0180](../measurements/0180-a-product-page-and-its-neighbors.md)

## Context

A product page's "goes with it", "the better one" and "instead of this" widgets
had nothing to read. The tree had no cross-sell, up-sell or related product
anywhere. The nearest things were a collection or a tag, and both are sets:
they have no direction from one product, no kind, and no order.

## Decision

A product holds an ordered list of up to 50 other products for each of the
closed kinds `cross_sell`, `up_sell` and `substitute`. The admin surface reads
all of them and replaces one kind whole with `PUT
/admin/v1/products/{id}/relations/{type}`, and the storefront reads one kind at
`GET /store/v1/sales-channels/{sales_channel_id}/products/{id}/related?type=`.

## Consequences

A relation is directed and typed. A belt as a shirt's cross-sell says nothing
about the belt's own lists, and an operator who wants both writes both. The
order is the operator's. It is stored as a rank that is unique within a kind,
so two reads of a list can never order it differently.

The write refuses and never trims. An id that names no product, a deleted
product, the product itself, the same product twice or a 51st entry is refused
with 422, which names what it refused, and nothing is written. The table holds
the same rules by itself: foreign keys, a CHECK on the kind, the product and the
rank, and the two keys.

Visibility is decided when the list is read, not when it is written. A draft or
a product bound to another channel can be put on a list, so a launch can be
lined up before it happens. The storefront passes the list through the rule
search uses: a related product that is not published, or not in the channel,
is left out without leaving a gap. The product the read starts from must pass
the single product endpoint's rule, or the answer is 404, so a list cannot be
read off a draft or another channel's product. A list can therefore come back
shorter than it was written. The admin read shows it as written.

The storefront names a kind because each kind is a separate widget, and every
related product is enriched with its prices and stock. Fifty sits under the 100
ids that enrichment accepts, and a test holds that. The answer depends on the
URL alone, so it may be cached.

Deleting a product removes the relations from it and to it, in the same
transaction as its other children. A row removed outright cascades.

Migration 000009 adds the table. The panel does not edit relations yet. GraphQL
and the search index do not carry them.

## Rejected

- **Collections or tags as the relation.** Sets with no direction, kind or order.
- **One untyped "related" list.** A storefront could not tell the widgets apart.
- **Free-text kinds.** A misspelled kind would become a list nobody reads.
- **Writing each relation both ways.** An up-sell runs one way only.
- **Requiring published products on write.** It blocks lining up a launch, and
  it goes stale the day a related product is archived.
- **All three kinds in one storefront call.** It enriches three lists for a page
  that shows one.
- **Recommendations derived from orders.** A different feature, one that would
  build on these lists.
