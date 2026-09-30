# ADR 0259 — A category rule can reach the subcategories

**Summary:** The product record publishes `category_tree_ids`, its categories
with every ancestor of them, and the cart hands it to the promotion engine, so a
rule naming a parent category reaches a product filed only under its children.

- **Status:** Accepted
- **Date:** 2026-09-30

## Context

A promotion rule reads a product's categories from `category_ids` (ADR 0148),
which is its direct memberships. A merchant running "half off Shirts" over a
tree had to name every subcategory, and a subcategory added later was left out
of the sale without a word. The known limits said the record was one of the
two places descendant resolution would go; the storefront's category filter is
the other.

## Decision

The product record publishes `category_tree_ids`: its categories in rank order,
each followed by its live ancestors nearest first, every id once. The cart
reads it beside `category_ids` and puts it in the line's lists, where a rule
with `any_in` on `category_tree_ids` matches a product under any of the named
categories' subtrees.

## Consequences

- A merchant chooses per rule: `category_ids` still matches only what a product
  is filed under, and `category_tree_ids` matches the subtree. No rule written
  before changes meaning.
- The ancestry is read in one recursive query for every category of the page's
  products, bounded at sixty-four levels as the reparent's cycle check is, and
  only when the field is asked for.
- A deleted category ends the walk upward, and a subcategory added later is in
  its parent's tree without anyone renaming the rule.
- The storefront's category filter still matches direct membership. Walking
  the tree there changes the plans of the catalog's most measured query, and it
  is left to its own record; the known limit keeps that half.
- The field is published to every reader of the product record, and one that
  asks for every field pays for the ancestry read.

## Rejected

- Changing `category_ids` to include the ancestors: every existing rule and
  every reader of the direct memberships would change meaning at once.
- Resolving descendants in the promotion engine: it knows no category and
  would have to read the product module's tree to learn one.
- A flag on the rule saying "include subcategories": the engine matches a list
  against a list, and a second list says the same with no new operator.
