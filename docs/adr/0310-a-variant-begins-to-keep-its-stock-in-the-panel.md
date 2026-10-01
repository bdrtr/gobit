# ADR 0310 — A variant begins to keep its stock in the panel

**Summary:** The product module's admin surface gives a variant an inventory
item, which inventory creates and the product module links, through a narrow
writer resolved by name as pricing's is for the import. The variant page offers
it to an operator holding `product:write` and `inventory:write`, and the stock
form then counts the item at each location.

- **Status:** Accepted — amends [0307](0307-the-panel-creates-a-product-and-its-variants.md)
- **Date:** 2026-10-01

## Context

After ADR 0309 a variant created in the panel was priced there and still
stocked over the admin API: its inventory item had to be created by inventory
and linked by the product module. The panel's stock form listed every open
location for a variant that had an item, and wrote the first count at a
location as it writes any other. ADR 0207 had given the product module a
narrow writer that another module's service satisfies by name, and a pin in
internal/arch that holds the two together.

## Decision

The product module declares a stock item writer that inventory's service
satisfies, resolved by inventory's service name, and its admin surface gains
`StockVariant`: a variant without an item gets one, named by the variant's SKU
or its id and titled by the product and the variant, and linked. The variant
page offers it to an operator holding both writes, and returns to the variant.

## Consequences

- A product created in the panel is priced and stocked there; it is ready to
  publish once a location holds its count.
- A variant that has an item keeps it, and a bundle, whose stock is its
  parts', is refused before an item is made (ADR 0234).
- The item is inventory's and the link the product's, so the button asks for
  both writes, as the price does (ADR 0309).
- A variant without a SKU names its item by its id; the item's SKU is changed
  over the admin API.
- A write stopped after the item is created and before it is linked leaves an
  item nothing names, as a price set is left (ADR 0309).
- The name the product module resolves pricing's service by is now held to
  pricing's own, as inventory's is; neither was before.

## Rejected

- A catalog flow for the item and the link: the product module already
  carries this shape for prices, and the panel writes through module surfaces.
- An item per location from the panel: a location's count is the existing
  stock form's, and a new item needs none to exist.
