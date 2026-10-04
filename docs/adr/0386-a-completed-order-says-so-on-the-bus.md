# ADR 0386 — A completed order says so on the bus

**Summary:** Completing an order publishes `order.completed`, and the notification
module mails the order's address unless the provider says it holds no such template. Archiving publishes nothing, and no generic transition hook is added.

- **Status:** Accepted
- **Date:** 2026-10-05
- **Amends:** [0243](0243-a-failed-confirmation-is-sent-again-by-an-operator.md) and [0317](0317-the-panel-lists-the-notifications.md), whose resend and its panel button now take the completion notice as well as the confirmation

## Context

Placing an order publishes `order.placed` and both cancels publish
`order.canceled` (ADR 0288); completing and archiving published nothing, and
`order.completed` and `order.archived` existed only as timeline kinds, so
nothing could react to an order the shop finished. ADR 0063 refuses a topic
whose only subscriber is the webhook forwarder. The shopper's timeline shows a
completion and hides an archiving. ADR 0051 judges the order mail CONFINED and
the web-push plugin's fan-out a standing authority.

Measurement: [measurements/0386](../measurements/0386-a-completed-order-says-so-on-the-bus.md)

## Decision

Completing an order writes `order.completed`, carrying the order and the moment,
into the outbox in the completion's transaction and publishes it after the
commit, and the notification module mails the order's own address with template
`order.completed` unless the provider says it holds no such template. Archiving
publishes nothing, and the event bus is the transition hook: a transition gains
a topic when a subscriber inside gobit acts on it.

## Consequences

- The API, the panel and the interop surface complete through one service
  method, so all three publish. A completion whose event cannot be written is
  not made.
- The outbox delivers the event twice; the delivery log's (template,
  reference) key sends one mail. A failed one is resent by an operator, as a
  confirmation is.
- An SMTP installation without `order.completed.tmpl` mails nothing and logs
  one line per delivery; the default `log` provider warns once more per
  completed order; another provider is asked for the template like any other.
- A provider says it holds no such template through `provider.TemplateHolder`,
  an optional interface in `core/provider` an out-of-tree provider implements.
- The subscription is unconditional, so a Redis consumer group exists from the
  first start and a copy written later replays no backlog.
- The webhook plugin forwards twelve topics.
- Orders completed before this change are never announced.
- The web-push plugin pushes no completion; its double push of `order.placed`
  stays open as D236.
- Archiving and the return, claim and exchange transitions stay unpublished.
- Nothing outside the order module can refuse a transition; a refusing rule is
  written in the transition, as the cancel's collected-amount guard is.

## Rejected

- A generic `OnTransition(from, to)` hook: every transition reaches it, so it names no consumer, which ADR 0063's gate exists to require.
- A hook run inside the transaction: it puts plugin code under the order's row lock, where a slow or failing plugin holds or refuses an operator's completion.
- A veto hook: the transition's refusals are typed codes a client switches on, and a plugin's refusal is an open set.
- The web-push plugin as first consumer: ADR 0051 names its fan-out a standing authority, a second trigger widens it, and it keeps no record to absorb the second delivery.
- The analytics funnel as first consumer: completion is the operator's act, and the funnel counts the shopper's.
- Publishing `order.archived` for the webhook plugin: ADR 0063 refuses a forwarder as a first subscriber, and nothing in gobit acts on filing.
- Writing `order.archived` into the exemption map: it buys a topic nothing uses at the cost of the map's emptiness.
- Subscribing only where the provider holds the template: a group created the day the copy arrives replays the stream's backlog.
- Skipping every template the provider lacks: a confirmation with no copy is a misconfiguration whose failed record the resend repairs.
- Carrying `customer_id` in the payload: the one subscriber reads the order, and a receiver is not owed it.
