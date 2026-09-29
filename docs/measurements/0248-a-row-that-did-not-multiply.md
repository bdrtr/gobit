# A row that did not multiply — measured 2026-09-30

The evidence behind [ADR 0248](../adr/0248-an-invoice-says-its-prices-include-tax.md)
and D171.

## 1. What a document said

| Place | Before |
|---|---|
| `internal/modules/invoice/service/issue.go`, `LineInput.Subtotal` | "Subtotal is UnitPrice x Quantity" |
| the same file, `validateAmounts` | checked each row's total against its subtotal, discount and tax, and the document against its rows; never the unit price against the quantity |
| `internal/modules/invoice/models/models.go`, `Line.Subtotal` | "Subtotal is UnitPrice x Quantity" |
| `internal/workflows/invoicing/issue.go`, `lines` | copies each order line's unit price and subtotal as they are |
| the admin endpoint's decoder | refuses a field it does not know, so no caller could say the prices included their tax |

Since ADR 0246 an inclusive order's line keeps the sticker as its unit price
and the net as its subtotal, so the end-to-end order of section 3 would have
been filed as unit price 11,999, quantity 2, subtotal 19,999 and tax 3,999,
with nothing on the document to say which reading the row took.

## 2. The shape

| Piece | Now |
|---|---|
| `invoices.prices_include_tax` (migration 000005) | written with the document; false on every document before it |
| `validateLineQuote` | subtotal = unit price x quantity, or subtotal + tax where the flag is set; a product past int64 is refused by its quotient, and so is a negative unit price, whose quotient is below zero |
| the order's invoice surface, the invoicing flow's order and document, the invoice interop, the admin request and the document response | carry `prices_include_tax` |

The carriage row the flow adds has no tax, so it multiplies under either
reading.

## 3. The tests

| Test | Holds |
|---|---|
| `TestATaxInclusiveStickerIsWhatTheOrderCharges` (end to end) | the inclusive order is invoiced over HTTP, and the document says `prices_include_tax` with 11,999 as the unit price and 19,999 as the subtotal |
| `TestARowIsHeldToItsQuote` | an inclusive row is filed with the flag and kept on the document; without the flag, not multiplying, a tax counted on top, a product that wraps 64 bits to zero, one past int64 and a negative unit price are refused |
| `TestADocumentSaysWhetherItsPricesIncludeTax` | the admin endpoint takes the flag and answers with it, and refuses the flag on a row whose tax was added on top |
| `TestTheDocumentSaysWhetherItsPricesIncludeTax` | the invoicing flow passes the order's flag either way |
| `TestTheInvoiceSurfaceSaysWhetherThePricesIncludeTax` | the order's invoice surface carries it by the name the flow reads |

The three overflow and sign cases are written with the subtotal the product
taken plainly in int64 would have, wrapped or negative, so each would pass the
comparison without the quotient.

## 4. Mutations

| # | Mutation | Killed by |
|---|---|---|
| V1 | the inclusive branch never taken | `TestARowIsHeldToItsQuote`, `TestADocumentSaysWhetherItsPricesIncludeTax` |
| V2 | an exclusive row not checked | `TestARowIsHeldToItsQuote` |
| V3 | an overflow not checked | the same |
| V4 | the overflow checked one past the bound | the same |
| V5 | a negative unit price let past the quotient | the same |
| V6 | the quote never checked | the same, and the admin endpoint's test |
| V7 | the document drops the flag | the same |
| V8 | the repository does not write the flag | `TestATaxInclusiveStickerIsWhatTheOrderCharges` |
| V9 | the repository does not read the flag | the same |
| V10 | the invoice interop drops the flag | the same: the document is refused |
| V11 | the admin request drops the flag | `TestADocumentSaysWhetherItsPricesIncludeTax` |
| V12 | the response drops the flag | the same |
| V13 | the invoicing flow drops the flag | `TestTheDocumentSaysWhetherItsPricesIncludeTax` |
| V14 | the order's surface drops the flag | `TestTheInvoiceSurfaceSaysWhetherThePricesIncludeTax` |

Fourteen mutants, all killed. The first draft took the product in 128 bits,
and gosec refused its conversion of the quantity to unsigned; the quotient
replaced it. A separate negative-price guard beside the quotient then survived
as an equivalent mutant, since the quotient refuses a negative price, and was
removed; the negative case was rewritten with the product as its subtotal, so
that only the quotient's sign refuses it, and V5 lets it past. Two spellings of
V3 failed to compile and were rewritten until one compiled.
