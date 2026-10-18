# ADR 0447 — A module sends one message through a published name

**Summary:** `core/plugin` publishes `NotificationSenderName` and the `NotificationSender` interface, the notification module's one-message surface, so a module or a plugin outside this repository resolves it by a constant and a type the compiler checks.
It costs three published names, and buys a password reset or a refusal mailed without a re-spelled container name and signature.

- **Status:** Accepted
- **Date:** 2026-10-17
- **Amends:** [0137](0137-a-colleague-sets-their-own-first-password.md), whose cross-module surface was in-tree only

## Context

depremtek's backend, a consumer, mails its panel accounts' password resets,
a return's refusal and other messages with a secret or one recipient (D302).
The notification module's `Send(ctx, template, channel, reference, to, data)`
under `notification.interop` was the way, and ADR 0137 named a password reset
its next caller, but the name and the signature were unpublished: the
consumer re-spelled both, and a rename would have compiled and failed at its
first mail.

## Decision

`core/plugin` publishes `NotificationSenderName`, `"notification.interop"`,
and `NotificationSender`, the interface with that `Send`, whose godoc is the
contract the notification module's surface already keeps. An arch test holds
the name to the module's `InteropName` and the module's surface to the
interface.

## Consequences

- A consumer resolves it with `container.Resolve[plugin.NotificationSender](c, plugin.NotificationSenderName)`, on first use, since the notification module may register after it.
- Changing the signature or the name is a breaking change of `core/plugin`.
- The in-tree consumers keep the narrow interfaces ADR 0001 has them declare; the published one is for a consumer outside the tree.
- (template, reference) stays the idempotency key: a consumer's references are its own records'.
- Nothing about delivery changed: the provider, the ledger and the redaction of data values in the default provider's log are the module's.

## Rejected

- `core/provider`: it holds the contracts a provider implements and the core calls; this one a module calls.
- A `Host` method returning the sender: a module, which holds the container and no host, would still need the name.
- A new published package for one interface: ADR 0026 publishes late and narrow.
- An event a mail subscriber answers: ADR 0137's reasons — a secret on a durable, forwarded stream.
