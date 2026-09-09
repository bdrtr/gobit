# ADR 0105 — A credit lowers what is owed, not what was sold

**Summary:** An order can carry credit lines, which lower the outstanding amount
without touching the order's total. It costs a table, a fifth query on the order
read, and a ceiling checked under the order's lock.

- **Status:** Accepted
- **Date:** 2026-09-10

## Context

Nothing could reduce what an order owed. A goodwill gesture, a price match or a
compensation agreed after the sale had no record: the only figures were the
order's total, what was paid and what was refunded, and none of them is the
right place for a concession.

Lowering the total was the obvious move and it is wrong. That figure is the
cart's snapshot — what was sold and at what price — pinned to the order's own
lines by `orders_totals_consistent`. Migration 000001 calls the order "the
permanent answer to the question what was sold at that moment". Editing it would
make the order disagree with its lines and would erase the fact that a
concession was made at all.

## Decision

`order_credit_lines` holds an amount, a reason and a note. `Outstanding` becomes
`total - credited - (paid - refunded)`, and the credited total is the SUM of
those rows rather than a column.

## Consequences

A concession is now a record with a reason on it, which is what makes it
answerable six months later. The reason is required and trimmed; a reason of
three spaces is a reason somebody thought they gave.

The running total is not stored, for the reason 000001 gives about the
outstanding amount: a number kept in a second place can go stale, and this one
is a sum the database can take itself. The order read pays a fifth fixed query
for it, inside the same snapshot as the others — a credit written between two
reads would otherwise produce an outstanding amount that never existed.

The ceiling is the order's TOTAL, not its outstanding amount. A credit granted
after the customer has paid is legitimate and turns the outstanding amount
negative, which is this module's word for "the shop owes the customer" and is
what a refund settles. Writing off more than the sale was ever worth is a
data-entry error, and no verb here could settle the difference.

It is checked under the ORDER's lock. The check reads a sum and then writes a
row; under READ COMMITTED two concurrent credits would each read the sum before
the other committed, both would pass, and the pair would exceed the ceiling —
the shape ADR 0091 measured.

A credit cannot be withdrawn. There is no update and no delete: the record is
that a concession was made. Reversing one would be a second line in the other
direction, which this table refuses because the amount is strictly positive —
a charge after the sale is a different act with a different authorization.

## Rejected

- **Lowering `orders.total`** — the order would disagree with its lines and the
  concession would leave no trace.
- **A `credited_total` column on `order_summaries`** — a second copy of a sum,
  free to go stale.
- **A negative amount for a reversal** — one column meaning both credit and
  charge makes "credit" the word for neither.
- **The outstanding amount as the ceiling** — it would refuse the legitimate
  case of a concession made after payment.
