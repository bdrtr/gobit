# ADR 0216 — A wishlist item can ask for its price

**Summary:** A proven customer marks a wishlist item's price in a region, the
alert job records the unit price their cart would be charged there, and it mails
their own address once when that price falls below the recorded one. The price
mark sits beside the stock mark on the same item, and each clears on its own.

- **Status:** Accepted
- **Date:** 2026-09-27
- **Amends:** [0215](0215-a-wishlist-item-can-ask-for-its-stock.md), whose items could ask only for their stock

Measurement: [measurements/0216](../measurements/0216-a-price-that-fell.md)

## Context

ADR 0215 left the price-drop trigger for later: a price depends on a region and
its currency, and the stock mark carried neither. The customer module cannot
price. The cart workflow prices a line in the region's currency with a rule
context naming the region, the customer, their company and their head group, so
a price list or a contract price reaches the customers it names (ADR 0185). A
promotion is the cart's discount, which depends on its other lines and a code.
The user chose the unit price the customer would pay, a drop below the price at
the mark, and a separate mark on the same item.

## Decision

`PUT /store/v1/customers/{id}/wishlist/{variant_id}/price-alert` with a
`region_id` marks the item with that region and the request's sales channels,
and `DELETE` takes the mark off. The `stock-alert` job's pass asks the cart
workflow for the one-unit price of the marked variants the storefront shows in
those channels, records it on a mark that has none, and mails the customer's own
address once when the price is lower than the recorded one in the same currency,
clearing the mark.

## Consequences

The price at the mark is recorded by the first pass after the mark, not at the
request, so a price that falls in between is not a drop. Marking again forgets
the recorded price and starts a new mark. The mail's reference names the mark's
moment, so a pass stopped between the mail and the clear sends nothing twice.

The quote is the cart workflow's `QuoteUnitPrices`: the cart's price for one
unit, price lists and contract prices included and promotions left out. A
customer whose groups or company cannot be read is quoted without them, as a
cart's totals are. A variant with no price in the region's currency records
nothing, and a price recorded in a currency the region no longer sells in is not
compared.

A region that does not exist, mistyped by the request or deleted since, prices
nothing and is not a failure of the pass: the quote names it
`cart_workflow_quote_region_unknown`, and the mark waits. The customer module
cannot check a region when the mark is set.

The job keeps its name and gains a count of the prices recorded in its line. A
notification provider has to know `wishlist.price_drop`, whose data carries the
currency and both amounts in minor units.

The mark is declared personal data; its region, channels and recorded price are
the shop's working state. Rolling back customer migration 000005 forgets the
price marks.

## Rejected

- **Recording the price at the request.** The customer module cannot price, and
  a price in the request body would be the client's word.
- **The price after promotions.** It depends on the cart's other lines and a
  code, and would mail a price no cart of the variant alone gives.
- **A second job for prices.** It would walk the same marks again; one pass
  reads each page once and answers both.
- **A threshold the customer names.** Any drop below the mark is what the user
  chose, and a threshold is a number the storefront would have to validate.
