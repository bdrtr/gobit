# ADR 0184 — The GraphQL product names its neighbors

**Summary:** A GraphQL product has a `related(type:)` field that reads one kind
of its relations the way the REST address does, priced as a database round
trip. It costs a cycle in the schema, and the schema now holds the cost model's
lists and its calibration document by gates.

- **Status:** Accepted
- **Date:** 2026-09-25

Measurement: [measurements/0184](../measurements/0184-a-cycle-with-a-price.md)

## Context

ADR 0180 put related products on the REST storefront and ADR 0181 on the
panel. The GraphQL storefront, where a product page can read everything in one
document, could not reach them. Its cost model prices a root query as a round
trip and a nested list by a fixed estimate per element. A nested list that is a
read of its own had no price. No gate asked whether every list had
one, and the document the ceiling is calibrated against was held to "every
field" by a sentence that two fields had already slipped past (D134). The
schema had no cycle, and its depth limit waited for the day one appeared.

## Decision

`Product.related(type: RelationType!)` returns the storefront service's
`StoreRelatedProducts` for that product, with the channels of the verified
identity, and the complexity model prices it as a root query. Every list field
of the schema must carry a cost, and the calibration document must select every
field it reaches except the cycle, each checked against the generated schema.

## Consequences

A product page reads the product and its three lists in one document, well
under the ceiling. The field under a page of 50 products is refused, and so is
a chain of three. Depth stops a chain for a deployment that raised the
complexity ceiling, which is the day the depth limit was set for.

Each product that selects the field is a read of its own, and there is no batch
loader. The ceiling bounds how many such reads a document can ask for.

The resolver asks the service with the product's id, so the parent is read
again under the single product query's rule. That costs one read by id and
keeps one rule in one place.

The resolvers' port grows by a method the REST storefront calls too. A schema
field with no counterpart on the record is allowed only by naming such a method.

The calibration document now selects `typeId` and `altText`, missing since
ADR 0101 and ADR 0104, so the heaviest legitimate document it measures grew. The
rows whose documents changed were measured again, bytes included.

`RelationType` is spelled in lower case, bound to the module's type, as
`ProductOrder` is.

## Rejected

- **A root query, `relatedProducts(handle:, type:)`.** It mirrors the REST
  address, and it cannot follow a product the document already holds.
- **A batch loader.** It takes a dependency or a per-request wait. It changes
  what a read costs, not how many a document can ask for, and the ceiling
  already bounds the second.
- **The nested-list estimate.** The field under a page of 50 would pass, at a
  thirty-second of the price a round trip for each product gives it.
- **A service method that trusts a parent already shown.** It would be a second
  place the visibility rule could drift.
