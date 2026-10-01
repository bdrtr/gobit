# ADR 0293 — A telephone order finds a variant by its title

**Summary:** The telephone order's cart page searches the catalog by product
title and offers the found products' variants to the add form by name. The
search is shown only to an operator who may read the catalog.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

ADR 0290 had the operator type a variant's id into the add form. A caller
names a product, not an id, so the operator had to open the catalog in another
screen to find one. The panel already searched products by title on its
catalog screen, through the product provider's `q` filter. Each module's data
on a page is read under that module's privilege (ADR 0251, ADR 0260), and the
cart page asks only for `cart:read`.

## Decision

The cart page reads the products whose title matches the term typed into its
search box, then their variants, and the add form offers those variants as a
list labeled with the product, the variant and its SKU. The search is offered
only to an operator who holds `product:read` as well as the cart's write.

## Consequences

- An operator adds a line by the product's name; an operator without the
  catalog's privilege keeps the id box, and nothing of the catalog is read for
  them.
- One search reads at most ten products and their variants, in two reads.
- The list shows every variant of a found product, including one the named
  channel does not sell. Adding it is refused on the page, as before.
- A search that matches nothing or cannot be read says so and leaves the id
  box.

## Rejected

- A variant search in the read layer: the product provider already searches
  titles, and a variant's title is a size or a colour, not what a caller says.
- Narrowing the list to the channel's catalog: the operator names the channel
  in the same form, after the search.
- Searching under `cart:read` alone: the page would read the product module's
  data under another module's privilege.
