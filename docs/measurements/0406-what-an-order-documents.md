# What an order documents — measured 2026-10-06

Evidence for [ADR 0406](../adr/0406-an-amount-moved-after-the-sale-is-a-document.md)
and gaps D247 and D262. Read on a tree at 6a84f21c; every Go command ran as
`GOTOOLCHAIN=go1.26.6 GOMAXPROCS=4 nice -n 19 ionice -c3`, the e2e against
Docker's `postgres:16-alpine` through testcontainers.

## 1. The reproduction, before the change

`internal/e2e/invoice_amendment_test.go` places one order on the production
wiring and compares what its documents say the buyer owes with what the
buyer's collections captured less what they refunded, read off the payment
journal.

| Step | Figure |
|---|---|
| Line 1: 1 unit at 10 000, taxed 1% by a rate ruled on its product | 10 100 |
| Line 2: 2 units at 5 000, taxed 20% by the region's default | 12 000 |
| Carriage sold, untaxed | 3 000 |
| Order total, captured at checkout | 25 100 |
| Delivery changed to a dearer service, its difference collected first (ADR 0200) | +1 500 |
| What the buyer paid | 26 600 |
| The order's invoice, issued after the change | 25 100 |

On 6a84f21c the assertion `paid == documented` failed with `expected: 26600,
actual: 25100`: the invoice copies the order's sold figures, the order binds
one document, and nothing else documents the 1 500 the buyer paid.

## 2. The same order after the change

| Step | Documents | Paid |
|---|---|---|
| Sale document issued | 25 100 | 26 600 |
| `delivery_upgraded` documented: a `sale` amending the carriage row, 1 × 1 500, tax 0 | 26 600 | 26 600 |
| The same act asked again | 200, `already_issued`, no number spent | |
| Line 2 returned whole and 12 000 refunded through the return | 26 600 | 14 600 |
| `return_refunded` documented: a `refund` of line 2's row, 12 000 with tax 2 000 | 14 600 | 14 600 |

The refund empties line 2's row, so it gives back all the tax the row has
left, 2 000, which is exactly what the row charged.

## 3. A worked split at mixed rates

A credit of 2 510 on an order whose sale document has a 1% row of 10 100 (tax
100), a 20% row of 12 000 (tax 2 000), a gift card row of 5 000 and a carriage
row of 3 000. A credit falls on every row but the card's, weighted by what each
has left:

| Row | Left | Share of 2 510 | Tax: share × row tax ÷ row total, rounded down |
|---|---|---|---|
| 1% | 10 100 | 1 010 | 1 010 × 100 ÷ 10 100 = 10 |
| 20% | 12 000 | 1 200 | 1 200 × 2 000 ÷ 12 000 = 200 |
| Carriage | 3 000 | 300 | 0 |
| Gift card | — | 0 | — |

Each row is printed as one unit carrying its share at its sale row's own rate,
so the document's tax of 210 is the sum of two rates' taxes and not a blended
rate applied to 2 510. The next refund of the 1% row is held to the 90 tax it
has left.

A return of one of line 2's two units refunded 7 000 falls first on the unit's
value, ⌊12 000 × 1 ÷ 2⌋ = 6 000, held to what the row has left, and the 1 000
over it on the carriage (`TestAReturnFillsItsLinesThenCarriage`).

## 4. The ceiling, per row

`TestARowGivesBackNoMoreThanItCarried` on Postgres, a sale with a stacked row
(5% + 8% compound on 2 000: 100 and 168) and a 20% row of 2 × 1 000:

| Refund on the 20% row | Answer |
|---|---|
| 1 200, tax 200 | issued |
| 1 201, tax 200 (the document still has 3 468 left) | 409 `invoice_amendment_exceeds_sale` |
| 1 200, tax 201 | 409 |
| 1 200, tax 200 | issued: what is left fits exactly |
| the second one canceled, then 1 200, tax 200 | issued: a canceled refund gave nothing back |
| after a `sale` charging 120, tax 20, on the row: 120, tax 20 | issued |

On the stacked row, a refund of 1 134 with tax 134 split 101 and 33 is refused
although the row's 268 covers it, because the 5% rate charged 100.

## 5. Two presses, one number

`TestASecondDocumentForOneActSpendsNoNumber`: two goroutines document one act
at once. One document is written, the other answers 409
`invoice_amendment_exists`, and the series' `last_number` is 2: the sale and
its one amendment. `TestTheIndexHoldsAnActToOneLiveDocument` writes a second
live document for the act past the service, through the repository, and the
index refuses it with the same code. `TestTwoActsRaceForOneRow` holds two
refunds that fit alone and not together at the read of what the row has left;
with the sale locked exactly one is issued, and with the lock removed both
were.

## 6. A row given back in pieces

A part's tax is rounded on what its row has given back so far: what the row's
ratio gives on everything given back with the part, less what it gave on
everything before (`TestTheRoundingOfARowIsNotCollectedByItsLastPart`). A row
of 1 180 with 180 tax given back as 6, 1 173 and 1:

| Part | Rounded on its own | Rounded on the running total |
|---|---|---|
| 6 | ⌊6 × 180 ÷ 1 180⌋ = 0 | ⌊6 × 180 ÷ 1 180⌋ = 0 |
| 1 173 | ⌊1 173 × 180 ÷ 1 180⌋ = 178 | ⌊1 179 × 180 ÷ 1 180⌋ − 0 = 179 |
| 1 | the 2 left: total 1, tax 2, net −1 | the 1 left: total 1, tax 1, net 0 |

Rounded on its own, the last unit collected the parts' rounding and printed a
negative net, which a document whose prices exclude tax refused and one whose
prices include it issued. Rounded on the running total, no part's tax exceeds
the part and the row still gives back exactly 180. After a refund issued past
the flow gave back 1 179 with no tax, the last unit takes 1 of the 180 left,
and the 179 stay on the row with nothing left to carry them.

## 7. A delivery changed up and back on an order that shipped free

`TestAFreeDeliveryChangedUpAndBackIsDocumented` (e2e): an order of 6 000 ships
free, so its invoice has no carriage row. The delivery is changed to an
express service and 1 500 paid; the act is documented as a `sale` with a row of
its own, and the documents say 7 500. The delivery is changed back, writing a
credit of 1 500; the act is documented as a `refund` naming the row the express
charge added, and the documents say 6 000, what the order owes. Before the
review's fix the second act answered 409 `invoicing_act_does_not_fit`: a
refund could name only the sale's own rows.

## 8. Sources

- Turkish VAT Law No. 3065, Article 35: a tax base changed after the sale, by
  a return or a price changed later, is corrected for the period of the change,
  by a document naming the original.
- The Revenue Administration's e-invoice guide: a return is issued as the
  return invoice type naming the invoice it returns against; a price
  difference is an ordinary invoice of the difference at the original rate.
- Turkish Consumer Protection Law No. 6502, Article 48: a distance sale may be
  withdrawn within fourteen days, and the seller repays within fourteen days of
  the notice; the document's period is the shop's duty, not the tree's.
