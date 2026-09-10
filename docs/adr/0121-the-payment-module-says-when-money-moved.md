# ADR 0121 — The payment module says when money moved

**Summary:** The payment module publishes a capture and a refund, naming the
collection and no amount, and the order module subscribes to bring its recorded
money up to date. It costs two published topic names and buys a report that
stops going stale in silence.

- **Status:** Accepted
- **Date:** 2026-09-10

## Context

An order's summary keeps what was collected and what went back. Two flows wrote
it — the checkout clearing a cart and the returns flow making a refund — and the
payment module publishes routes that capture and refund directly. Neither flow
is on those paths, so money moved and the order's record never learned.

ADR 0022 chose the flow deliberately and named the alternative in the same
breath: a subscriber is "the better home", it covers the whole lifetime rather
than one moment, and the merge semantics of the order's write were designed for
its delivery guarantees. It refused it for one reason only — the payment module
published nothing at all — and left the question that had to be answered first:
what does a payment event carry. Deferring a third time would be what ADR 0063
refuses: putting off a thing until somebody needs it, when somebody does.

## Decision

The payment module publishes `payment.captured` and `payment.refunded`, each
naming the payment collection and the moment and carrying no amount. The order
module subscribes to both, reads the collection's cumulative totals through the
query layer, and reports them to its own summary.

## Consequences

The event carries no figure, and that is what makes it correct rather than merely
small. A refund is deliberately not idempotent, so an amount would be an
increment; the bus delivers at least once, and a redelivered increment reports a
number that never happened. The cumulative total lives on the collection, the
order's merge keeps the larger of the two, and a payload without an amount makes
the wrong shape unwritable.

It settles the forwarding question before it is asked: every published topic is
forwarded by the outgoing-webhook plugin, so money in the payload would go on the
wire to endpoints an operator registered. An identifier does not.

The subscriber reaches the order BACKWARDS over `order_payment`, because the
collection's reference carries the CART's identifier — the published description
of that parameter said "an order id in practice" and is corrected here. Going
backwards needed one thing the payment module did not offer: a filter on the
collection's own id.

ADR 0119 is not bent by this: it forbids an order row from holding a payment
figure as its own TRUTH, not from holding a report, and the subscriber produces
the report by asking at the moment it writes. The tension with ADR 0022's defence
of the copy — a divergence is detectable because there are two — is real and
neither record had stated it: 0119 refuses new mirrored columns, 0022 defends the
existing report, and both stand.

The event bus becomes REQUIRED for the payment module: a lost money event has no
compensation, and a module starting without a bus would look healthy and say
nothing.

ADR 0022 is superseded and the write it placed in the saga is now redundant; it
stays, because two writers of one cumulative figure cannot disagree.

Measurement: [measurements/0121](../measurements/0121-what-a-payment-event-carries.md)

## Rejected

**An amount in the payload.** Increments cannot survive at-least-once delivery,
and money would leave the installation through the webhook plugin.

**A third topic for the authorization.** No money moves at a hold, and ADR 0054
fixed the money-moment vocabulary at two for that reason.

**A reconciliation job that repairs the totals.** It makes the report eventually
true and leaves every reader between two repairs reading a number that is not.

**Resolving the order through the collection's reference.** It holds the cart's
identifier, so the reader would find carts and miss orders.
