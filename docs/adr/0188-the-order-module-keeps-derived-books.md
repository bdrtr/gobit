# ADR 0188 — The order module keeps derived books

**Summary:** The order module publishes its facts as a double-entry journal,
derived when read from orders placed, orders canceled and credit lines. It
costs a second chart of accounts and three time indexes, and with the payment
module's journal it closes what a buyer owes.

- **Status:** Accepted
- **Date:** 2026-09-25

Measurement: [measurements/0188](../measurements/0188-what-the-buyer-owes.md)

## Context

ADR 0186 put the payment module's movements on derived books, where a capture
credits receivable and a refund debits it. The debit side of receivable, what a
buyer owes because an order was placed, is the order module's, and so is the
revenue, tax and shipping the order recorded. ADR 0187 made every refund name
its cause, so the order's side of a return can be read later. Nothing on the
order side was a book.

## Decision

`GET /admin/v1/order-journal` derives one balanced entry from each order placed,
each order canceled and each credit line in a window, over six accounts, and
writes nothing. `receivable` is spelled as the payment module's, and
internal/arch binds the two.

## Consequences

An order placed debits receivable with its total and sales_discounts with its
discount, and credits sales, tax_payable and shipping with its subtotal, tax
and shipping. The table holds the order to that identity, and an order that
breaks it is an error rather than an unbalanced entry. A cancellation is the
same entry the other way, and a credit line debits credit_allowances against
receivable. A zero amount writes no line, and an order of nothing is no entry.

With both journals, an order paid in full owes nothing: its placement debits
receivable and its capture credits it. An order canceled before payment owes
nothing, and a credit line followed by an operator's refund balances too.

A line cancellation is not an entry, because it records a quantity and no
amount. Its money arrives as a credit line or a refund.

The revenue a return, a claim or an exchange gives back is not on these books
yet. Its refund debits receivable on the payment side, and the cause ADR 0187
records is what a later reading needs to credit it. Until then, a returned
order's receivable stays open by the refunded amount.

An exchange's difference is paid into a collection of its own, which the
order's payment link does not reach, so it is outside these books too.

Three indexes serve the window. The window's bounds are the payment journal's:
93 days and 10,000 facts, refused rather than cut.

## Rejected

- **One books endpoint over both modules in this record.** It needs the refund
  causes read back into revenue, a decision of its own. Each module publishing
  its own side first keeps each chart with the records it describes.
- **Line cancellations valued at the line's unit price.** The price charged
  was per line, with tax and discount spread over it; valuing a quantity would
  invent an amount the order never recorded.
- **The order's summary totals as the source.** They are merged in place and
  keep only their latest value.
