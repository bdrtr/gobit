# ADR 0401 — An order line keeps what its goods cost

**Summary:** A variant carries a unit cost per currency, and the checkout copies the one in the order's currency onto each line.
The admin order and the admin order list read the margin each order was placed at; the storefront reads neither.

- **Status:** Accepted
- **Date:** 2026-10-05
- **Amends:** [0399](0399-stock-on-its-way-has-a-date-the-storefront-shows.md), whose receipt still names no cost; a variant's unit cost is not a receipt's

Measurement: [measurements/0401](../measurements/0401-what-the-goods-cost.md)

## Context

A shop selling through gobit could not say what an order earned. No table held
a cost, and the order journal books sales, discounts, tax and shipping but no
cost of goods (ADR 0188). A cost read when a margin is read rewrites a closed
sale whenever the catalog moves, which ADR 0211 refused for the gift card flag.
The checkout is the one place that sees the sale and its catalog at once, and it
takes no observer (ADR 0385).

## Decision

A variant carries at most one unit cost per currency, net of tax, in
`product_variant_cost`, written whole by `PUT /admin/v1/variants/{id}/costs`,
and the checkout copies the one in the order's currency onto the order line as
`unit_cost`, NULL when there is none. The admin order and each row of the admin
order list publish `placed_margin` for an order with a line that is not a gift
card: the net sales of those lines less their cost, computed by one SQL
aggregate, stating no cost or margin while one such line has none or the cost
passes an order total's bound.

## Consequences

- The cost is the variant's at the sale; changing it later changes no order. A
  line sold before order migration 000041, or placed from a plan saved before
  it, has none.
- The checkout makes no new call to the catalog: `unit_costs` is one more field
  of its first variant read, and the catalog answers it with one more batch
  query.
- The cost list is read as strictly as the title: a list that does not read
  refuses the checkout before any step runs, as a failed variant read does.
- A cost is per currency, as a price is; gobit converts nothing.
- Sales are `subtotal - discount_total` per line, the net base the tax was
  taken from, so the margin is net of tax in either market (ADR 0246).
- `placed_margin` is the margin at placement. Cancellations, returns, credits,
  provider fees and carrier costs do not move it; exchange and replacement
  goods are outside it, and an addition is an order with its own. An order
  whose lines are all gift cards has none.
- A bundle line costs the bundle's own entry, not its components'.
- `product:read` reads costs and `order:read` reads line costs and margins; no
  field varies by scope.
- The variant record carries `unit_costs` only to a reader that names it; a
  reader naming no fields does not receive it, as ADR 0399 keeps inventory's
  per-warehouse fields (D258).
- The cost has its own table and model. The admin order shadows the
  storefront's lines with lines of its own, and an admin list row is a type of
  its own that adds `placed_margin` to the storefront's order; neither the
  storefront's product and order nor the invoice's order detail carries a cost.
- An arch gate reads every JSON name a production struct publishes (its tag's
  name, or its exported Go name), every type a published or embedded field
  holds, and every name in the GraphQL schemas, and allows a cost or a margin
  only on the types it names. A cost put into a map or an `any` at run time is
  not read.
- Two concurrent writes of one variant's costs keep the later one whole: each
  holds the variant's row, as a bundle write does (ADR 0234).
- The costs are not in the product's revision view (ADR 0221), as add-ons are
  not.

## Rejected

- A column on `product_variant`: a cost is one row per currency, and the storefront product embeds the variant model.
- The cost beside the prices: the storefront variant carries pricing's record as it came, and the ladder chooses one price where a cost is not chosen.
- The cost on the inventory item: an uncounted variant has none, and a bundle's stock is its components'.
- The catalog's cost read when the margin is read: it rewrites a closed order.
- A margin over the costed lines alone: it is not the order's margin.
- A stored order-level cost total: a second copy to check against the lines, for a list one aggregate already serves per page.
- A lenient cost read: a cost list in the wrong shape would leave every line uncosted with no signal.
- A scope of its own for cost fields: it would split one order record by reader.
- The embedder deriving it from `order.placed`: the event names no line.
