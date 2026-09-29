# ADR 0234 — A variant names what it is made of

**Summary:** A variant can name the variants of other products one unit of it
holds, and how many of each; a bundle is counted and sold from no stock of its
own, so until its stock is read from its parts it reads out of stock.

- **Status:** Accepted
- **Date:** 2026-09-29
- **Amended by:** [0235](0235-a-bundle-sells-from-its-parts.md): a bundle reads in stock from its parts and sells, each component reserved and put back

Measurement: [measurements/0234](../measurements/0234-a-gift-box-of-three.md)

## Context

A shop sells a gift box of a towel and two soaps as one product, at its own
price, from the stock of its parts. The catalog had no way to say what a
variant is made of: the only box it could sell was a variant with an inventory
item of its own, counted apart from the towels and soaps on the same shelf.
Where the composition lives and how it stays whole are this record's questions;
how the box sells from its parts is the next one's, and this record fixes the
state it starts from.

## Decision

A variant carries an ordered composition of at most 20 live variants of other
products, 1 to 100 units each, none a bundle and none a gift card, written as a
revision of its product through `PUT /admin/v1/variants/{id}/bundle` and read
as `bundle_components` on every variant read and `bundleComponents` in GraphQL.
A bundle is counted, not sold past zero and linked to no inventory item, and a
component is not deleted while a bundle holds it, both held under row locks.

## Consequences

- The box keeps its own price set and the storefront shows its parts, while
  ADR 0040's badge answers out of stock, as for any counted variant nothing
  counts, and checkout refuses it with `checkout_workflow_variant_not_stocked`.
  A bundle can be set up and priced before it sells.
- What the input can fix answers 422. What another record has to change first
  answers 409 with `product_bundle_shape`: a bundle inside a bundle either way
  round, a bundle not counted or sold past zero, a bundle with an inventory
  item. The variant update and the inventory link refuse the same states from
  the other side, and clearing a composition is always allowed.
- Deleting a component or its product answers 409 naming the bundles holding
  it; deleting the bundle or its product takes the composition. The bundle
  write locks every variant it names in id order, and a deletion locks its
  variants before it asks who holds them, under the product's lock for a
  product: a composition and a deletion at the same instant end with one of
  them refused.
- The inventory link lives in the link service, outside the product
  transaction. Each side checks the other before it writes, and a link and a
  composition written in the same instant can both land.
- A revision carries the composition; a restore leaves variants alone
  (ADR 0221), and their compositions with them.
- Every GraphQL document selecting every field costs 200 more per product, the
  default page 37,880 against the ceiling of 50,000. The two published copies
  of the calibration table had kept the numbers from before ADR 0219 (D158); a
  test now holds them to the pinned table.

## Rejected

- **A bundle as a product of its own kind.** The variant is what carries a
  price, a SKU and a cart line; a bundle that is not one needs all three again.
- **An inventory item for the bundle kept in step with its parts.** Two counts
  of one shelf drift apart the first time either moves alone.
- **Bundles inside bundles.** Every write would have to refuse cycles and every
  checkout multiply reservations; a box of boxes is written as its parts.
- **A bundle sold on its own flags until its parts are read.** One not counted
  would sell without reserving a single part.
