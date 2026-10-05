# ADR 0404 — A gift card is no item to the shipping quote

**Summary:** The cart's shipping quote and an order's delivery facts count every unit sold but a gift card's in `item_count`.
The cart tells a card line by its product's `is_giftcard`, the order by the flag the line kept; the line is still stocked and parcelled.

- **Status:** Accepted
- **Date:** 2026-10-06
- **Amends:** [0210](0210-a-sold-gift-card-is-issued-when-its-order-is-paid.md), whose card line was counted, stocked and shipped like any other, and [0199](0199-a-delivery-can-be-changed-before-it-ships.md), whose delivery quote read the sale as the cart's quote did

## Context

ADR 0210 mails a sold card's code and stocks and ships its line like any other,
nothing marking it digital. The cart's quote and an order's delivery facts
counted every unit sold in `item_count`, so a per-item rate charged for codes
and an `item_count` rule counted them (D246). Whether a unit ships is
`requires_shipping` on its inventory item, which a new item takes as true unless
told otherwise; a variant need not have an item, and a bundle cannot have one
(ADR 0234). The quote already reads each line's product, `is_giftcard` among
its flags, and an order line keeps that flag from the sale (ADR 0211).

## Decision

The cart's quote leaves out of `item_count` the units of a line whose product
the catalog reads as a gift card, and an order's delivery facts leave out those
of a line that recorded `is_giftcard`. Every other unit counts as before, and
the subtotal keeps the card's price.

## Consequences

A per-item rate and an `item_count` rule count every unit sold but a card's; a
cart of cards alone is quoted on no items, and an option's base fee still
applies. An add-on still counts (ADR 0393): the quote tells a card apart, not a
unit that ships.

`is_giftcard` still says what the product is, and the quote reads it as the tax
does (ADR 0247); no column takes a second meaning, which is the fold ADR 0048
refused. The cost is a card that ships: its line is still reserved and a parcel
takes it, but a shop that posts its cards cannot charge them per item or count
them in a rule. Nothing has asked for that; reading a shipping flag in place of
the card flag would give it, and no row written meanwhile would need re-reading.

A quote that cannot read the products counts every unit, as a failed read
prices the cart without its discounts, and the shopper sees the dearer delivery
before choosing it. Its price is written when chosen and nothing quotes it
again at completion, so the shopper pays the over-count.

The subtotal keeps a card after its discount, so buying a card can reach a
"free shipping over" threshold, as it could before.

A delivery change reads the flag the order line recorded. An order placed
before this was sold a delivery quoted with its cards, so a change to another
per-item option can write a larger credit than its sale's count would give; the
same option still writes nothing. A line written before order migration 000028
reads `false` and counts.

The fulfillment module is unchanged. A provider plugin's `QuoteInput.ItemCount`
leaves a card's units out when the cart or a delivery change quotes, and the
eligibility endpoints, which take the count from the client, say what the
cart's listing counts.

## Rejected

- **`requires_shipping`, read through the variant's inventory item.** Every
  existing item reads true, a card's included, so it changes nothing until an
  operator flips each one; a variant without an item and a bundle have no
  answer; and every quote pays more reads.
- **`requires_shipping` copied onto the order line.** A migration and a checkout
  read for orders the card flag already answers.
- **The `gift_card` shipping profile.** Nothing binds a product to a profile,
  and a profile selects options, not units.
- **The card's price out of the subtotal.** A threshold reads what the buyer
  pays, and a delivery change's facts are held to the cart's.
- **Refusing a quote whose products cannot be read.** A catalog fault would
  stop every delivery price, where the totals price on without the facts.
- **A setting to count cards as items.** A capability with no consumer.
