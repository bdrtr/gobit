# ADR 0307 — The panel creates a product and its variants

**Summary:** The product module's admin surface creates a draft product and
adds a variant to a product, and the panel offers both to an operator who
holds `product:write`: a form under the product list and one on the product's
page. A new product's prices and stock are still set up over the admin API.

- **Status:** Accepted — amended by [0309](0309-a-variant-takes-a-price-in-the-panel.md), [0310](0310-a-variant-begins-to-keep-its-stock-in-the-panel.md)
- **Date:** 2026-10-01

## Context

The panel edited the catalog it was given — a product's title, handle, status,
schedule, related products and add-ons, a variant's bundle, base prices and
stock — and created nothing: a product and its variants were made over
`/admin/v1` with a bearer token. The known limits named it the editable part
of the catalog rather than the creatable part. The product module created
both through its service, deriving a handle from the title, and the panel
wrote through a module's admin surface in primitives (ADR 0013).

## Decision

The product admin surface gains `CreateProduct`, which creates a draft with
the title and the handle or the title's slug, and `AddVariant`, which adds a
variant with a title and an optional SKU. The product list links to a form
that creates a product and goes to its page, and the product page offers a
form that adds a variant and goes to the variant's page.

## Consequences

- An operator creates a product and its variants without an API client; the
  product stays a draft, which the storefront does not show, until it is
  published on its edit form.
- A taken handle or SKU is refused as the service refuses it, and the form
  says so.
- A new variant has no price set and no stock item, so its page offers
  neither form; those, and the links that bind them, are still made over
  the admin API.
- Options, images, categories and the product's other fields are set over
  the admin API, as before.

## Rejected

- Creating a published product: it would be shown before it has a price.
- Creating the price set and the stock item from the panel in the same step:
  each belongs to another module, and the panel would run a three-module
  write with no transaction to undo the parts that succeeded.
