# ADR 0189 — A refund's cause gives back revenue

**Summary:** The order journal reads the refunds that name one of the order's
returns or claims and books what each gave back against receivable. It costs a
read of the payment module per journal, and with it the two journals close a
returned order.

- **Status:** Accepted
- **Date:** 2026-09-26

Measurement: [measurements/0189](../measurements/0189-the-books-close.md)

## Context

ADR 0188 left a returned order's receivable open. The payment journal debits
receivable when money goes back, and nothing on the order's side credited it,
because the order kept no record of what a return or a claim refunded. ADR 0187
put the cause on every refund row: the return's, the claim's or the exchange's
id. The order module owns those records, and the payment module owns the
amounts and the moments.

## Decision

The payment module answers which refunds in a window name a cause, and the
order journal books each one that names its own return as sales_returns and
each that names its own claim as claim_allowances, against receivable, at the
refund's amount and moment. The order module reads it through its own port,
resolved lazily, as it reads the spending rule.

## Consequences

An order paid, returned in part and refunded owes nothing across the two
journals, and what the return gave back is exactly what its refunds sent.

A refund that names an exchange is not an entry: an exchange's difference is
paid into a collection of its own, outside these books (ADR 0188). A refund
that names nothing of this module is not an entry either, and an operator's
refund from the payment surface names nothing. Both leave receivable open by
their amount, which is what books should show for money sent back without a
recorded reason.

A refund in another currency than the order it names is an error rather than
an entry.

Without the payment module the journal books no refunds, and the order journal
reads as it did under ADR 0188. A registration that does not satisfy the port
is a wiring error and is returned.

The payment module's answer is JSON with field names the order module repeats.
The compiler cannot hold the two together, so the e2e test does: it closes a
returned order's books on the production wiring, and a renamed field turns it
red.

## Rejected

- **A third endpoint over both journals.** Each module publishing its side
  already sums to closed books, and a combining endpoint would be a second
  place to name the accounts.
- **The return's planned refund amount.** It is set when the return is opened
  and never updated, and the refund can differ from it.
- **Booking an exchange's difference on the order.** It needs the exchange's
  own collection read beside the order's, which is a record of its own.
