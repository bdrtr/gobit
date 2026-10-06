# ADR 0419 — A document's tax is on the books at the document

**Summary:** The order journal moves an amending document's tax between tax_payable and the account its act was booked to, at the document's issue and back at its voiding.
It costs a third module read by the journal and a voided_at column, and an act nobody documents corrects no tax.

- **Status:** Accepted
- **Date:** 2026-10-06
- **Amends:** [0188](0188-the-order-module-keeps-derived-books.md) and [0406](0406-an-amount-moved-after-the-sale-is-a-document.md), whose journal booked each act whole

Measurement: [measurements/0419](../measurements/0419-a-documents-tax-on-the-books.md)

## Context

The order journal books a credit, a delivery change and a return's or a claim's
refund whole to a revenue or contra account against receivable, and credits
tax_payable only at placement. ADR 0406 documents each act as an amending
document whose rows give back or charge tax at the sale's rates, split by the
invoicing flow. Turkish VAT corrects a sale's tax through that document, and
the books kept owing the tax it gave back (D262). The invoice module kept no
moment for a document's rejection or cancellation.

## Decision

An amending document that names an act moves its tax_total between tax_payable
and the account its act was booked to, at its issued_at, and the same lines go
back at its voided_at when it is rejected or canceled. An act no document names
and a document naming no act correct no tax.

## Consequences

- A refund document debits tax_payable and credits sales_returns,
  credit_allowances, claim_allowances or shipping; a charge document the other
  way. Carriage is untaxed, so a delivery's document writes no line today.
- The tax follows the document's own split, rows and rounding; the journal
  computes none.
- issued_at is the issuing process's clock and voided_at the database's
  (ADR 0053), and nothing orders the two. A window that closed before any
  write still in flight reads the same after a later document or voiding; a
  voiding stamped at its transaction's start can land behind a lock wait, as
  ADR 0241 accepts for the journal.
- One read is no snapshot across modules: a window reaching now can show a
  document's correction before its act, as it could a refund.
- Invoice migration 000007 adds voided_at, stamped by the status write and
  filled for documents already rejected or canceled from updated_at, which
  only that write moves.
- The journal reads the invoice module's documents and, for a refund's act,
  the payment module's refund by id; without the invoice module it books none.
- A document whose act the journal cannot find, or whose currency is not its
  order's, refuses every read of a window holding its moments until its
  retention ends, rather than leave its tax out. The invoice module refuses a
  key naming a kind the journal does not book, and the invoicing flow keys
  only its order's acts in the order's currency, so no writer in the tree
  makes one; unregistering the payment module under a documented refund would.
- An order canceled under a live refund document owes negative tax on these
  books; cancel the refund document, then the sale.
- A document issued on the admin route naming no act gives back tax the books
  keep owing.
- An exchange is on no document and its figure carries no tax (ADR 0203).

## Rejected

- Booking at the act at the order's tax ratio: an act carries no tax, and the split is the document's.
- Booking at the act with the document's tax: a later document would restate a closed window.
- A journal in the invoice module: the entry's other side is the order's account.
- Reading updated_at as the voiding moment: a housekeeping column no constraint ties to the status.
- Booking a document that names no act: no entry debited the account it would credit.
- Skipping a document the journal cannot place: the books would balance and owe the wrong tax.
