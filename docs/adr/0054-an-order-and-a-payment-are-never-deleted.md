# ADR 0054 — An order and a payment are never deleted

**Summary:** gobit drops the ten `deleted_at` columns of the order and payment
modules, and the money-event surface keeps two moments and gains no third.

- **Status:** Accepted
- **Date:** 2026-09-08

## Context

Ten `deleted_at` columns, six in `order` and four in `payment`, had never been
written. Fifty-six of the modules' eighty-three statements carried `deleted_at
IS NULL`, never once false, and twenty-three indexes were partial on it.

Both modules had already answered the question elsewhere. An order retires by
STATUS and each of its four states carries a dated transition, two held by mirror
CHECKs; `archived` is the state a soft delete would have been asked for.
`payment_sessions.sql` opens with "A session record is NEVER DELETED, its status
changes", and `payment_manual_sessions` never had the column.

Four of the twenty-three indexes were UNIQUE and written as "unique among LIVING
rows". While the column stood, one hand-written UPDATE let the same idempotency
key open a second live row — and on `payments` and `payment_sessions` that key
is what stands between a retried saga step and a second charge.

Measurement: [measurements/0054](../measurements/0054-soft-delete-in-order-and-payment.md).

## Decision

**The ten columns are dropped, and the two modules do not soft-delete.** An
order and a payment are records of something that happened: an order retires by
status, a money record is kept, and the retreat from one is a refund ROW or a
canceled status.

**Only four of the ten are licensed by the precedent.** `fulfillment` 000003's
criterion is a uniqueness rule written as "unique among LIVING rows", reaching
`orders`, `order_return_items`, `payment_sessions` and `payments`; the other six
— `order_line_items`, `order_returns`, `order_claims`, `order_exchanges`,
`payment_collections`, `refunds` — go on the product argument and nothing else.

**The published money-event surface keeps two moments and does not gain a
third.** `authorized_at` is not added.

## Consequences

- **No order or payment record can be hidden**, by any surface or by hand. A
  test order stays in the list; `archived` takes one out of the daily ones.
- **The four uniqueness rules now cover every row**, so an idempotency key can no
  longer be freed by stamping the row that holds it. A migration meeting a
  hand-stamped duplicate fails on the index instead of dropping the guarantee.
- **Twenty-three indexes are rebuilt in the block that drops the columns**, since
  PostgreSQL removes a partial index on a dropped column with no notice.
- **The caution in `order` migration 000001 is answered**, not carried forward:
  `order_summaries` has no `deleted_at`, so the divergence cannot arrive.
- **`GetOrderLineItemsByIDs` lost its JOIN**, which existed only to check the
  order's `deleted_at`, and the erasure and disclosure reads stop being
  exceptions (ADR 0034): there is nothing left to omit.
- **`updated_at` is the authorization moment while a session is authorized**, so
  no column is needed; after capture the moment is gone and nothing asks for it.
- **A gate refuses the columns' return:**
  `TestTheOrderAndPaymentModulesDeclareNoSoftDelete`.

## Rejected

- **Close the four uniqueness holes and leave the columns.** It buys the whole
  second-charge argument at no product cost and it was available; refused because
  the product question is answered here rather than deferred, and ten unwritten
  columns beside an unconditional unique index is a rule next to its own
  contradiction.
- **Write the columns — give the modules a delete.** `GetOrderSummary` would have
  to be bound first; and, by this record's own inference, an issued invoice
  survives erasure (ADR 0032), so a hidden order would sit under a document the
  shop still holds.
- **Keep the columns unwritten and record the decision in prose.** That was the
  state for two rounds; it kept the uniqueness hole.
- **Drop the columns and leave the indexes to PostgreSQL.** It removes them
  silently; the schema then looks untouched and a duplicate key is accepted.
- **Add `authorized_at` and publish a third money moment.** No money moves at an
  authorization, and the field would cross the module read map, a DTO and the
  OpenAPI text for a reader nobody has named.
