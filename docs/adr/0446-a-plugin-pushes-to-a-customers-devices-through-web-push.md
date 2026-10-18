# ADR 0446 — A plugin pushes to a customer's devices through web-push

**Summary:** The web-push plugin registers `webpush.service`, a `*webpush.Pusher` that pushes a template or a written message, with a tag, to the devices bound to a set of customers, with the plugin's key, copy, endpoint rule and clean-up.
It costs a published Go surface on the plugin and a fan-out any installed module may start, and buys a module's pushes without the plugin's table, settings or encryption.

- **Status:** Accepted
- **Date:** 2026-10-17
- **Amends:** [0018](0018-web-push-is-a-device-registry-not-a-channel.md), whose module registered no container name

## Context

depremtek's backend, a consumer, pushes a campaign to an RFM segment's
customers, a parcel's shipment and delivery and an order's cancellation to
its customer, and a back-in-stock alert (D301). The plugin pushed only an
order's confirmation and an operator's broadcast to every device, so the
consumer read `webpush_subscription`, `WEBPUSH_VAPID_PRIVATE_KEY`,
`WEBPUSH_VAPID_SUBJECT`, `WEBPUSH_PUSH_SERVICES` and the key's fingerprint
itself, re-implemented RFC 8291 and 8292, the push-service rule ADR 0440 holds
the plugin to and the template lookup, and deleted gone rows: a second sender
whose every rule could part from the plugin's.

## Decision

The plugin's module registers `webpush.ServiceName`, `webpush.service`, a
`*webpush.Pusher` whose `PushToCustomers` and `PushToCustomer` push a `Push`
to every device bound to the named customers, at most a thousand a call — a
template from `WEBPUSH_TEMPLATE_DIR` filled with its data and rendered in each
device's locale, then its language, then the default, or a written title,
body and url — through the plugin's own sender, rule and removal of devices
that can never be reached, and answer a `PushResult` of counts. A `Push`'s
`Tag` travels in the payload as `tag` and names the push's `Topic`.

## Consequences

- A push refused before a device is read is an error — no module, too many customers, an unknown template, no title or body, a payload too large — and everything after is counts; a device failing is logged by its id.
- A device bound to no customer is never pushed to; a binding is the proven customer's (ADR 0440). What the copy shows on a locked screen is the caller's.
- The fan-out ADR 0051 names a standing authority gains in-process callers; no HTTP surface reaches it.
- A template file's locale is read in lower case, so `order.placed.en-GB.tmpl` serves an `en-gb` device; a device's language serves a regional locale the plugin has no file for, the order confirmation included.
- The payload gains `tag`, absent when none is named.
- A push keeps the plugin's four-hour TTL; the plugin keeps no record of what was pushed, so a caller that must not push twice keeps its own.
- The plugin's settings, table and fingerprint stay unpublished.

## Rejected

- Publishing the device table and the key: the consumer's fault, promised.
- A notification provider for push: ADR 0018's ledger reasons stand.
- A storefront or admin route pushing to customers: a stranger-reachable fan-out.
- Pushing the back-in-stock alert from the plugin on the event: ADR 0386 keeps the plugin's own trigger to one topic.
- A TTL per push: no caller's need measured beyond the plugin's.
