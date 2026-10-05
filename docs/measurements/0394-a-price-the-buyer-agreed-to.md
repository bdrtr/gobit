# A price the buyer agreed to — measured 2026-10-05

Evidence for [ADR 0394](../adr/0394-a-sold-lines-price-and-tax-are-not-raised.md)
and gap D247. Read on a tree at 2bdeb96a; the probe ran with Docker's
`postgres:16-alpine`, every Go command as
`GOTOOLCHAIN=go1.26.6 GOMAXPROCS=2 nice -n 19`.

## 1. Every order write that moves money after the sale

| Act | Route | Direction | Record | Journal kind → account | Document |
|---|---|---|---|---|---|
| Credit | `POST /admin/v1/orders/{id}/credit-lines` | to the buyer | `order_credit_lines` | `credit_line` → `credit_allowances` | none |
| Cheaper delivery | `PUT /admin/v1/orders/{id}/shipping-methods/{shippingMethodId}` | to the buyer | `order_delivery_changes` + a credit line | `delivery_changed` → `shipping` | none |
| Dearer delivery | the same route with `payment_collection_id` | from the buyer | `order_delivery_changes` + a paid collection | `delivery_upgraded` → `shipping` | none |
| Exchange funding | `POST /admin/v1/orders/{id}/exchanges/{exchangeId}/funding` | from the buyer | `order_exchanges.payment_collection_id` | `exchange_funded` → `sales` | none |
| Exchange refund | `POST /admin/v1/orders/{id}/exchanges/{exchangeId}/refund` | to the buyer | a caused refund | `exchange_refunded` → `sales` | none |
| Return refund | `POST /admin/v1/orders/{id}/returns/{returnId}/refund` | to the buyer | a caused refund | `return_refunded` → `sales_returns` | none |
| Claim refund | `POST /admin/v1/orders/{id}/claims/{claimId}/settle` | to the buyer | a caused refund | `claim_refunded` → `claim_allowances` | none |

The exchange's funding is the one path that books goods revenue after the sale.
Its figure is `difference_due`, which the operator types when the exchange is
opened (`internal/modules/order/service/aftersales.go`) and which is checked
for sign alone; `docs/known-limits.md` relates it to the goods "only by a
human". It is booked to `sales` whole, with no tax split (ADR 0203).

## 2. What the order module can compute

No price and no tax: the order module copies the cart's snapshot
(`internal/modules/order/service/order.go`), and its amounts are pinned to its
lines by the CHECKs `orders_totals_consistent` and
`order_line_items_totals_consistent`. The journal books one account per kind:
`givenBackTo` names five, `chargedTo` names two
(`internal/modules/order/service/journal.go`). The gate
`TestOnlyADearerDeliveryAndAnExchangeChargeAfterTheSale` reads every
`JournalKind` the models declare, books each through `journalEntry`, and pins
that only those two debit receivable and credit another account after the
sale; a kind added with its own case arm is read as well as one added to the
map.

## 3. What SQL can rewrite

Every UPDATE and DELETE under `internal/modules/order/queries`, with the
columns it sets, comments stripped:

| File | Table | Columns set |
|---|---|---|
| `orders.sql` (CancelOrder) | orders | status, canceled_at, cancel_reason, updated_at |
| `orders.sql` (CompleteOrder) | orders | status, completed_at, updated_at |
| `orders.sql` (ArchiveOrder) | orders | status, archived_at, updated_at |
| `erasure.sql` (AnonymizeOrderContacts) | orders | email, personal_data_erased_at, updated_at |
| `erasure.sql` (AnonymizeOrderAddresses) | order_addresses | the address fields, source_address_id, updated_at |
| `order_addresses.sql` | order_addresses | superseded_at, updated_at |
| `order_summaries.sql` | order_summaries | paid_total, refunded_total (GREATEST), updated_at |
| `order_exchanges.sql` (cancel, complete, reopen, withdraw) | order_exchanges | status, a stamp, updated_at |
| `order_exchanges.sql` (FundOrderExchange) | order_exchanges | status, payment_collection_id, funded_at, updated_at |
| `order_claims.sql` (three) | order_claims | status, a stamp, updated_at |
| `order_returns.sql` (two) | order_returns | status, a stamp, received_location_id, updated_at |
| `order_replacements.sql` (three) | order_replacements | status, a stamp, fulfillment_id, recalls, updated_at |
| `order_replacement_items.sql`, `order_replacement_item_parts.sql` (four) | the reservation tables | reservation_id, updated_at |
| `order_claim_evidence.sql` | order_claim_evidence | DELETE |

No statement updates or deletes `order_line_items`, `order_line_taxes` or
`order_shipping_methods`, none sets an order's `subtotal`, `discount_total`,
`tax_total`, `shipping_total` or `total`, none sets `difference_due`, and no
query says `ON CONFLICT`. `TestASoldLineIsNotRewrittenInSQL` pins this on every
statement read whole, whitespace collapsed and identifiers unquoted, so an
alias, `ONLY`, a line break, a row assignment `SET (a, b) =`, a subquery with
its own `WHERE` and a `MERGE` are read as the plain form is. It also refuses
removing an order, which cascades to its lines, and an upsert that updates a
sold line, an order or an exchange; an insert that does nothing on a conflict
is not refused.
`order_credit_lines_amount_positive` refuses a credit row of zero or less in
the schema, which `TestTheCreditTableRefusesACharge` now tests; before it, the
only file naming the constraint was its migration.

## 4. What an invoice can carry

The invoicing flow issues one kind, `sale`
(`internal/workflows/invoicing/issue.go`), built from the order's sold lines
and a carriage line of the order's `shipping_total`. The order's link to its
document is `link.OneToOne` (`internal/modules/invoice/service/links.go`), and
the refund document kind exists in the model while nothing issues it.
`POST /admin/v1/orders/{id}/invoice` is an operator call with no moment of its
own: it may come before or after any act in section 1.

**The worked case, run.** A probe in `internal/e2e`, run with the whole package
and deleted afterwards, checked out an order on a spy option priced at the sold
fee, paid a dearer change of 1,500 on a collection naming the order
(ADR 0200), and then issued the invoice. The whole package passed with it.

| Figure | Minor units |
|---|---|
| The sale: one line of 12,000 with tax, carriage of 3,000 | 15,000 |
| The dearer delivery, captured on its own collection | 1,500 |
| What the buyer paid | 16,500 |
| The invoice's total | 15,000 |
| The invoice's carriage line | 3,000 |

Nothing in the tree prints the 1,500 on a document (D247).

## 5. The law (the project's reading, not legal advice)

| Source | What it says | Effect here |
|---|---|---|
| Directive 2011/83/EU art. 6(1)(e), 6(6) | The total price with taxes is given before the contract; undisclosed charges are not borne by the consumer | The price is fixed at checkout |
| Directive 2011/83/EU art. 22 | An extra payment needs express consent before the consumer is bound | Consent to a higher price is a new checkout |
| Directive 93/13/EEC Annex 1(l) | A price raised after the contract without a right to cancel is indicatively unfair | No raise after the sale |
| Law 6502 art. 48; Distance Contracts Regulation (2014) art. 5(1) | Total price with every tax in the pre-contract information | The same as the EU rule |
| Law 6502 art. 5; Unfair Terms in Consumer Contracts Regulation (Official Gazette 29033, 17 June 2014), art. 5(4) and its annex | A term letting the seller fix or raise the price at performance, with no right to withdraw when the final price is far above the price agreed, is listed as unfair, and an unfair term is void | No raise after the sale, as in the EU |
| TBK 6098 art. 31, last paragraph | A simple calculation slip does not affect the contract's validity and is corrected | The order module offers no act for it; the shop pursues it outside the tree, and the tree's remedy is the buyer's new purchase |
| TBK 6098 arts. 30-39 | A party may avoid the contract for fundamental error, within a year | A legal act of the shop, not a module verb |
| VAT Directive 2006/112/EC art. 73; CJEU C-249/12 and C-250/12 (Tulică, 2013) | Tax is owed on the agreed consideration; a price agreed without VAT is read as VAT-inclusive when the supplier cannot recover the tax | Tax charged too low comes out of the agreed total |
| VAT Directive art. 219; VAT Law 3065 art. 35 | A change after the sale needs an amending document | None exists in the tree (D247) |

The article number of TBK 6098's calculation slip was checked against the
published text on 2026-10-05: it is the last paragraph of article 31, the
article on error in the declaration. The unfair-terms annex entry was checked
against the regulation's published text the same day.

## 6. The remedy walked end to end

- **A pending order.** `POST /admin/v1/carts` with `adds_to_order_id`, the
  line at its right price, `POST /admin/v1/carts/{id}/complete`; then
  `POST /admin/v1/orders/{id}/line-cancellations` for the first line, which
  restocks its units (ADR 0134), and
  `POST /admin/v1/orders/{id}/credit-lines` for what it was paid.
- **A shipped line.** A new order at the right price, then
  `POST /admin/v1/orders/{id}/returns` for the first line and its refund on
  `POST /admin/v1/orders/{id}/returns/{returnId}/refund`.
- **What is refused, and where.** A gift-card line is neither written off nor
  returned (`refuseGiftCardLines`, ADR 0213); a guest's order and a completed
  one take no addition (ADR 0192), so the buyer places a new order; a paid
  order is not canceled (ADR 0339), so the first sale stands until its line is
  written off or returned.
- **What the panel offers.** The write-off (ADR 0341) and the credit
  (ADR 0388). The addition cart is an `/admin/v1` call: `adds_to_order_id` is
  a field of the admin cart body (`internal/modules/cart/api/admin_write.go`)
  and no panel screen sends it.
