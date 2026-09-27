# ADR 0219 — A product carries typed attributes

**Summary:** An operator defines store-wide attributes, each a number, a boolean
or a select with options, and gives products values of them. The storefront
filters its listing by them, over REST and GraphQL, and counts, per value, the
products its filters keep.

- **Status:** Accepted
- **Date:** 2026-09-28

Measurement: [measurements/0219](../measurements/0219-a-catalog-that-can-be-narrowed.md)

## Context

A product carried a type, collections, categories, tags, options that make its
variants, and an untyped `metadata`; the storefront filtered by one collection,
category, tag or option value (ADR 0039). What a shopper narrows a catalog by —
a fabric, a width, whether it is waterproof — had no typed home, and no listing
said how many products each choice would leave. The listing's filters are
EXISTS clauses in one SQL body shared with its count, scoped to the request's
sales channel (ADR 0044). The user chose store-wide definitions, values on the
product, and filtering with counts.

## Decision

`/admin/v1/product-attributes` defines attributes of kind `number`, `boolean` or
`select` with options, and `PUT /admin/v1/products/{id}/attributes` replaces a
product's values, each checked against its attribute's kind. The storefront
listing and the GraphQL `products` query take attribute filters, options ORed
within an attribute and attributes ANDed, and `/store/v1/sales-channels/{id}/
product-facets` counts the products the same filters keep per value, an
attribute's own filter left out of its own count.

## Consequences

A filter is one more EXISTS clause in the listing's shared body, so the list,
its count and the facets read the same set in the same channel. A handle, an
option or a value the catalog does not have is refused with 422 rather than
answered with an empty page. A facet request is one query for every attribute
not filtered and one per filtered attribute, at most ten; in-stock and price,
which are answered after enrichment, are refused there.

An attribute's handle and kind do not change once written; its title and order
do. Options are ordered by rank, then handle. A removed attribute or option is
soft deleted: the products no longer carry it, and a filter naming it is
refused. At most 100 attributes stand, their writers serialized by advisory lock
class 7, and a select holds at most 200 options under its row lock.

A select value's option and attribute are held together by a composite foreign
key; that a number attribute holds numbers is the service's to check. Values are
not in the CSV export and import yet. The GraphQL schema gains the filter and the
product's values, and its complexity calibration moved with them.

## Rejected

- **A schema per product type.** A product without a type could hold no
  attribute, and the storefront filter would need the type to know the kinds.
- **Values on the variant.** Options already make the variant axis; a second
  one would overlap them and send every filter through the variants.
- **Counting within every filter.** A shopper who chose cotton would see wool
  count zero and could not tell what the other choice leaves.
- **Filtering in Go after the page is read.** The page would hold fewer
  products than its limit, and the count would not match the list.
