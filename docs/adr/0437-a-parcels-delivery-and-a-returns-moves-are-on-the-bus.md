# ADR 0437 — A parcel's delivery and a return's moves are on the bus

**Summary:** Marking a parcel delivered publishes `fulfillment.delivered`, and opening and withdrawing a return publish `order.return_opened` and `order.return_canceled`, through the outbox, for a consumer outside gobit that `subscriberlessPublications` names.
It costs three topics no subscriber inside gobit acts on, kept published, and an exemption gate that rests on a record's Decision rather than on a subscriber.

- **Status:** Accepted
- **Date:** 2026-10-11
- **Amends:** [0063](0063-a-forwarder-is-not-a-first-subscriber.md), whose exemption map becomes reachable and names a record; [0386](0386-a-completed-order-says-so-on-the-bus.md), whose return transitions gain two topics

Measurement: [measurements/0437](../measurements/0437-what-the-marketplace-could-not-hear.md)

## Context

A marketplace running beside gobit as a separate process settles a seller's
payout a week after the parcel is delivered and reverses it when goods come
back. gobit published a parcel's cancellation and its coming back, but not its
delivery and no move of a return, so the marketplace read every pending
payout's parcels on every tick, compared the unpublished status `delivered`,
and heard a return opened at the storefront or in the panel only when an
operator told it. ADR 0063 refuses a topic whose only subscriber is the webhook
forwarder and names the way out, an entry in `subscriberlessPublications`; an
entry there failed the build when written, because the first gate counted the
plugin host's and the command bus's relays as subscribers (D276), and its
message named one relay (D277). The parcel's cancel and come-back events
carried the clock's nanoseconds where the row stores microseconds, and the
cart's two events a clock read before their transaction (D278).

## Decision

Marking a parcel delivered publishes `fulfillment.delivered`, and opening and
withdrawing a return publish `order.return_opened` and `order.return_canceled`,
each written into the outbox in the transaction that makes the move and
carrying identities and the moment the row stores, as every published event
now does, and no personal data. A topic whose only subscriber is a forwarder passes
when `subscriberlessPublications` names the outside consumer and a record whose
Decision names the topic, and both subscriber gates count from one census that
skips only the two relays it names and fails any other site it cannot read.

## Consequences

- These are the moves the consumer's code needed. A parcel's collection, a
  return's receipt, claims, exchanges, replacements and archiving stay
  unpublished; a return's refund is already `payment.refunded`.
- `fulfillment.delivered` carries `fulfillment_id`, `reference`, `return_id`
  and `delivered_at`, and no tracking number. The return topics carry
  `order_id`, `return_id` and the moment, and not the shopper's reason, note or
  lines. Each moment is the one the move's statement returned, so it equals
  the record's to the microsecond; `fulfillment.canceled`,
  `fulfillment.returned`, `cart.created` and `cart.completed` take theirs the
  same way now, and every topic that carries a moment has a test holding it to
  its column.
- The admin API, the panel and the storefront reach each move through one
  service method, so all of them publish. A move whose event cannot
  be written is not made; a repeat and a refused move publish nothing.
- A parcel delivered before any collection announces its delivery. One a
  fulfillment provider reports delivered as it is opened announces nothing,
  since only marking it delivered publishes.
- `webhook-out` forwards sixteen topics; a receiver names the topics it takes,
  so none hears these unasked. Deliveries keep no order, and a withdrawal may
  arrive before its opening.
- `subscriberlessPublications` holds three entries naming the marketplace and
  this record. An entry is refused when its record is missing or its Decision
  does not name the topic, and when a subscriber inside gobit chooses the topic.
  Whether the marketplace still listens is not observable; a published topic
  stays published.
- A new relay is written into the census's closed list with its reason; a
  subscriber written as a helper that takes its topic as a parameter is refused
  rather than read.
- Moves made before this change are never announced. No migration, no setting.

## Rejected

- `fulfillment.shipped` and `order.return_received`: no consumer's code read either, and a refund already publishes `payment.refunded`.
- Claim, exchange and replacement moves: no consumer acts on one, and they reopen (ADR 0239), so an id keyed on the record drops a second completion.
- A shipping notice as the first subscriber inside gobit: no shopper or consumer asked, and return parcels, replacements and multi-parcel orders come with it.
- A generic `status_changed` topic: ADR 0386's `OnTransition` hook on the bus.
- A delivered-since filter on the parcel list: it keeps the polling and its latency.
- The tracking number, the return's lines, reason or note in a payload: a lookup key to the recipient, a list in a flat map, and the shopper's own words.
- A second map beside `subscriberlessPublications`: two answers to one question.
- Recognizing a relay by its shape: it skipped a subscriber written as a helper.
