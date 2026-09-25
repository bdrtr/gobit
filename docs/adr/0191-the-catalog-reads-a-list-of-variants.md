# ADR 0191 — The catalog reads a list of variants

**Summary:** The storefront product listing takes up to 100 variant ids and
returns the products that own them, once each and whole. It costs one clause in
the listing's filter, and it is how a wishlist is shown in one read.

- **Status:** Accepted
- **Date:** 2026-09-26

Measurement: [measurements/0191](../measurements/0191-a-list-shown-in-one-read.md)

## Context

ADR 0190 keeps a customer's wishlist as variant ids and does not show it. The
storefront reads products through a listing narrowed by channel, collection,
category, tag, option value, stock and price, and none of those names a
variant, so a storefront holding variant ids had no read that turned them into
products it may show.

## Decision

The storefront listing keeps the products that own at least one of up to 100
named variants, as a repeated `variant_id` parameter on REST and a `variantIds`
argument on GraphQL, under every rule the listing already applies. A variant
that was deleted, or whose product the listing would not show, names nothing
and is not reported.

## Consequences

A wishlist of 200 is two reads. The bound is the listing's page size, so one
page holds every product a request names.

A product comes back with all its variants, and the storefront picks out the
one it saved. The page is in the listing's order rather than the wishlist's,
and the storefront orders it by its own list.

A saved variant the catalog no longer shows is simply absent, and the wishlist
keeps its id until the shopper removes it. The filter combines with the others,
so a storefront can ask for the saved variants that are in stock.

The variants are resolved to their products first and the products are then
read by key, so the read touches the products the list names and not the
catalog in page order.

The admin listing does not read the parameter.

## Rejected

- **A variant endpoint that returns each variant with its product.** It is a
  second body shape and a second enrichment path for prices and stock the
  listing already has.
- **The wishlist answering with products.** The customer module cannot read
  the product module (ADR 0001), and a workflow owning a storefront route for
  one read is a second catalog surface.
- **Product ids on wishlist items.** The client would supply a pairing nothing
  checks, and the variant already names its product.
- **Reporting the ids that named nothing.** The listing's body says what it
  shows, and a list of absences needs a field of its own.
