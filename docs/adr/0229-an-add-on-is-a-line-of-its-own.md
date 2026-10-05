# ADR 0229 — An add-on is a line of its own

**Summary:** A cart line takes add-ons its product accepts as lines of their
own, bound to it, priced by their own price sets and part of what it is, and the
order keeps each add-on bound to the line it was sold with.

- **Status:** Accepted
- **Date:** 2026-09-29
- **Amended by:** [0393](0393-an-add-on-is-written-under-its-line.md): the invoice order and the raise are decided; a quote keeps counting an add-on

Measurement: [measurements/0229](../measurements/0229-an-engraving-and-its-ring.md)

## Context

ADR 0228 lets a product name the variants its lines accept as add-ons, and no
cart line could carry one. A cart line's identity is its variant and properties
(ADR 0223), so an engraving bound to one ring and a wrap bound to another would
have merged, and a plain ring added after an engraved one would have raised it.
The order makes its own line ids and the checkout sent none, so a bond between
two lines could not reach it.

## Decision

A line added with add-ons opens each as a line bound to it, priced from its own
price set at the line's quantity once the workflow finds it on the product's
list; the add-ons join the line's identity, follow its quantity and go with it.
The checkout names every line by its cart id and each add-on by its parent's,
and the order maps the names to the ids it makes and keeps the parent on the
add-on's line.

## Consequences

An add-on is priced, discounted and taxed as its own product, so the price row
of every line still names its whole charge (ADR 0168). The same ring with the
same add-ons, in any order and spacing, raises the line and its add-ons; with
other words on an engraving, or without it, it is another line. An add-on's
quantity is its line's, and a write to it alone, or its removal, is refused
with `cart_line_is_an_add_on`: the shopper removes the line and adds it again.

Every add-on counts against the line ceiling (ADR 0227), and an add that does
not fit with its add-ons opens nothing. A merge opens a line the target lacks
with its add-ons under it and raises one it holds with them. The checkout
refuses a plan whose add-on names no line of its own before it reserves or
charges anything, and the order line's parent is a composite key to its own
order, shown on the order read and the order line entity.

The add-on list is checked when the line is added and not again at the till.
Returning, cancelling or replacing a parent does not touch its add-ons, an
invoice prints lines in the order they are read, and a shipping quote counts
an add-on's units; the panel and the GraphQL surface do not show the bond yet.

## Rejected

- **An add-on folded into its line's unit price.** One price row would name part
  of the charge, and a gift card's value and the price list trial would count
  the modifier as the product (the user chose the line).
- **A modifier amount on every hop from cart to order.** Four identity checks
  and the order's constraints would change for what a line already carries.
- **Add-ons attached to a line already in the cart.** The line's identity would
  change under it, and two lines could become one.
- **Line ids made by the checkout.** The order makes its ids, and a sender
  inventing them would be a second author of the order's rows.
