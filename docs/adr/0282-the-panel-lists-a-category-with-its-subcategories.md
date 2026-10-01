# ADR 0282 — The panel lists a category with its subcategories

**Summary:** The product query provider takes `category_tree_id`, a category
with every category below it, and the panel's catalog filter asks it when its
"with its subcategories" box is checked.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

The storefront's listing has taken `category_tree_id` since ADR 0261, and the
known limits said the query layer's product filter, which the panel's catalog
filter reads, takes `category_id` alone: an operator looking at "Clothing" saw
the products filed under it directly and none filed under "Shirts" below it.
The repository's listing already reads a subtree, and the product service
resolves a category into its subtree for the storefront.

## Decision

The product provider takes `category_tree_id`, resolves it through the
repository's category subtree and lists the products filed anywhere in it, a
category that does not exist keeping nothing. The panel's catalog filter draws a
"with its subcategories" box beside the category dropdown and asks
`category_tree_id` instead of `category_id` when it is checked.

## Consequences

- An operator lists a category with everything below it, and the paging links
  keep the choice in the address as `subcategories=1`.
- `category_id` stays the category alone, so a screen or a consumer that asked
  for direct membership still gets it.
- The id path refuses `category_tree_id` as it refuses the other taxonomy
  filters, since membership is not read there.
- The box is unchecked by default: the list an operator bookmarked keeps the
  meaning it had.

## Rejected

- Making the panel's category filter always a subtree: a bookmarked list would
  change what it shows, and a direct filing is a question operators ask too.
- A second provider for category trees: the subtree is a filter of the product
  listing, and the listing already has one SQL body for both surfaces.
