# ADR 0261 — A storefront category lists its subcategories

**Summary:** The storefront listing, its facets and the GraphQL listing take
`category_tree_id`, which keeps the products whose `category_tree_ids` name the
category; the statements carrying it are planned for the ids they are given.

- **Status:** Accepted
- **Date:** 2026-09-30

## Context

ADR 0259 gave a promotion rule a product's categories with their ancestors and
left the storefront's category filter matching direct membership, because
walking the tree there changes the plans of the catalog's most measured query.
A shop whose menu opens "Apparel" had to file every shirt under "Apparel" as
well as "Shirts" for the page to list it.

Measurement: [measurements/0261](../measurements/0261-subtree-filter.md)

## Decision

`category_tree_id` resolves the category to itself and every live category
below it, walked down as ADR 0259's lineage walks up, and filters the listing
with `category_id = ANY` over that set, beside `category_id` and not in its
place. A statement carrying the filter runs as an unnamed statement, so
PostgreSQL plans it for the ids of each call and caches no generic plan for it.

## Consequences

- A product is listed under a category exactly when its `category_tree_ids`
  name it: a deleted category ends the walk both ways and both stop at
  sixty-four levels. The integration test reads the expected set off the
  records, for every category of a tree and at the bound.
- A switched-off or internal subcategory is walked. The flags keep a category
  out of the menu, and `category_id` has never read them either.
- With one id the clause plans and reads what `category_id = $n` does. A
  subtree holding the whole catalog counts under the channel filter at the
  unfiltered count's cost, 87 ms against 74 on the rig.
- The generic plan was measured wrong in both directions: adopted at the
  whole-catalog subtree, where it counts in 152 ms, and a 21 ms list at a
  52-product subtree when forced. Planning every call costs the Parse the
  statement cache saved, a fraction of a millisecond.
- The subtree is read in one recursive query per request, 0.15 ms for 22
  categories and 7.6 ms for 5,072. A listing that scans for stock or price
  reads it once per chunk.
- A category that does not exist lists no product, as `category_id` does.
- The read layer's product filter, which the panel's catalog uses, still
  matches direct membership, and `parent_id` on the category listing walks one
  level. The known limit now names these.

## Rejected

- Walking the tree inside the listing's SQL: the planner would see a
  recursive subquery with no estimate of its size, which is the problem the
  unnamed statement solves, a second time.
- Changing `category_id` to walk the tree: every client asking for direct
  membership would change meaning at once.
- Forcing custom plans for the whole pool with `plan_cache_mode`: it would
  re-plan every statement the product module runs, most of which a generic
  plan serves correctly.
- Resolving the subtree in the client from the category listing: a
  storefront would page through the tree on every category page.
