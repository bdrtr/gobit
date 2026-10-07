# ADR 0432 — An exchange names its return and prices what it sends

**Summary:** An exchange that names the return taking its goods back prices each item it sends when the item is written and derives its difference, and is documented as a refund of what came back and an amending sale of what was sent.
It costs naming the return before its receipt and a quote per variant sent; an exchange written without a return keeps its typed figure and stays on no document.

- **Status:** Accepted
- **Date:** 2026-10-07
- **Amends:** [0120](0120-an-exchange-can-take-its-difference.md), whose difference is derived for an exchange that names its return; [0145](0145-a-replacement-can-send-something-else.md), whose variant is priced; [0394](0394-a-sold-lines-price-and-tax-are-not-raised.md), whose exchange no longer raises a sold line when it names its return; [0423](0423-a-parcel-that-came-back-holds-only-what-a-return-or-a-replacement-speaks-for.md), whose count of what a line's returns and replacements speak for takes such an exchange once

Measurement: [measurements/0432](../measurements/0432-what-an-exchange-prices.md)

## Context

An exchange recorded a difference the operator typed, sent goods through a
replacement that carried no price, and named nothing it took back (D247). The
dispatch guard compared a collection against that figure alone, so a jacket
could be promised against a shirt's difference, a line's own variant sent at a
difference raised a sold row (ADR 0394), and no document could carry a figure
that names neither goods nor tax (ADR 0406). Turkish practice documents an
exchange of goods as a return of what came back and a sale of what was sent,
each at its own rate. A return already carries the lines coming back, their
ceilings and their receipt.

## Decision

An exchange that names the return taking its goods back prices each
replacement item when it is written, a line as the next units of it at their
share of what the order line charged and a variant at the cart flow's quote in
the order's region, sales channel and customer or at the operator's unit price
taxed by that quote, and its difference is what its live replacements send
less what the return's units were sold for. It is documented as a refund of the returned
units on their sale rows and an amending sale adding a row per replacement
item at the item's recorded rate, and the order journal moves each document's
tax against sales.

## Consequences

- The order module rewrites the difference under the exchange's lock while it
  is requested; a typed difference beside a return is refused, and a funding
  names the figure its collection was checked against or is refused.
- A line's units sent in one replacement or several add up to the returned
  units' worth, and a withdrawal while the exchange is requested, or its
  reopening, prices its line items again from each line's first unit, so an
  even swap owes nothing and no sold row's price is raised.
- A priced item keeps its figures when a price list or a tax table changes, as
  an order line does (ADR 0211). The returned units' worth is what they were
  sold for; a credit or a claim's refund already given for them is not taken
  off.
- A return is named only while it is requested, under its row lock, so it has
  refunded nothing; while the exchange stands it is neither refunded nor
  withdrawn, and a withdrawn exchange lets it go. A return received first is
  not named.
- A quote in a currency or a tax convention the order was not sold in is
  refused, and no variant is sent unpriced when the cart flows are not bound.
- It fixes the figure, not the dispatch: goods still leave before an unfunded
  difference is collected (ADR 0124).
- A funded exchange's replacements are not withdrawn, before any unit is
  released; its refund withdraws it, and they go after. One withdrawn from a
  settled exchange, and a difference owed to the buyer, leave money owed that
  only the payment module's refund route pays (ADR 0120); the named return's
  refund is not a route.
- A claim's replacement and an exchange written without a return are priced
  nothing, and that exchange stays on no document (D247). Its documents and
  the journal's two kinds land in a second commit under this record, which
  amends ADR 0203 and 0406 then; until it a priced exchange is on none either.
- What a line's returns and replacements speak for counts such an exchange
  once per line, the more of what it takes back and what it sends.
- Rolling the order schema back is refused while a live exchange names a
  return.

## Rejected

- One document carrying the difference: it prints the returned row's rate for other goods and raises a sold row for the same variant.
- The operator's typed difference split at the replacement's rate: a human's arithmetic as the source of a legal document.
- A goods-back table of the exchange's own: it duplicates the return's lines, ceilings and receipt.
- Naming a received return the payment module says refunded nothing: a refund in flight is not yet a refund, so both could pass.
- Pricing in the returns flow: the panel and the API would write an exchange's replacements on two paths.
- Pricing at funding or dispatch: the price could move between the money and the goods.
- Accepting the gap: the documents are owed by law on a flow the panel offers.
