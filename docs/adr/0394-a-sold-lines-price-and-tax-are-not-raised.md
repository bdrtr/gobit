# ADR 0394 — A sold line's price and tax are not raised

**Summary:** Nothing raises the price or the tax of a line a placed order sold; the buyer buys it again or the shop bears it.
It costs a second order where an edit would do, and the shop pays a tax it under-charged.

- **Status:** Accepted; amended by [0406](0406-an-amount-moved-after-the-sale-is-a-document.md), which documents an amount moved after the sale on a document amending the order's
- **Date:** 2026-10-05
- **Amends:** [0105](0105-a-credit-lowers-what-is-owed-not-what-was-sold.md), whose "different act" this record refuses for a sold line

Measurement: [measurements/0394](../measurements/0394-a-price-the-buyer-agreed-to.md)

## Context

ADR 0105 refused a negative credit and called a charge after the sale a
different act with a different authorization, and nothing built or refused
that act for a line the order sold. A placed order takes money after its sale
in two ways, a dearer delivery (ADR 0200) and an exchange's difference
(ADR 0120), each on a collection the buyer pays, each booked to one revenue
account with no tax and printed on no document. Consumer law in the EU and
Turkey requires the total with every tax before the buyer is bound, and holds
a price raised afterwards without a right to cancel unfair.

## Decision

No record, route or journal kind raises the price or the tax of a line a placed
order sold, and `order_credit_lines` stays positive. A line priced too low is
the shop's loss unless the buyer buys it again at checkout, and a tax charged
too low is the shop's, out of the total the buyer agreed.

## Consequences

- The buyer's consent to the higher price is a checkout: an addition to the
  pending order (ADR 0192) or an order of its own, priced, taxed, invoiced and
  booked as any sale.
- The first line is given up only after that purchase: written off before
  dispatch (ADR 0113) and credited (ADR 0105), or returned and refunded after
  it. A write-off restocks (ADR 0134), so it never names goods the buyer holds.
- A buyer who declines keeps the sale as made. Avoiding the contract for error
  is the shop's legal act, not a verb of the order module, and a paid order is
  not canceled (ADR 0339).
- A gift-card line is neither written off nor returned (ADR 0213), and a
  guest's or a completed order takes no addition (ADR 0192); the buyer places
  a new order or the price stays as sold.
- A tax charged too low stays on the order, its invoice and the order journal
  as charged, so `tax_payable` understates what the shop owes by it. A
  restatement is a record nothing writes.
- An exchange's difference prices the goods its replacement sends (ADR 0145)
  and the buyer pays it on its own collection. Nothing ties the figure to those
  goods, so an exchange that sends the line's own variant at a difference is a
  raise this record forbids and no code refuses.
- An order's invoice prints the sale alone and an order binds one document, so
  no money taken or given back after the sale is on a document (D247).
- A zero or negative credit is refused with `order_invalid_input` as before,
  and its message says a credit is never withdrawn.
- Two gates hold it: the order journal credits revenue after the sale for a
  dearer delivery and an exchange's funding alone, and no order query rewrites
  a sold line, its taxes, its shipping method or an order's amounts.
- Reopened when the invoice module binds a document amending an order's issued
  one and a buyer's contract lets the price move after the sale, as a business
  account on payment terms would; the tree has neither.

## Rejected

- A debit on a collection naming the order, as a dearer delivery is
  (ADR 0200): the checkout prices, taxes and invoices what the buyer agrees to
  pay, where the debit is booked to sales whole on no document (ADR 0203).
- A negative credit line: one column would mean credit and charge (ADR 0105).
- Editing the order's lines or totals: an order is what was sold at its moment,
  and its invoice copies it (`order/models`).
- A debit left owing: the summary reads only the sale's collection, so nothing
  could collect it (ADR 0200).
- Exchanging a line for itself at the difference: an untaxed price raise on no
  document, under another record's name.
- Moving tax inside a tax-inclusive line's total: the issued document would
  disagree with the books until a document amends it.
- Charging the buyer a tax charged too low: the price they agreed included it.
