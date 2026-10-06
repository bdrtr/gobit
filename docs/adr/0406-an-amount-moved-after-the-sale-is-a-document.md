# ADR 0406 — An amount moved after the sale is a document amending it

**Summary:** A credit, a delivery change and a return's or a claim's refund are issued on request as a document naming the order's sale document and each row it moves.
It costs a number per act and a row ceiling in the invoice module; an exchange stays on no document, and the journal still books no tax after the sale.

- **Status:** Accepted; amended by [0419](0419-a-documents-tax-is-on-the-books-at-the-document.md), whose order journal books a document's tax at the document
- **Date:** 2026-10-06
- **Amends:** [0394](0394-a-sold-lines-price-and-tax-are-not-raised.md), whose D247
  consequence this replaces, and [0344](0344-the-panel-shows-an-invoice-and-moves-its-status.md),
  whose cancellation of a sale now waits for its amendments

Measurement: [measurements/0406](../measurements/0406-what-an-order-documents.md)

## Context

The invoicing flow prints an order's sold lines and the carriage it was sold
with, and the order link binds one document to an order (D247). After the sale
the order journal books a credit, a cheaper or a dearer delivery, an exchange's
funding and a return's, a claim's or an exchange's refund; none reaches a
document. Turkish VAT corrects a sale by a further document at the sale's rate
that names it, and documents a return apart from a price changed later. The
invoice module has a `refund` kind that names no sale, and the link's
`from_uniq` is the one bar to two sale documents on an order.

## Decision

An amending document names the sale document it amends and the row each of its
rows moves, the sale's or one a live charge on it added, and the invoice module
holds every row's refunds to what the row and its later charges carried, in
amount and in tax. The invoicing flow issues one per act on request, a `sale`
for a paid dearer delivery and a `refund` for a credit, a cheaper delivery or a
return's or a claim's refund.

## Consequences

- `amends_invoice_id`, `amendment_reason` (`returned`, `price_lowered`,
  `price_raised`) and `amends_line_id` say what is amended and why; a provider
  sends a return as its regime's return document and the rest as a price
  difference.
- The flow's `amendment_key` names the journal entry, and a unique index over
  live documents holds an act to one; a second press spends no number, and a
  rejected document frees its act.
- An amendment amends a live `sale` that amends nothing, in its currency and
  price convention, at each row's rates, prints its buyer, and moves no
  negative figure. The sale is locked while its rows are read; it is not
  canceled under a live amendment, nor a charge under a refund relying on it.
  A charge the receiving side rejects is recorded and can leave refunds above
  their row.
- A refund names its sale on every route; refunds issued before name none and
  are under no row's ceiling. An amending `sale` and the key come only through
  the flow, so no route raises a sold line and ADR 0394 stands.
- A delivery falls on the carriage rows, a dearer one on a sale that shipped
  free adding its own; a return on its lines by the units returned, then on the
  carriage; a credit or a claim on every row but a gift card's, by what each
  has left. The request may name the rows; an amount that fits none is refused.
- A row is one unit carrying its share. Its tax is rounded on what its row has
  given back so far, never above the part, and the part that empties a row
  takes the tax it has left up to its own amount; a refund issued outside the
  flow at a lower tax ratio can leave tax no later part gives back.
- `POST /admin/v1/orders/{id}/invoice/amendments` issues an act's document,
  its GET lists the acts with theirs, the order page offers both, and an
  amendment's page links its sale.
- An act written before the sale document is amended like any other, so a
  shop that invoices after a delivery change issues two documents.
- An exchange's funding and refund are on no document (D247 stays open).
- The order journal books each act whole to revenue, so `tax_payable` keeps
  the tax a refund document gives back (D262).
- When a shop documents an act, within its law's period, is its policy.

## Rejected

- Widening `order_invoice`: it drops `from_uniq`, the one bar to two sale documents on an order.
- A ceiling per document: one row could give back more tax than it charged.
- A unique index over every status: a rejected document would hold its act.
- Booking the tax in the journal here: it reverses ADR 0203's whole booking, and whether a correction follows the act or its document is a decision of its own.
- Folding earlier acts into the sale document: it copies the order, and the act's key would need a second home.
- Splitting an exchange over the sale's rows: it prints rates its goods never carried.
- Rounding each part on its own: the part that empties a row collects the others' rounding and can take more tax than itself.
- Refusing a dearer delivery on a sale that shipped free: the delivery changed back would stay undocumented.
- Issuing on each act: the flow issues when someone decides.
