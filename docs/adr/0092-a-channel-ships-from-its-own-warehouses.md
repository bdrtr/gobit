# ADR 0092 — A channel ships from its own warehouses

**Summary:** A sales channel can be bound to the warehouses it ships from, and
an order placed on it is reserved from those and from no others.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

gobit had sales channels and it had warehouses, and nothing between them. The
checkout asked the inventory module which warehouses hold the units and the
fulfillment module to rank them; the channel the order came from was never part
of the question, because no row anywhere could answer it.

So a shop running two storefronts out of two warehouses could not say so. An
order placed on the Istanbul storefront was reserved in Ankara whenever Ankara
held the stock — silently, and correctly by every rule the framework knew.

## Decision

The inventory module declares a many-to-many link between a stock location and
a sales channel, and three admin endpoints manage it. The checkout resolves the
warehouses its order's channels are served by, narrows the reservation
candidates to them before the ranking is asked for, and refuses a DECLARED
location outside them.

A channel bound to no warehouse narrows nothing. That is the reading, not an
oversight: every installation has no binding on the day this ships, and the
other reading would refuse every order in all of them.

## Consequences

The channel travels from the request's own identity — the storefront endpoint
fills it from the publishable key — so a client cannot name a channel to reach
another storefront's stock. An administrative caller names none and the flow
behaves exactly as it did before.

A read of the binding that FAILS fails the order. Treating it as "no
restriction" would place the order from a warehouse the channel may not serve,
which is the thing the binding exists to prevent, and nothing would say the rule
had been skipped.

Refusing is now possible where selling was: units exist, and the order is turned
down because they are in the wrong warehouse. It carries a code of its own, so
an operator is sent to the binding rather than to a purchase order.

**The storefront badge is not narrowed and the checkout is.** A shopper can be
shown "in stock" for units their storefront cannot ship, and find out at the
last step. It is written down in `known-limits.md` rather than left implicit,
and it is the next change: making the badge channel-aware needs a filter on a
Query expansion, which `core/query`'s provider contract does not carry.

Measurement: none. The gap was read off the code — the reservation step asks two
modules and neither of them is asked about the channel.

## Rejected

**Storing the channel on the cart.** Medusa's shape, and a bigger change: a
column, a writer, a migration, and a cart that can disagree with the key the
request arrives with. The flow already takes the location and the provider as
inputs; the channel is one more, and it is the one the request has proved.

**Reading the channel from the request context inside the inventory module.**
The narrowing would then be invisible at the call site and would apply to
callers that never asked for it, including background flows with no request at
all.

**An intersection over several channels.** A key holding two storefronts would
then order from the warehouses BOTH ship from, which is usually none. The union
is what "this order may have come from either" means.

**Locking the binding to the fulfillment module's policy.** Which warehouse may
serve a channel is a merchant's rule about the shop's structure; the ranking is
a policy about a single shipment. Putting the first inside the second would tie
the shop's topology to a preference order.
