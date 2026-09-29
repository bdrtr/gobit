# ADR 0244 — A bundle variant is replaced from its catalog parts

**Summary:** A replacement item that names a bundle variant rather than an order
line is recorded with the parts the catalog gives the bundle at that moment, and
is then held, sent, withdrawn and recalled part by part like a line's.

- **Status:** Accepted
- **Date:** 2026-09-30
- **Amends:** [0238](0238-a-bundle-is-replaced-from-its-parts.md), which left such an item refused

Measurement: [measurements/0244](../measurements/0244-a-box-the-order-never-sold.md)

## Context

ADR 0238 sent a line that sold a bundle from the parts the line kept, and left
an item that names a bundle variant refused: the order never sold that box, so
no line kept its parts, and the dispatch found no inventory item for the box
itself. An exchange that answers a shirt with a gift box could not send it.

## Decision

Recording a replacement reads, in one catalog call before its transaction, what
each variant the request names is made of, and writes a bundle's parts on its
item. Everything after the record reads the parts, as it does for a line.

## Consequences

- The promise is the bundle as the shop makes it when the replacement is
  recorded; a bundle edited afterwards sends what was promised.
- The order module reads the variant's `bundle_components` through the query
  layer it already holds; the names it reads by are held to the product
  module's by `TestTheBundleNamesAgree`.
- A catalog read that fails, or a part that cannot be counted, records nothing
  (`order_catalog_read_failed`).
- An installation without the query layer records no parts, and its dispatch
  refuses a bundle for having no inventory item, as before.

## Rejected

- **Reading the catalog at dispatch.** The flow would send whatever the bundle
  had become, and a withdrawal or a recall would release parts the dispatch had
  not recorded promising.
- **Having the operator list the parts.** The shop already says what the box is
  made of, once, in the catalog.
