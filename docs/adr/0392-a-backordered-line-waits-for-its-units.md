# ADR 0392 — A backordered line waits for its units

**Summary:** A line the checkout lets through without stock leaves a claim the inventory module fills
from the next units that arrive where the order may ship from. It costs a table, a query on every write that raises a sellable quantity, and a level answer that can fall below the count written.

- **Status:** Accepted
- **Date:** 2026-10-05
- **Amends:** [0048](0048-the-four-carried-flags-get-a-reader-not-a-deletion.md), which owed a backordered line nothing, and [0142](0142-both-acts-compute-the-same-target.md), whose target counts every unit bought as deducted

## Context

ADR 0048 lets a backorder-permitting line through when no warehouse covers it
whole. It takes no reservation and is named only in the checkout's execution
record, so units that arrive go to the next shopper. A reserved line is deducted
at the confirm as a `sale` naming the order, and a write-off finds the shelf to
credit by item and order. The cancellation flow counts every unit bought as
deducted, so a written-off backordered line is credited to the shelf whenever
another line of the order sold the same item, which ADR 0223's lines of one
variant make ordinary (D242). Which warehouses may ship an order is
fulfillment's decision (ADR 0010, ADR 0092), and a stock write cannot ask
another module under its lock.

Measurement: [measurements/0392](../measurements/0392-a-backordered-line-waits.md)

## Decision

The checkout's last step records each counted line it let through without stock
as a claim in `inventory_backorders`, naming the order line, its units and the
warehouses fulfillment ranks for the order, and every write that raises a
level's sellable quantity fills the waiting claims eligible there, oldest first,
each whole as a reservation of its order confirmed in that write's transaction.
A write-off withdraws from a waiting claim the units the cancellation flow
counts as never leaving, and both acts put back only what the line's stock lost,
at the warehouse it left.

## Consequences

- Units that arrive where an order waits are deducted for it before the write
  commits, so no shopper, badge or stock alert sees them.
- A level write answers the count after the fill: `stocked_quantity` can be lower
  than the one written, the difference a `sale` naming the claim's reservation
  and the order. The panel's stock form says how many units went.
- A claim is filled whole at one warehouse and records which. It waits until the
  units sellable at one of its warehouses reach it; units short of it stay on
  sale meanwhile, and later, smaller claims are filled first.
- The warehouses are ranked once, when the order is placed: one opened or bound
  to the channel later does not fill the claim, and a claim the checkout could
  not rank names none and is filled from nowhere.
- A claim names its order line by position among the order's lines (ADR 0233),
  checked against variant and quantity; a mismatch records no claim and says so
  in the checkout's warnings. A bundle's part is claimed as a line is.
- A claim is recorded before the confirm and settled against the write-offs and
  live parcels read after it, so a line written off before the checkout
  finished is withdrawn. Both acts read the order's sales before they settle a
  line, so a sale they see means its claim is there.
- A withdrawal leaves the units a live parcel holds, so they are deducted when
  they arrive.
- An item with a waiting claim is not deleted (`inventory_item_owes_orders`).
- `GET /admin/v1/inventory-items/{id}/backorders` lists an item's claims under
  `inventory:read`. The checkout, an arrival, a write-off and a canceled parcel
  are a claim's only writers.
- D242 stays open for orders placed before migration 000008 and for a line whose
  claim the checkout failed to record.
- A filled claim opens no parcel and publishes nothing; the operator opens it as
  ADR 0135 lets them. No level goes negative and no date is promised.

## Rejected

- An operator action that fills a claim: until someone acts, the units go to the next shopper.
- Filling after the stock write commits: the units are on sale until the fill, and the module publishes no event to fill from.
- Subtracting waiting claims from availability: it rewrites ADR 0040's answer for every item, and a claim eligible at two warehouses is counted at both.
- Filling a claim in parts: a part is off sale while the order still cannot go whole, and the claim is pinned to the warehouse of its first part.
- Stopping at the first claim that does not fit: the units it leaves go to the next shopper instead of the next waiting order.
- Asking fulfillment for the warehouse at each arrival: a stock write cannot call another module under its lock.
- Keying a claim by order and item: two lines of one variant withdraw each other's units.
- Withdrawing every written-off unit: units a live parcel holds leave anyway, and the shelf would gain units that shipped.
- Opening the parcel when a claim fills: which parcel and when is fulfillment's decision, and ADR 0135 lets the operator make it.
