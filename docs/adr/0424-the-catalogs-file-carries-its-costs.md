# ADR 0424 — The catalog's file carries its costs

**Summary:** The product CSV carries a variant's unit cost in each currency a region sells in, and an import writes the costs its cells name.
It costs one read of the costs per page of the export and one locked write per costed row of an import.

- **Status:** Accepted
- **Date:** 2026-10-06
- **Amends:** [0204](0204-the-catalog-leaves-as-csv.md) and [0205](0205-a-catalog-import-is-a-record-a-job-works-through.md), whose columns stop at the prices, and [0412](0412-the-panel-shows-what-an-orders-goods-cost.md), whose file carried no cost

## Context

ADR 0401 put a unit cost per currency on a variant, and ADR 0412 lets the
panel write one currency of one variant at a time. ADR 0412 left the cost on
the CSV export and import (ADR 0204, 0205) to its own record, and ADR 0426
names it as C7's remaining slice. A shop that keeps its costs in a
spreadsheet, or changes a supplier's prices across a catalog, has no bulk path
to them. The export writes one `variant_price_<currency>` column per currency
a region sells in, and the import writes those cells through pricing (ADR
0207), an empty cell leaving the price.

## Decision

The export writes, after the price columns, one `variant_cost_<currency>`
column per the same currency, holding the variant's unit cost there in minor
units and empty when it has none. The import writes each non-empty cost cell as
that currency's cost under the variant's lock and leaves an empty cell and the
variant's other currencies as they are.

## Consequences

- A cost in a currency no region sells in has no column: an order is placed in
  a region's currency, so such a cost reaches no order, and the import, which
  never sees it, leaves it.
- The cost columns take no scope of their own. The export already requires
  `product:read`, which `GET /admin/v1/variants/{id}/costs` takes, and the
  import `product:write`, which its `PUT` takes; pricing's scopes stay the
  price columns' alone.
- A cell is a whole number from 0 to 10^12, read before anything of its row is
  written, and a cost on a row with no variant refuses the row, as a price
  does. A cost in a new currency on a variant that carries 50 refuses the row
  where the list is written, after its product, variant and prices: the bound
  is the stored list's, and a row is not one transaction (ADR 0207).
- The file clears no cost. An empty cell means "leave it", as for a price; a
  cost is cleared on the variant page or by replacing the list.
- An import does not compare a cost with the one its file was drawn from, as
  ADR 0412's form does: the file states the catalog, as it does for a price.
- A cost that stands is no change, so an export sent back unedited writes
  nothing. A file without cost columns is read as before.
- The sales report still carries no cost (ADR 0426).

## Rejected

- A column per currency any variant has a cost in: the header is written before the first page, and such a currency reaches no order.
- A marker such as "-" that clears a cost: a price cell has none, and one cell meaning two things in two column families invites the wrong one.
- A scope for the cost columns: costs are the product module's, under the scopes the export and import already take.
- Comparing each cost with the file's read: the export's file is not a form, and a price cell compares nothing either.
