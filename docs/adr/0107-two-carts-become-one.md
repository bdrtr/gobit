# ADR 0107 — Two carts become one, and the quantity is the sum

**Summary:** A cart can be folded into another: the lines move, colliding
quantities are summed, and the source is emptied and deleted. It costs a second
row lock taken in identifier order and buys the member who signed in one cart
instead of two.

- **Status:** Accepted — amended by [0109](0109-a-cart-holds-the-coupon-the-customer-typed.md)
- **Date:** 2026-09-10

## Context

Signing in could TRANSFER a cart and could not fold one. `UpdateCart` gives a
guest cart an owner, keeping its lines — and refuses when the cart already has a
different one. So the shopper who filled a basket on their phone as a guest, and
who already had a basket from last week's session, ended up with two: the
transfer was refused and nothing else could join them.

The path is the ordinary one. A shop that keeps carts across visits and lets
people browse before signing in reaches it on every login, and what the customer
sees is the goods they just chose disappearing.

The open question was the collision: the same variant on both sides. Sum, or the
larger of the two?

## Decision

`MergeCart(source, target)` moves the source's lines onto the target, sums the
quantity where the variant is on both sides, then empties and soft-deletes the
source. Both carts are locked in identifier order inside one transaction.

The sum is not a new decision — it is `AddLineItem`'s, applied to a batch.

## Consequences

Only the LINES move. The target keeps its email, addresses, shipping method and
metadata: the merge moves goods, not identity, and an address chosen while signed
in is not something a guest session overwrites.

A source in another region or currency is refused. A line's unit price is a
snapshot priced FOR a region and a currency, and carrying it across would put a
price into a cart where it was never valid.

A source owned by another customer is refused — `UpdateCart`'s refusal seen from
the other end.

The lock is taken by identifier and not by role. Two merges running opposite ways
at once would otherwise each hold the row the other waits for, and PostgreSQL
would settle it by killing one.

The prices that travel are the source session's snapshots. They are of the same
region and currency, and the totals round reprices them exactly as it reprices a
line that had been sitting in the target all along — which is why the merge bumps
the target's revision, and why it does not bump it when nothing moved.

## Rejected

**The larger of the two quantities.** `AddLineItem` already answered this, with
three grounds: the price tier is picked off the summed quantity, one line means
one reservation, and the same product twice reads as two products. The same two
adds must not answer differently for having been made in two sessions.

**Merging into the guest cart instead.** That is the transfer, and it already
exists; it is the case where the member has no cart of their own.

**Carrying the source's addresses and shipping method.** They were chosen by a
session that could not prove who it was.

**Leaving the source alive.** A cart still holding lines can still be completed,
and the same goods would be bought twice.

**Repricing inside the merge.** The cart module does not call pricing
(Principle 2.1); the revision bump is the signal the totals workflow reads.
