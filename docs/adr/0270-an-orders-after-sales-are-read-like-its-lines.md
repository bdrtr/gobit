# ADR 0270 — An order's after-sales records are read like its lines

**Summary:** The order module offers its returns, claims, exchanges and
replacements to the read layer as four entities read per order, and the admin
panel's order page lists them.

- **Status:** Accepted; amended by [0271](0271-the-panel-acts-on-an-orders-after-sales.md), which lets the panel act on the records
- **Date:** 2026-09-30

## Context

The known limits said the panel's order screen showed the after-sales sums and
not the records: each line's units asked back and written off (ADR 0252), but
not whether a return's goods arrived, a claim's status or a replacement's
parcel, because the order module offered those records to no read. The panel
reads through the read layer and knows no module (ADR 0011). The order entity
leaves out the lines because a record has one join key and an order has many
lines, which is why the lines are an entity of their own.

## Decision

The order module registers `order_return`, `order_claim`, `order_exchange` and
`order_replacement`, each listed newest first with the `order_id` filter
required, answering the record's status, money, moments and, for a return and
a replacement, its lines. The panel's order page reads the four for the order
it shows and prints them newest first in one After-sales section, under the
order's own privilege.

## Consequences

- An operator sees on the order page what came back and where it arrived, how
  a claim is settled and when, what a replacement sends, from where and in
  which parcel, and who pays an exchange's difference.
- Anyone reading the read layer — a report, a workflow, a library user — gets
  the same records; a replacement is reached through its claim or exchange and
  takes the order filter all the same.
- A read across orders is refused (422): the tables are indexed per order, and
  a report over every open claim is a reader nobody has asked for yet.
- The metadata, a claim's evidence and a replacement line's parts are left
  out; the first has no shape this module states, the other two have endpoints.
- The page reads at most 25 records of each kind and says so beyond that; a
  kind that cannot be read costs the section, not the page.
- The panel still cannot act on a record: receiving a return or settling a
  claim is an API call. The known limit now says that.
- The panel's four entity names are pinned in internal/arch, and a
  replacement's `location_id` joins the provider fields the panel is held to.

## Rejected

- The records as list fields on the order entity: the reason the lines are not
  there.
- One entity for every kind: most of its fields would be empty on most records.
- A panel surface returning the records as JSON, as the sessions screen has:
  the read layer is where a module's records are read, and it serves every
  reader.
