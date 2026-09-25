# ADR 0187 — A refund names its cause

**Summary:** A refund row carries the id of the order-side record that caused
it, written in the transaction that writes the row. It costs a column and an
argument on the payment module's refund surface, and it lets the order's books
say why each refund left.

- **Status:** Accepted
- **Date:** 2026-09-25

Measurement: [measurements/0187](../measurements/0187-why-the-money-went-back.md)

## Context

ADR 0186 books a refund against receivable, and the order's side of the books
has to credit receivable with the cause: goods returned, a claim settled, an
exchange withdrawn. The returns workflow refunds through `RefundCollection`
with a free-text reason, and nothing on either side recorded which return,
claim or exchange a refund was for. The order module has no refund id in any
table, and the payment module's reason is prose. The draft asked the workflow
to write the refund ids onto the order's records after the refund. That write
is a second transaction, and a failure between the two would leave money gone
with no cause to find.

## Decision

`refunds.reference` holds the caller's id for the cause, and `RefundCollection`
writes it on every refund row of a split, in the transaction that writes the
row. The returns workflow passes the return's, the claim's or the exchange's
id, and the payment movements the order reads carry it.

## Consequences

The cause cannot be lost apart from the money: the row is written with its
reference or not written. The payment module never reads the reference, as it
never reads a collection's; it is the caller's key.

A refund an operator makes on the payment surface has no reference, and the
order's books will show it as unattributed rather than guess a cause.

The reference is an id, so it is refused with surrounding space, and it is
bounded like the module's other text.

The admin refund listing shows the reference.

The interop's `RefundCollection` gains the argument, so the returns workflow's
port and its test stub change with it; internal/arch pins the two together.
`Service.RefundPayment` keeps its signature for the admin surface, and
`RefundCollection` reaches the same code with the reference.

## Rejected

- **Refund ids written onto the order's records.** It is a second transaction
  after the money moved, and it is the draft this record replaced.
- **A structured prefix in the reason.** The reason is prose an operator reads,
  and parsing it would make a sentence a contract.
- **A cause table in the payment module.** The cause is the order's record; the
  payment module keeps only the key, as it does for a collection.
