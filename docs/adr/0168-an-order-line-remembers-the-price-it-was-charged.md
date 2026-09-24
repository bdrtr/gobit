# ADR 0168 — An order line remembers the price it was charged

**Summary:** Pricing's bulk answer names the price row its ladder picked and
that row's list, the checkout carries both into the order, and each order line
keeps them in three nullable columns. NULL means unknown, and a line's origin is
published on both order views.

- **Status:** Accepted
- **Date:** 2026-09-24

Measurement: [measurements/0168](../measurements/0168-what-a-line-was-charged.md)

## Context

An order line kept the amount it was charged and not which of the variant's
prices produced it. The ladder picks one row among base prices, sale and
override lists, quantity tiers and rules, and equal amounts from different rows
cannot be told apart afterwards. Since ADR 0167 a price row can be read back
after it is replaced, so an id is enough to recover everything the row said.

## Decision

The price row's id, and for a list price the list's id and type, travel from
pricing through the cart's totals round and the checkout's plan into
`order_line_items`, where a line holds none of them, a price id alone, or all
three. A line with none is sold with an unknown origin.

## Consequences

The line answers "was this sold at the sale price" without joining pricing, and
the price id opens the rest of the row in pricing's history. No foreign key:
the row is deleted when its set is replaced (ADR 0047), and the line names what
was true when it was sold.

Two hops of the route drop a field they do not know, so all of them changed in
one commit (ADR 0096's shape). Each hop has a witness in its own package, and
one end-to-end test proves they agree.

Every line sold before the upgrade has no origin, and so does a line placed by
a saga recovered from a plan written before it. The money was taken either way,
so the order is placed and the origin is NULL, which the API leaves out rather
than rendering as a base price.

The service refuses an incoherent origin as a 400, and a CHECK holds the same
three shapes for any other writer. Its disjuncts test `IS NOT NULL` before they
compare, because a CHECK passes on NULL.

The cart does not keep the origin, for ADR 0109's reason about the promotions
that applied: every round recomputes it and the checkout takes it from its own.

Which promotion reduced a line is not recorded. The engine reports discounts
per line and per promotion, never both at once, and a split would be invented.

## Rejected

- **Keeping only the list type.** The price id is what opens the history.
- **A foreign key to `price`.** The first reprice after a sale would refuse it.
- **Refusing a line with no origin.** It trades a sold order for a tidy column.
- **Keeping the origin on the cart.** A second source of truth (ADR 0109).
- **Per-line promotion attribution.** The engine has no such breakdown.
