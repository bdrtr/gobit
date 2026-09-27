# ADR 0218 — A webhook receiver can narrow what it gets

**Summary:** A webhook receiver can filter each of its topics on payload fields
and list the fields it is sent, and an operator can change its topics, filters,
fields and description in place. The fields a filter or a list may name are the
ones each topic carries, read out of the publishers by a census.

- **Status:** Accepted
- **Date:** 2026-09-27

Measurement: [measurements/0218](../measurements/0218-a-receiver-that-asked-for-less.md)

## Context

`plugins/webhookout` forwards every published topic, and a receiver registers
the topics it wants; the queue writes one delivery per receiver whose topics
contain the event's (D149). Within a topic a receiver took every event and every
field but the redacted `customer_id`, and changing a receiver meant deleting it
and registering again with a new secret. `cart.created` is a delivery per opened
cart. Payload values are strings, and each publisher builds its payload in one
function of its module. The user chose a per-topic filter of field values, a
per-topic field list, and a change in place.

## Decision

A receiver's `filters` name, per topic, fields and the values one of which an
event must carry in each, and its `fields` name, per topic, the payload fields it
is sent; the enqueue statement applies both, so an event that does not match is
queued for it not at all and the queued body is the narrowed one. `PATCH
/admin/v1/webhooks/{id}` replaces the topics, filters, fields or description it
names under the receiver's lock, keeping the URL and the secret.

## Consequences

A filter or a list may name only a topic the receiver takes and a field the
topic carries: `TopicFields`, which the listing returns as `topic_fields`. A
census reads each topic's payload keys out of the publishers' builders and
fails the build when the list and the source part, with a named exception for a
key a builder can add and the publisher never has it add. A redacted field is
not among them, so a receiver cannot filter on `customer_id` either.

Values are compared as text, and an event without a filtered field matches
nothing. A redrive sends the narrowed body that was queued. A change applies to
the events after it, and a topic taken away while a filter or a list still names
it is refused. The body a request may carry grows from 2 KB to 64 KB.

Rolling back webhookout migration 000002 drops the filters and field lists and
keeps the receivers, which are then sent every event of their topics whole.

Writing the census showed two documents behind the code: `known-limits.md` said
no receiver chose its topics (D149), and `extending.md` named six subscribers and
six topics of eight and ten (D150). Both now say what the code does.

## Rejected

- **A filter expression language.** A new evaluator and a dependency for what
  field values already say; the user chose equality.
- **A template that renames or adds fields.** A receiver's body would stop
  meaning what the publisher's payload means, and a plugin may add nothing a
  module did not publish (ADR 0001).
- **Filtering and narrowing in the sender.** The queue would hold rows owed to
  nobody, and a redrive after a change would send a body the receiver never saw.
- **Changing the URL in place.** A new destination is a new receiver with a new
  secret, and the deliveries queued for the old one keep its URL.
