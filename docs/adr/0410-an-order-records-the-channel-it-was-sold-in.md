# ADR 0410 — An order records the channel it was sold in

**Summary:** An order copies the sales channel its cart was opened in, and the admin record, list and filter publish it.
It costs a column and a plan field, and the trials price a past order in the channel it recorded.

- **Status:** Accepted
- **Date:** 2026-10-06
- **Amends:** [0397](0397-a-cart-is-priced-in-the-channel-it-was-opened-in.md), whose order kept no channel

## Context

ADR 0397 made a cart record the one sales channel it was opened in and price
every round there, and left the order without it: a shop selling through a
store key and a web key could not tell the two apart after the sale, and the
promotion and price list trials priced past orders in no channel, saying
`no_sales_channel`. The checkout reads the cart's snapshot, which carries the
channel, and passes a saga plan to the order module, as it passes the operator
who placed the order (ADR 0298).

## Decision

An order records the sales channel its cart was opened in as
`sales_channel_id`, copied at placement and NULL when the cart named none, and
the admin order record, the admin list and its `sales_channel_id` filter
publish it. The promotion and price list trials price each past order in the
channel it recorded.

## Consequences

- The channel is the cart's, the one the price was chosen in; the completing
  request's channels still pick the warehouses (ADR 0092) and are not
  recorded.
- An order from a key bound to several channels, an operator's cart naming
  none, a cart opened before cart migration 000010 or a plan saved before this
  records none, and the filter lists it under no channel.
- The value is copied as the cart holds it; the order module does not read the
  auth module's channels, so a channel an operator mistyped is recorded.
- Order migration 000042 adds the column, a CHECK refusing a blank one and a
  partial index the filter walks.
- The trials say `recorded_sales_channel`; `no_sales_channel` was never
  released.
- The panel's order list filters by channel, offered by name to an operator
  who may read the channels, and the order page names the channel's id.
- The storefront's order, the order events and the dossier do not carry it.

## Rejected

- The request's channels: they may be several and scope stock, not price.
- The channel on `order.placed`: the one subscriber reads the order, and a receiver is not owed it.
- The channel on the storefront's order: it is the shop's partition, not the shopper's.
- The channel's name on the order page: one more read of the auth module for every order page, for what the list's filter already names.
- Deriving an old order's channel from its cart: a cart's row is not kept for that, and a guess would enter the books.
