# ADR 0232 — The panel edits a product's add-ons

**Summary:** The product page lists a product's add-ons and a form replaces the
list, one variant per line named by its SKU or its id, resolved and refused by
the product module.

- **Status:** Accepted
- **Date:** 2026-09-29

Measurement: [measurements/0232](../measurements/0232-an-operator-names-an-engraving.md)

## Context

ADR 0228 gave a product a list of add-ons that only the admin API could write,
by variant id. An operator using the panel could not see which add-ons a
product's lines accept, and an operator does not know a variant's id: a SKU is
what is printed on the stock and in the catalog export.

## Decision

The product page lists the add-ons, each with its product and variant and the
storefront's hiding said, and a form at `/admin/ui/products/{id}/add-ons`
replaces the list, one reference per line in the storefront's order. A reference
is a variant id or a SKU, resolved by the product module's admin surface, which
refuses a SKU no live variant carries by the text typed and applies ADR 0228's
rules.

## Consequences

The page reads the add-on variants in one call and their products in another,
and a product without add-ons costs neither. The form shows each add-on by its
SKU, or its id when it has none, so a saved list comes back as written; a
refused save comes back with what was typed and the module's sentence. The form
is a product write and demands the product write privilege, as the related
products' form does. The limit the form prints and the field the page reads are
bound to the module's at compile time.

## Rejected

- **A picker over every variant.** A catalog of fifty thousand products has no
  screen that lists them all, and the related products' form types handles for
  that reason.
- **Variant ids alone.** An operator would look each one up before saving, which
  is the lookup the admin surface makes in one read.
