# ADR 0181 — The panel edits a product's neighbors

**Summary:** The admin panel's product page lists the product's related
products, and a form edits all three lists at once, one handle per line. The
read layer's product record carries the lists, and the panel's save writes all
three or none.

- **Status:** Accepted
- **Date:** 2026-09-25

Measurement: [measurements/0181](../measurements/0181-a-list-an-operator-can-type.md)

## Context

ADR 0180 gave products related products on the admin API and the storefront,
and left the panel out. An operator who manages the catalog in the panel had no
way to see a product's lists or change them. The panel reads only through the
read layer and writes only through the product module's admin surface (ADR
0011, ADR 0013), and neither carried relations.

## Decision

The product record in the read layer carries `cross_sell_ids`, `up_sell_ids` and
`substitute_ids`, filled in one batch read only when asked for. The admin
surface's `SetProductRelations` takes the three lists as handles or ids, and
the panel's form at `/admin/ui/products/{id}/relations` saves them in one
transaction.

## Consequences

An operator names a product by its handle. A catalog of fifty thousand products
does not fit a picker, and ids are not what an operator knows. The module
resolves the handles in one read, and only live products count, since a deleted
product's handle can belong to its successor. A reference with the product id
prefix is taken as an id, as on the storefront's single product address. A
reference that names nothing is refused as typed.

The save is all or none. Every list is checked, and every product looked up,
before the one transaction that replaces them. The panel therefore checks
nothing itself: a refusal has already written nothing, and the form comes back
with every list as typed and the module's sentence naming the line to fix.
`SetProductRelations` on the API is the same code with one kind in the map.

The product page reads the related products in one more call for all three
lists, and only when there are any. A related product the storefront leaves out
is marked as such, so the list reads as the operator wrote it and shows why a
shopper sees a shorter one. The panel cannot tell which channel a published
product is bound to, so it marks only the status.

The read layer's whole record now costs one more batch read. A reader that
names its fields and leaves the lists out pays nothing, which is the rule the
category and tag lists already follow (ADR 0148).

The panel spells the three fields, the kinds and the limit it prints, and
internal/arch binds each to the module's at compile time.

## Rejected

- **A picker over the catalog.** It does not fit a large catalog.
- **Saving each list on its own.** A form that is half saved when the third
  list is refused tells the operator nothing they can trust.
- **The panel resolving handles through the read layer.** The product
  provider's handle filter takes one handle, so it would cost a read per line,
  and resolution would run twice — once in the panel, once in the module.
- **A read-layer entity of its own for relations.** The read layer's contract
  fetches by id, and a relation has none. The lists belong to the product the
  way its categories do.
