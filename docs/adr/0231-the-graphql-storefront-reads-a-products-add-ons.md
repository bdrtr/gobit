# ADR 0231 — The GraphQL storefront reads a product's add-ons

**Summary:** A GraphQL `Product` answers `addOns`, the add-ons its cart lines
may carry, through the REST add-on read's own service call and priced as a
round trip for every product that selects it.

- **Status:** Accepted
- **Date:** 2026-09-29

Measurement: [measurements/0231](../measurements/0231-an-add-on-on-a-product-page.md)

## Context

ADR 0228 gave a product a list of add-ons and a storefront REST read of it, and
ADR 0229 let a cart line carry them. A storefront built on GraphQL read the
product it shows there and had to leave the surface for the add-ons it offers
beside it.

## Decision

`Product` gains `addOns: [AddOn!]!`, each the add-on variant's id and its
product, answered by the REST read's service method with the product it hangs
off and the channels of the request's identity. The field is priced as the
related products are: a root query's cost plus its list, for every product that
selects it.

## Consequences

The two surfaces answer one list because they ask the service the same thing: an
add-on whose product is not published or not visible in the channels is left
out on both. One product page reads its add-ons in the same document; a page of
fifty products each asking for theirs is refused under the default ceiling
before any read, as fifty related lists are. `AddOn` binds the service's own
`StoreAddOn`, so the field gates read it, and the calibration document leaves
`addOns` out for the reason it leaves out `related`: an add-on's product leads
back into `Product`.

## Rejected

- **The add-on ids on the product body.** The storefront would read the ids and
  then the products they belong to in a second request, and a draft's id would
  be named where its product is hidden.
- **Add-ons as a root query.** A product page asks for them with the product,
  and the root query would repeat what the field already answers.
