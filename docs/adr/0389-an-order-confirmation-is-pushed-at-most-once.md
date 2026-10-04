# ADR 0389 — An order confirmation is pushed at most once

**Summary:** The web-push plugin records an `order.placed` event's id before it pushes, and
pushes nothing for an id it holds or an order older than the push's four-hour TTL.

- **Status:** Accepted
- **Date:** 2026-10-05
- **Amends:** [0018](0018-web-push-is-a-device-registry-not-a-channel.md), whose deferred delivery ledger this settles for the order confirmation

## Context

The outbox delivers every event twice under one id, once from the publish
after the commit and once from the relay, and the bus deduplicates nothing by
id (ADR 0386). The analytics and webhook plugins and the notification module
each key a record on the event or the order it names; the web-push plugin,
whose ledger ADR 0018 deferred, kept none and pushed an order's confirmation to
an online device twice (D236). The push's `Topic` header replaces only a push
the push service has not yet handed to the device. An outbox or bus
dead-letter redrive and a Redis consumer back from an outage deliver an event
hours after its order, and the push is sent with a four-hour TTL because a
late message is worse than none.

Measurement: [measurements/0389](../measurements/0389-an-order-confirmation-is-pushed-at-most-once.md)

## Decision

The web-push plugin writes the event's id into `webpush_claimed_event` before
it pushes an order's confirmation, only when the customer has a device, and
pushes nothing for an id the table holds or an order placed longer ago than the
push's TTL. A record that cannot be written is returned to the bus as an error,
and a row is deleted a day after it was written by the handler that writes a
later one.

## Consequences

- An order is pushed once whichever delivery arrives first; two that arrive
  together are decided by the table's primary key, the second waiting for the
  first's INSERT.
- The push is at most once: a process that stops between the row and the
  fan-out, or a push service that refuses a device, leaves that push unsent,
  and the relay's delivery no longer sends it again.
- A failed device read or record is returned to the bus, which calls the
  handler twice more (ADR 0240); both come before the fan-out, so a retry
  cannot push twice. A failed push is still logged, not returned.
- A row says a fan-out was claimed, about to start, not that a device received
  anything, so it carries no status and nothing resends from it.
- An order placed more than four hours before its event arrives is not pushed,
  a redriven one included. The horizon is the push's TTL, read from `placed_at`
  in the order's payload; a test holds the plugin's field names to the order
  module's constants.
- A `placed_at` that cannot be read is pushed and logged at WARN; the record
  still stops its second delivery, though not one that arrives after a day.
- The retention is six times the horizon, so an id the table has forgotten
  belongs to an order the age check refuses; a test fails when the retention
  falls to the horizon or below.
- An event with an empty or blank id is not pushed. The bus fills an empty id
  and the outbox refuses one, neither refuses a blank one, and a CHECK does.
- The table holds an event id and a moment, no customer and no device, so an
  erasure has nothing to remove from it.
- The plugin's migration 000002 adds the table; rolling it back loses the
  record and keeps the devices.
- No setting, route or error code is added; the broadcast is not an event and
  is not recorded.

## Rejected

- The fast path marking its outbox row: a write per event in every publishing module for one subscriber's fault; the in-memory bus returns before its handlers run, and a redrive still delivers twice.
- Deduplicating in the bus: the in-memory backend would hold every id it saw and the Redis one a shared set, for every subscriber's sake.
- Recording after the fan-out: two deliveries that arrive together both find no row and both push.
- Reading the record before writing it: two deliveries that arrive together both read nothing.
- A record per device and event: it keeps a device's id per order for the erasure contract to sweep, to retry a push the plugin never retries.
- A row with a sent or failed status: a fan-out has no single outcome (ADR 0018).
- A row for a customer with no device: it records a push that was never attempted.
- Returning nil on a failed record: the push the bus's retry can still make is lost.
- A horizon of a day: it pushes what the push service would itself have dropped after four hours.
- Refusing an unreadable `placed_at`: a change in the order module's format would stop every push without an error.
- A retention job: a schedule, a lock and a history row for a table that grows only when the handler writes.
- A setting for the horizon or the retention: an operator who sets the retention below the horizon brings the double push back without seeing it.
- The `Topic` header alone: it replaces only a push not yet handed to the device.
