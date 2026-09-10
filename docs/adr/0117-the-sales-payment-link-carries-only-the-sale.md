# ADR 0117 — The sale's payment link carries only the sale

**Summary:** `order_payment` stays one to one, and money collected against an
order for some other reason will be bound under a name of its own. It costs a
second relation to query and buys three readers that keep saying something true.

- **Status:** Accepted
- **Date:** 2026-09-10

## Context

ADR 0116 made a cardinality widenable and left one question open in writing:
whether the exchange's collection wants its own name. Until that record the
answer was forced — two link definitions promised that widening was free, both
were wrong, and the declaration would not have started. Now it would.

So the question is no longer what can be built. `order_payment` is named as the
next thing to widen by ADR 0114, by the payment module's own definition, and by
the feature list. Nothing about the mechanism refuses it any more.

What refuses it is the row. A link carries two identifiers and a creation
moment, and no read statement in the package selects the moment. Under a widened
`order_payment` there is nothing in a row to tell the collection the checkout
opened from a collection that answers an exchange — and all three readers take
the first one they get.

## Decision

`order_payment` stays one to one: it binds an order to the collection its
checkout opened, and to nothing else. Money collected against an existing order
for another reason is bound under a link of its own, whose name carries the
distinction the row cannot.

## Consequences

Three sentences in the tree stay true. The refund flow's is the one that
mattered: it takes the first collection and says a second would be "a data fault
rather than a choice", which is an ARGUMENT and not a description. Widening
would have left the behaviour identical and the reason false, and a refund the
operator approved would have come out of the wrong collection with nothing
raised.

An order's payments will live in two relations, so a reader wanting all of them
must ask twice. Nothing asks today; the three readers each want the sale's.

The guarantee `from_uniq` gives is kept. It is the only structural bar to two
concurrent writers binding one order to two collections — the checkout binds
before it authorizes, on purpose — and a widening drops it.

Rolling back a widened link is not merely refused, it can be impossible: rows
written under the wider rule need not satisfy the narrower one, so recreating
the index fails on live data. That is ADR 0116's own safety argument read
backwards.

The read surfaces that lagged ADR 0114 are corrected here rather than left for
the record that finally collects a difference: the admin exchange gains the
moment whose status it was already publishing, and the timeline reports both of
an exchange's endings instead of one. Gap D52 is the class.

An exchange that owes money still cannot be settled, and the blocker is not the
link but the collection: it cannot be abandoned once it has taken anything,
because the rule closing it reads a total that only grows rather than a balance.
The next decision is the payment module's, and its trigger is a collection that
can be withdrawn, re-aimed or reopened once it owes nothing.

Measurement: [measurements/0117](../measurements/0117-why-order-payment-stays-one-to-one.md)

## Rejected

**Widening `order_payment` now that it runs.** It spends `from_uniq`, makes the
release one-way, and leaves no field able to say which collection is which.

**Choosing between two collections by their creation order.** The column exists
and no read selects it, and identifiers order to the millisecond at best.

**Deciding the second link's name and ends here.** It has no consumer yet, and
this repository does not publish a name before something reads it.

**Leaving ADR 0114's stale surfaces to that later record.** They are wrong now,
and the endpoint table cannot see it: it is derived from the response type.
