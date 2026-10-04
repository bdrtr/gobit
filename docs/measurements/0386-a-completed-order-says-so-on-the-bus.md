# An order's end on the bus — measured 2026-10-05

The evidence behind
[ADR 0386](../adr/0386-a-completed-order-says-so-on-the-bus.md). Read on the
tree at 9c787e4b, before the change, unless a section says otherwise.

## What the order module published

Three topics, each written into `event_outbox` in the transaction that made it
and published after the commit:

| Topic | Built in | Written by |
|---|---|---|
| `order.placed` | `service/events.go` | the checkout's `CreateOrder` |
| `order.line_canceled` | `service/cancellation_events.go` | a line write-off |
| `order.canceled` | `service/order_canceled_events.go` | both cancels (ADR 0288) |

`order.completed` and `order.archived` existed only as timeline kinds
(`service/timeline.go`). `CompleteOrder` and `ArchiveOrder` ran through
`Service.transition`, which locks the order inside `WithTx`, and published
nothing. Every caller of `CompleteOrder` goes through the service method:
`grep -rl "CompleteOrder("` finds the admin API, the admin surface, the panel's
close screen and the interop surface, and nothing under `internal/workflows`;
the checkout's `Orders` interface leaves it out on purpose. The SQL already
stamps `completed_at`, so no migration is needed.

The shopper's timeline shows a completion and hides an archiving, which it
calls the merchant filing the order away.

## Who subscribes, and who keeps a record of what it handled

| Subscriber | Topics | Absorbs a second delivery |
|---|---|---|
| notification module | `order.placed` | yes: the (template, reference) claim in `service/send.go` |
| `analytics` | two cart topics, `order.placed` | yes: `ON CONFLICT (id) DO NOTHING` in `store.go` |
| `webhook-out` | every topic | yes: `ON CONFLICT (endpoint_id, event_id) DO NOTHING` in `store.go` |
| `search-pg` | three product topics | its handler is written to be (`index.go`) |
| `web-push` | `order.placed` | no: it pushes each delivery |

The order and payment modules and the gift card sale, cancel and return flows
subscribe to payment, cancel and parcel topics; this record does not measure
them, because the completion reaches none of them.

ADR 0063's gate (`TestEveryTopicHasASubscriberThatChoseIt`) does not count the
webhook forwarder as a topic's subscriber, and its exemption map is empty.

## Every outbox event is delivered twice

- The relay reads every row `WHERE published_at IS NULL`
  (`core/eventbus/outbox/relay.go`), and only the relay writes `published_at`:
  `grep -rl published_at` finds `relay.go` and the migrations.
- The relay job runs every minute (`internal/jobs/outboxrelay`).
- Neither bus backend deduplicates by id; the Redis backend carries the id as a
  stream field and nothing more.

So the direct publish and the relay each deliver a row once. The web-push
plugin's `Topic` header lets the push service replace a push that has not
reached the device; a device that was online when the first one arrived shows
the second as well (`plugins/webpush/sender.go`). That is D236.

## Why the web-push plugin is not the consumer

ADR 0051 calls `plugins/webpush` a standing authority by construction: the
plugin binds a device to a customer id the storefront claims and fans every
`order.placed` for that id out to it. A second trigger widens what a hostile
claim receives, and the plugin keeps no record that absorbs the outbox's second
delivery.

## The notification module and the copy

- gobit ships no mail copy (`.env.example`, `SMTP_TEMPLATE_DIR`).
- The SMTP provider refuses a template it was not given
  (`smtp_template_unknown`), and the module writes that as a failed delivery.
- The default `log` provider warns and returns nil for every template.
- The module's subscription is set up at Register. On the Redis backend a new
  consumer group starts at the beginning of its stream, which holds about ten
  thousand entries, so a subscription made only where a template exists would
  replay that backlog the day the copy arrives. The subscription is therefore
  unconditional and the provider is asked per event.

## The webhook plugin's figures, after the change

| | Before | After |
|---|---|---|
| Forwarded topics | 11 | 12 |
| Of them written into the outbox | 8 | 9 |
| Published directly | the three product topics | the same |

Its prose said seven of ten, a figure that predated `order.canceled` (D235).

## The sentences D235 rewrote

- `plugins/webpush/plugin.go`: `order.placed` "is the ONLY order event the
  repository publishes today".
- `internal/modules/order/module.go`: its event section listed `order.placed`
  alone.
- `internal/e2e/order_flow_test.go`: `order.canceled` "does NOT exist today".
- `plugins/webhookout/module.go`: "seven of the ten".
- `service/order.go` and `service/interop.go`: retry safety lives in "the
  idempotency key of the workflow engine", while no workflow calls the method.
