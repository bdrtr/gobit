# ADR 0211 — An order books a sold gift card as a debt

**Summary:** An order line records whether it sold gift cards, copied from the
product when the order is placed. The order journal credits those lines to
`gift_card` rather than `sales`, and the gift card sale flow issues cards from
the same flag.

- **Status:** Accepted
- **Date:** 2026-09-27
- **Amends:** [0210](0210-a-sold-gift-card-is-issued-when-its-order-is-paid.md), whose flow read the catalog and whose order books counted a card as a sale
- **Amended by:** [0213](0213-an-operator-closes-a-gift-card-and-a-card-line-is-final.md): a card line is not returned or written off, and a closed card's balance is voided
- **Amended by:** [0247](0247-a-gift-card-is-not-taxed-when-it-is-sold.md): a card line carries no tax

Measurement: [measurements/0211](../measurements/0211-a-card-is-owed.md)

## Context

ADR 0210 issues a card for each unit of a line whose product is flagged
`is_giftcard`, and left the order journal crediting the whole subtotal to
`sales` (ADR 0188). The payment journal debits `gift_card` when a card is spent
(ADR 0208), so a sold card's debt was credited nowhere. An order line did not
know it sold a card: the flow read the product through the catalog at capture,
and the order journal reads orders. The cart's totals read the flag for
promotion rules, and price a cart without it when that read fails.

## Decision

The checkout reads each line's product flag when it plans the order, refuses
the order when it cannot, and the order line keeps the flag as `is_giftcard`.
The order journal credits the subtotal of an order's gift card lines to
`gift_card` rather than `sales`, and the gift card sale flow issues cards for
the lines that carry the flag.

## Consequences

A sold card's debt closes across the two journals: the order that sold it
credits `gift_card` at its face value, each capture through the card debits it,
and what is left is the card's balance. The two modules spell the account alike,
and a gate holds them to it.

The checkout reads the catalog twice, the variants and then their products, and
a failed product read stops it as a failed variant read does.

The flag is the product's at the sale: flagging or unflagging a product later
changes no order, and the books and the flow read the same fact. A line written
before migration 000028 reads `false`. The flow no longer reads the catalog, so
the sales channel exemption it carried is gone. Both order views and the read
layer's `order_line_item` publish the flag.

A cancellation takes the debt back with the rest of the placement, but a card
the order issued stays spendable; nothing voids it yet. A returned card line is
booked as a sales return for the same reason. A card line is taxed as the tax
rules say; nothing exempts it.

## Rejected

- **The flag from the cart's totals.** They read it without stopping on a
  failure, and a guess would book a card as a sale and never issue it.
- **The product's flag on the variant record.** It saves the second read and
  puts a product's column into the variant's contract.
- **The debt booked at the card's issue by the payment journal.** It books an
  issue against a grant, which a sold card is not, and the order would still
  count the price as a sale.
- **The journal reading the products when it books.** A product flagged after
  the sale would rewrite a closed period.
