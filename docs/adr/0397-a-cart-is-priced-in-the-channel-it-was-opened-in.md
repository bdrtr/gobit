# ADR 0397 — A cart is priced in the sales channel it was opened in

**Summary:** A cart records the one sales channel it was opened in, and every round prices
and discounts it there; a channel names no currency, its country's region does.

- **Status:** Accepted; amended by [0403](0403-a-carts-metadata-chooses-no-price.md), which prices no cart by its metadata, so the list trial sets none aside
- **Date:** 2026-10-05
- **Amends:** [0146](0146-an-operator-can-build-a-cart.md), whose cart opening now may name a channel, and [0216](0216-a-wishlist-item-can-ask-for-its-price.md), whose quote is now a cart's that names none

Measurement: [measurements/0397](../measurements/0397-what-a-cart-is-priced-in.md)

## Context

Feature row B5.3 asks for a price and a currency per sales channel. A price
rule may name any attribute, so a rule on `sales_channel_id` can be written
today and matches no cart: the rule context carries the region, the buyer,
their groups and the cart's metadata, never a channel. The cart holds no
channel; ADR 0146 refused one as a scope chosen once, and ADR 0092 as a cart
that can disagree with the key the request arrives with. Its totals are
recomputed by every write under whichever principal made it: a storefront key,
an operator's asserted channel, or an operator's address write carrying none.
A currency is a region's column and a country belongs to at most one region,
by the region schema's own structure. ADR 0009 stands: a channel is a market
inside one installation, not a tenant.

## Decision

A cart records `sales_channel_id` when it is opened — the publishable key's
channel when the key holds exactly one, the channel an operator names on
`POST /admin/v1/carts`, none otherwise — and the rule context carries it on
every price, discount and totals round. A channel carries no currency and no
region: a channel's price is a price in a region's currency whose rule names it.

## Consequences

- The ladder is unchanged. A channel rule is one more matched rule: a price
  ruled on the region and the channel beats the region's own, but one ruled on
  the channel alone ties with it and the cheaper wins. A sale or override list
  still outranks a channel's base price, and a contract outranks it.
- The price follows the cart and the scope follows the request. This is the
  disagreement ADR 0092 refused, accepted for the price alone: an address write
  and the completion price one cart alike, so `expected_total` (ADR 0286)
  compares like with like, while a line enters under the request's channels
  (ADR 0281) and the completion's channel picks the warehouses (ADR 0092).
- A channel price is no privilege: a publishable key is served to every browser
  of its storefront. A price for some buyers is ruled on the buyer or the group.
- A key bound to several channels, a cart opened before migration 000010 and
  an operator's cart opened without a channel name none, and a channel-ruled
  price never matches them. An operator's channel is checked for shape only
  (ADR 0146), so a mistyped one is recorded and matches nothing for good.
- A merge moves lines into the target's channel as it moves them into its
  buyer; it refuses nothing new.
- A price or promotion rule on `sales_channel_id` written before the upgrade
  matched nothing and starts matching.
- The order does not record the channel. The trials price past orders without
  it and say `no_sales_channel`; the list trial also says `no_cart_metadata`
  (gap D253). The wishlist price alert quotes a cart naming no channel.
- The storefront's product reads list only rule-free prices (ADR 0044), so a
  channel price shows at the cart and not on the product page.
- Currency per channel is reopened when a shop must sell one country in two
  currencies by storefront; its shape is a country in several regions, chosen
  by channel, which undoes the region schema's one-region rule.

## Rejected

- The channel derived from each request: an address write with none would
  reprice an operator's cart away from the completion's figure.
- The first of several channels: the price would follow an ordering accident.
- A currency column on `sales_channel`: one cart would have two sources for
  its currency, and the region's tax and shipping would not follow it.
- A merge refused across channels: the target's totals reprice the moved
  lines, and a login would answer 409 for every cart opened before the upgrade.
- A channel per line: one cart priced in two channels has no total to show.
- A rank of its own for a channel price: specificity already ranks it.
