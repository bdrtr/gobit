# ADR 0274 — A store credit names the order it compensates

**Summary:** An issue of store credit can name an order in `order_id`, the row
keeps it, and the history is read for one order.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

The known limits said store credit named no cause: `reference` is free text,
so "this is the compensation for return R-19" was a convention rather than a
link, and nothing could answer which credits an order had been compensated
with. Credit is issued by an operator through the admin API, most often
because of an order: a late delivery, a damaged parcel, a return taken back as
credit. The payment module already keeps other modules' identifiers as columns
it does not check (a collection's customer, a session's reference), and the
order module keeps a collection's id on an exchange the same way.

## Decision

`POST /admin/v1/store-credits` takes an optional `order_id`, which the issue
row keeps and the schema allows on an issue alone. The history takes
`order_id` to list the credits issued for that order, and every row says the
order it names.

## Consequences

- An operator asks what an order has been compensated with and gets the rows,
  and a report reads the relation from a column rather than parsing a
  reference.
- The order is not looked up: it is another module's record (Principle 2.2),
  and a mistyped id is stored as typed and found by nobody.
- A hold, a release, a refund or an expiry names no order; the schema refuses
  one, since those rows are money moving within the balance.
- The history's order filter is a narrowing of one customer's ledger in one
  currency, so it still needs the customer and the currency.
- `reference` stays free text for a finer cause, a return or a ticket.
- Migration 000015 of the payment module adds the column, its CHECK and a
  partial index; rolling it back forgets the orders and keeps every credit.

## Rejected

- A Module Link from the order to the ledger row: the row is no read-layer
  entity, and a link nothing expands is a second place to keep one fact.
- Naming a return or a claim instead of the order: the order is what every
  cause belongs to, and the finer one fits `reference`.
- A listing across customers by order: the order has one customer, whom the
  caller already knows.
