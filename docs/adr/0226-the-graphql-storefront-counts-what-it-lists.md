# ADR 0226 — The GraphQL storefront counts what it lists

**Summary:** The GraphQL storefront answers the facet counts and the option
vocabulary through the channel-scoped REST reads' own service calls, scoped to
the request's sales channels as its listing is.

- **Status:** Accepted
- **Date:** 2026-09-28

Measurement: [measurements/0226](../measurements/0226-the-count-beside-the-listing.md)

## Context

ADR 0225 left two storefront reads on REST alone, the facet counts and the
option vocabulary, because they take the listing's sales channel and filters.
The GraphQL listing takes both, the channels from the request's identity and the
filters as arguments. A storefront built on GraphQL still read `/product-facets`
for the counts beside its filtered page and `/option-values` for the words its
`optionValue` filter takes, under a channel path the schema never names.

## Decision

The schema gains `productFacets`, taking the listing's catalog filters under
their names and types, and `optionValues`, each calling its REST read's service
method with the channels of the request's identity. A facet count is charged a
root query for every attribute it filters on plus the catalog's ceiling of 100
facets, and the option vocabulary its page.

## Consequences

A storefront hands `productFacets` the filters it listed with and reads counts
of the same catalog, since the service method is the REST count's and the
channels are the identity's on both queries. The listing's page, order,
`inStock` and `price` are not arguments: a count covers the whole filtered
catalog and the service refuses the two enriched filters, so the schema does not
offer what the call refuses. A test holds every `productFacets` argument to a
`products` argument of the same type, and every `products` argument it leaves
out to a written reason.

The surfaces scope these reads as they scope the listing: REST counts the one
channel its path names, GraphQL every channel the key carries, and a key bound
to one channel receives the same answer from both. A facet's fields carry the
REST body's names, `true` and `false` among them, because the schema type is
bound to the service's own `Facet`; a number or boolean facet answers an empty
option list where the REST body leaves it out.

The facets and their options are charged as the attributes are, and every
filtered attribute adds the price of a root query, since the count makes a round
trip for each. The option vocabulary's page is priced as the other vocabulary
pages are.

## Rejected

- **The listing's other arguments on `productFacets`, ignored or refused.** An
  argument the service refuses or does not read promises a filter that does not
  work.
- **A channel argument narrowing to one of the key's channels, as the REST path
  does.** The schema takes no channel from the request, and the listing beside
  the count would still read every channel the key carries.
- **A `facets` field on `ProductList`.** The listing's `inStock` and `price`
  would refuse it as they refuse `count`, and the REST count is a read of its
  own.
- **Other names for a facet's counts, such as `trueCount`.** A mapping between
  the schema's names and the bound type's is the second definition the binding
  policy avoids, and the REST body would name the fields differently.
