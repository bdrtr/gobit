# ADR 0344 — The panel shows an invoice and moves its status

**Summary:** An invoice's page shows the document's parties, rows and
totals under `invoice:read` and moves its status under `invoice:write`
through `invoice.admin`, from the status the page was drawn in; a move from
a status the document has left is refused with `invoice_status_moved`.

- **Status:** Accepted; amended by [0406](0406-an-amount-moved-after-the-sale-is-a-document.md), which keeps a sale from being canceled while a document amending it stands
- **Date:** 2026-10-02

## Context

The Invoices screen lists the documents (ADR 0343), but a document's rows
were read and its status moved only through the admin API. Without an
e-invoicing provider an operator records by hand that an invoice was sent,
accepted or rejected, or cancels one; and the API's move checks the
transition from whatever status the document is in, so an operator who
read an issued invoice and cancels it may cancel one a colleague sent
meanwhile.

## Decision

A status move may name the status the caller read the document in, and is
refused with `invoice_status_moved` when the document has left it; the
panel's invoice page offers the moves its status may make from the one it
was drawn in. The page shows the document's parties, rows and totals as
issued, and the order's page and the Invoices screen link to it.

## Consequences

- An operator moves an invoice where they read it, and a document moved
  meanwhile is refused rather than moved on from a status they did not
  see.
- The moves offered are the module's transition table, sent with the
  document, so the panel holds no copy of it.
- A rejection and a cancellation say why, as through the API.
- A move made here names no provider or external id; those stay a
  provider's to write.
- The API's move names no read status and checks as before.

## Rejected

- Matching on the moment the document was last written: the status is
  what the move depends on, and a write that leaves it alone does not
  change what the operator may do.
- Editing the parties or the rows: they are copies taken when the document
  was issued, and a correction is a new document, not an edit.
- One button per move: the reason a rejection and a cancellation need
  would have to be typed beside each.
