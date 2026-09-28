# ADR 0228 — A product names the add-ons its lines take

**Summary:** A product keeps an ordered list of up to 20 variants of other
products that its cart lines may carry as add-ons, which the operator replaces
whole and the storefront reads as the add-ons it may show.

- **Status:** Accepted
- **Date:** 2026-09-28

Measurement: [measurements/0228](../measurements/0228-the-words-a-ring-takes.md)

## Context

A price modifier, an engraving or a gift wrap priced on top of a line, is an
add-on line: a variant with its own price set, opened on the cart bound to the
line it modifies. ADR 0223 left the modifier out because pricing had no row for
it and the server deciding the price needed its own design; an add-on variant
has a price row and the ladder picks it. Nothing yet said which add-ons a
product's lines accept, and a cart cannot let a shopper attach any variant to
any line.

## Decision

A product carries a list of add-on variants, replaced whole by
`PUT /admin/v1/products/{id}/add-ons` and refused, naming what it refused, when
a variant is missing, deleted, named twice, the product's own, or past 20. The
storefront reads it at `/store/v1/sales-channels/{sales_channel_id}/products/{id}/add-ons`
as each visible add-on's variant and product, in the operator's order.

## Consequences

The list is a pointer, as a relation is (ADR 0180): deleting the variant or
either product takes the entry off, in the deletion's transaction, and it is
neither in a product's revision view nor behind its `If-Match` (ADR 0221, ADR
0222). The add-on's product need not be published when it is named; the
storefront shows only the add-ons whose product it may show in the channel, and
the product the read starts from has to be one it may show, or the answer is
404.

An add-on is an ordinary variant: its price, tax class, stock and channel are
its product's. The cart binding a line to its parent, and checking the add-on
against this list, is the decision that follows; until it lands the list
answers what a storefront may offer and nothing accepts it. The panel does not
edit the list yet.

The gates holding the channel-scoped reads' document and their refusal of a
foreign channel read four and three reads named by hand; they now read every
path under the channel segment, which found the facet counts' document
describing neither the segment nor its refusal, and a paged body the read never
writes (D155). The audit holding every such route to the narrowing helper read a
path only from a literal, and the related products' path was a concatenation it
skipped; the path is now a literal and a path it cannot read fails it (D156).

## Rejected

- **Add-ons as options with a price on the product.** Pricing has no row for
  an option, and a modifier folded into a variant's line would leave one price
  row naming part of the charge (ADR 0168).
- **Any variant on any line.** A storefront could attach a wrap to a gift card,
  and nothing would say which engraving belongs to which ring.
- **The list in the product's revision view.** A relation is not in it either,
  and a restore would bring back entries whose variants may be gone.
