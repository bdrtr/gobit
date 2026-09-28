# ADR 0225 — The GraphQL storefront reads the vocabulary

**Summary:** The GraphQL storefront answers the collections, the public
categories, the tags and the product attributes through the REST vocabulary
reads' own service calls, each priced by the page it asks for.

- **Status:** Accepted
- **Date:** 2026-09-28

Measurement: [measurements/0225](../measurements/0225-a-filter-and-its-words.md)

## Context

The GraphQL storefront's `products` query filters by `collectionId`,
`categoryId`, `tagId` and `attributes` (ADR 0219), and no query of the schema
named a collection, a category, a tag or an attribute: a client read the REST
vocabulary endpoints for the ids and handles it then passed to GraphQL. The
gates holding each schema type's fields to its Go type read a list of types
written by hand, and three of the schema's ten, `ProductList` and ADR 0219's
two, were never on it (D153).

## Decision

The schema gains `collections`, `categories`, `tags` and `productAttributes`,
each calling the service with its REST read's arguments, the categories public
only, and each charged its page size times its selection. The field gates read
every object type the compiled schema defines.

## Consequences

A storefront built on GraphQL reads its menus and its filter vocabulary from
the surface it lists products on, and the two surfaces cannot answer the
question differently because they ask the service the same thing. The pages go
by offset with `count`, `offset` and `limit`, as the REST pages do; neither
surface scopes the vocabulary to a sales channel, since which products a
channel sells is the listing's question. A collection's metadata stays on the
REST read.

A vocabulary query costs 1,000 plus its page size times its selection, the
attributes are charged as the catalog's ceiling of 100, and an attribute's
options as a list; `collections(limit: 100)` with three fields fits under the
default ceiling and is refused under one of 1,200. The channel's option values
and facet counts stay REST reads, since they take the listing's channel and
filters. A type bound in `gqlgen.yml` and missing from the gates' list now fails
a test, and `ProductAttribute` and `AttributeOption` declare the fields they
leave out; `ProductList` leaves out none.

## Rejected

- **A cursor on the vocabulary.** Its lists are short and have no deep page to
  make cheap, and the REST reads page by offset.
- **Resolvers reading the repository.** Two surfaces with two queries could
  start answering one question differently.
- **The vocabulary scoped to the request's channels.** The REST reads are not,
  and GraphQL alone scoping it would make the surfaces disagree.
- **Adding the two missing types to the list.** The next type bound by hand
  would escape the same way.
