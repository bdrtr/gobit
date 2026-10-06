# A document's tax on the books — measured 2026-10-06

Evidence for [ADR 0419](../adr/0419-a-documents-tax-is-on-the-books-at-the-document.md)
and gap D262. Read on a tree at 85389ad9; every Go command ran as
`GOTOOLCHAIN=go1.26.6 GOMAXPROCS=4`, the e2e against Docker's
`postgres:16-alpine` through testcontainers.

## 1. The reproduction, before the change

`internal/e2e/invoice_amendment_test.go` places ADR 0406's order on the
production wiring: a line of 10 000 taxed 1% and two units of 5 000 taxed 20%,
an untaxed carriage of 3 000, a dearer delivery of 1 500 paid and documented,
then the 20% line returned, refunded 12 000 and documented. The return's
document gives back 2 000 of tax, the 20% row's own. The test now reads the
order's own entries in the order journal over the test's window, each account
as its credits less its debits.

| Account | Expected | On 85389ad9 |
|---|---|---|
| tax_payable | 2 100 − 2 000 = 100 | 2 100 |
| sales_returns | −10 000 | −12 000 |

The journal booked the refund whole to sales_returns and never touched
tax_payable after the placement: the books owed the 2 000 the document gave
back.

`internal/e2e/document_tax_books_test.go` places an order with the offline
method, invoices it, writes a credit of half its total, documents the credit
and cancels the order while the document stands. The credit's document gave
back 1 000 of tax.

| Account | Expected | On 85389ad9 |
|---|---|---|
| tax_payable, after the cancellation | −1 000 | 0 |

## 2. After the change

Both tests pass, and so do the steps after them:

- The return's document canceled: tax_payable 2 100 and sales_returns −12 000
  again, read over a window that reaches the cancellation; the window that
  ended before it reads exactly as it did.
- The credit's document canceled after the order: tax_payable 0.

The worked example of the return, as entries on the order:

| Entry | Dr | Cr |
|---|---|---|
| order_placed | receivable 25 100 | sales 20 000, tax_payable 2 100, shipping 3 000 |
| delivery_upgraded | receivable 1 500 | shipping 1 500 |
| return_refunded | sales_returns 12 000 | receivable 12 000 |
| tax_corrected (the return's document) | tax_payable 2 000 | sales_returns 2 000 |

The delivery's document carries no tax, since carriage is untaxed, and writes
no line.

## 3. Why updated_at fills voided_at exactly

The backfill in invoice migration 000007 reads a rejected or canceled
document's updated_at as the moment it was voided. The argument, read from
the tree:

- `SetInvoiceStatus` (internal/modules/invoice/queries/invoice.sql) is the one
  statement that writes updated_at; the erasure handle's rewrite leaves it as
  it was, as its own comment says.
- `models.Status.CanMoveTo` lets neither rejected nor canceled move again, so
  the last status write of a voided document is its voiding.

`TestMigration000007FillsTheMomentOfDocumentsVoidedBefore` writes a rejected,
a canceled and an accepted document at version 6 on its own container, each
with an issued_at, an updated_at and the database's now() all different,
migrates over them, and reads the two voided ones' voided_at equal to their
own updated_at and the accepted one's empty. The first version of the test
gave issued_at and updated_at the same value, and a backfill from issued_at
passed it; the independent review found it.

## 4. What the read costs

The journal makes one more read of the invoice module per window: the
amending documents that name an act and were issued or voided inside it,
served by two partial indexes on issued_at and voided_at. A documented refund
costs one read of the payment module's refunds by primary key and one of this
module's causes; a credit line or a delivery change one read of this module.
