# ADR 0248 — An invoice says its prices include their tax

**Summary:** An invoice records whether its prices included their tax, copied
from the order, and holds each row's subtotal to its unit price and quantity by it.

- **Status:** Accepted
- **Date:** 2026-09-30

Measurement: [measurements/0248](../measurements/0248-a-row-that-did-not-multiply.md)

## Context

[ADR 0246](0246-a-cart-and-an-order-say-their-prices-include-tax.md) let a
tax-inclusive market sell, and its orders keep each line's unit price as the
sticker and its subtotal as what is left once the tax is taken out. The
invoicing flow copies an order's lines onto the document as they are, and the
document said nothing about which reading a row took: a provider transmitting
"unit price 11,999, quantity 2, subtotal 19,999" could not tell an included tax
from a wrong row. The invoice module wrote that a row's subtotal is its unit
price times its quantity and never checked it, so a row that did not multiply
was filed (D171).

## Decision

The invoice records whether its prices include their tax, copied from the order
or given on the admin endpoint, and holds each row's subtotal to its unit price
and quantity by it, as the cart and the order do. A row whose product an int64
cannot carry, or whose unit price is negative, is refused.

## Consequences

- An inclusive order's document says `prices_include_tax`, and its rows keep
  the order's figures: the sticker as the unit price, the net as the subtotal.
  How a provider prints a net unit price is the provider's.
- `POST /admin/v1/invoices` now refuses a row that does not multiply, which it
  filed before. A document issued before is not read again.
- The column defaults to false, which is true of every document before it.
- The carriage row carries no tax and multiplies under either reading.

## Rejected

- **Printing a net unit price on the row.** It is rarely a whole minor unit,
  and the document would stop being its order restated.
- **Carrying the flag and leaving the rows unchecked.** A flag nothing holds
  the rows to is a claim, not a record.
- **The flag on each row.** A document has one market, as its order does.
