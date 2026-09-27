# ADR 0220 — A price list can be tried on past orders

**Summary:** An operator asks what a price list, in whatever status, would do to
the goods of a past period's orders. Each line is priced with today's ladder
with the list and without it; nothing is written.

- **Status:** Accepted
- **Date:** 2026-09-28

Measurement: [measurements/0220](../measurements/0220-a-list-against-its-period.md)

## Context

An operator writing a sale or a contract list could read its prices but not
what they would do to the store's sales until the list was switched on. ADR 0176
answers the same question for a promotion over the orders of a period: the cart
flows read the orders and the rule context a cart carries, and the promoting
module asks them through a narrow surface, since it may not import them (ADR
0006). The price ladder is pricing's own: which price wins among the base, the
lists, their rules and quantities is decided in `selectPrice`.

## Decision

`GET /admin/v1/price-lists/{id}/trial?from=&to=` prices every uncanceled order
placed in the period line by line, at the line's quantity, in its currency and
with the rule context a cart of its customer in its region carries, twice with
today's ladder: without the list, and with it offered as active with no window.
It reports per currency, and for the hundred largest changes per order, the
baseline, the trial and what the lines were sold at, writes nothing, and asks
for `order:read` beside `pricing:read`.

## Consequences

The list's effect is trial minus baseline. Both are priced at today's ladder, so
the figure isolates the list from every price change since the sale; what the
lines were charged is reported beside them and not compared. The report names
what it set aside: today's prices, price set links and customer groups, the list
active with no window, and prices before discounts.

Pricing gains `CompareListJSON`, one read of every set's candidates and two
selections per line, bounded at 5,000 purchases and 100,000 lines. The trial
flow lives in the cart flows beside the promotion trial and shares its bounds:
93 days and 5,000 orders. The pricing module resolves the flows' surface by name
on first use, and the interop pin holds it to the producer.

A line whose variant has no single price set today, or whose set has no price in
the order's currency without the list, is counted unpriced; an order with no
line priced is not counted. A customer whose groups cannot be read is priced
without them and the failure is logged. The list's type, rules and quantity
tiers count as they are written, so a draft override is tried as an override.

## Rejected

- **Comparing the trial with what was charged.** The difference would mix the
  list with every price change since each sale.
- **The ladder as it stood at each sale (ADR 0167).** The list will be switched
  on against today's ladder; the figure a decision needs is its effect there.
- **Running the trial in the pricing module.** It would read orders and build
  the cart's rule context, both of which belong to the cart flows (ADR 0006).
- **Recomputing promotions.** A promotion has its own trial (ADR 0176);
  combining them would multiply what the report assumes.
