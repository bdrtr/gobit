# ADR 0018 — Web push is a plugin-owned DEVICE REGISTRY, not a notification channel

- **Status:** Accepted
- **Date:** 2026-09-04
- **Phase:** after the roadmap

## Context

The first installation that will run gobit at scale sends web push in nine
places: four storefront endpoints, an admin broadcast, and a stock alert. Push
is therefore not a hypothetical; it has a counted consumer, which is the bar
ADR 0009 sets for building anything at all.

The obvious way to add it was the notification provider slot. gobit already has
one — `internal/core/provider.NotificationProvider` — and the round before this
one filled it for the first time with `plugins/notificationsmtp`. Adding a
second provider looked like the same shape of work.

**It is not, and the reason is in the contract's own fields.** A
`Notification` carries `To string`, documented as "an email address or a phone
number". A push destination is not an address. It is three values the BROWSER
mints — an endpoint URL, a P-256 public key and a 16-byte auth secret — and the
framework has to have **stored** them before a send is possible. One customer
has many; they arrive and expire on their own schedule; the push service
reports their death in the response to an unrelated send.

So the question was never "which provider" but "where does that state live".

## Decision

**Web push ships as `plugins/webpush`: a plugin that brings its own module, its
own table, its own storefront and admin routes, its own `order.placed`
subscriber and its own transport. It registers NO notification provider. The
core's channel set, the `NotificationProvider` contract, the meaning of
`Notification.To` and the semantics of `NOTIFICATION_PROVIDER` are left
untouched.**

This is the `plugins/searchpg` shape — a plugin that owns a table, a migration
and an endpoint without being named anywhere except one line in the catalog —
applied to a second problem. The plugin is deletable with `rm -rf` plus that
line.

Three designs were written against this question and each was judged through
three independent adversarial lenses (fidelity to the existing decisions,
silent-failure surface, and whether the real consumer's cases can be
expressed). The two that changed the core scored 8/15; the one that forced push
through the provider slot scored 6/15. The shape decided on here is the one all
three lenses preferred, and each of the three authors had named it as their own
fallback.

## Rejected options

**A. Add `ChannelPush` and a `Channels() []string` method to the provider
contract.** Adding a provider would then require changing every existing
provider — the inverse of what `internal/core/provider`'s own godoc says the
contract is for. Worse, the default `log` provider would have to claim `push`,
so every stock installation (`NOTIFICATION_PROVIDER=log` is the default) would
write a second delivery row and a second "not sent" warning per order, for a
channel with no table, no plugin and no possible recipient.

**B. Add `ChannelPush` with a per-channel `NOTIFICATION_PROVIDER` grammar.**
`email:smtp,pus:webpush` is well formed, every provider id in it resolves,
startup is green — and push is silently off forever. That is precisely the
failure the composition root's own documentation exists to prevent, moved one
level up to the channel key.

**C. Keep the provider slot and put subscriptions in an external HTTP
directory.** The provider would have to accept `Channel == "email"` and deliver
something that is not email, which the contract forbids in writing. It also
moves durable state out of the cluster, against ADR 0015.

**D. A push subscription as an ADR 0005 link.** A link row holds two id strings
and nothing else. The endpoint, the public key and the auth secret have nowhere
to live.

**E. Subscriptions in `customer.metadata` through `core.db`.** A plugin writing
into another module's table is a worse breach than any core change.

**F. A push delivery ledger in v1.** Deferred, not refused. A fan-out has no
single truth value, and a row meaning "at least one device took it" is a lie
the unique key would make permanent. Delivery outcome is still recorded, as a
side effect: a 404 or 410 deletes the row, so the table converges on live
devices.

## Consequences

**The notification module's `sent` status cannot lie about push.** This is the
sharpest consequence and it is the reason the decision is not merely tidy.
`Send` returns a single error, so a fan-out to zero devices would return nil and
the ledger would record `sent` — and because `ClaimDelivery` skips a repeat
claim regardless of the prior status, that false `sent` would **permanently
disable** the resend the module's own godoc says must stay a human's decision.
Not routing push through the ledger is what closes that.

**Two facts about the repository were measured on the way and are worth
recording, because both were invisible before.**

1. `ChannelSMS` is already an instance of the error class ADR 0009 names. Five
   references exist in the whole tree: the constant, two godoc cross-references,
   one mention, and a test asserting that smtp *rejects* it. There is no SMS
   provider and no producer. Extending the channel set was proposed twice in
   this round; the existing channel set already carries an unconsumed member.

2. **Plugin migrations are covered by no arch gate.** Both rollback tests walk
   `moduleNames(t)`, which reads `internal/modules/` only. A plugin that brings
   a table — searchpg today, webpush next — has its up/down pair certified by
   nothing. The plugin therefore carries its own rollback test, and that is a
   requirement of this decision rather than a nicety.

**The VAPID private key is durable state on the order of the database.** Losing
or rotating it invalidates every subscription ever issued, and every user has to
re-subscribe from their own browser. gobit has no other secret with that
property: a JWT secret rotation drops sessions that can be re-created by logging
in, while this one cannot be repaired from the server side at all. It lives in
no migration and in no backup story unless the operator puts it there. Because a
deliberate rotation must still be able to boot, a mismatch is not fatal at
startup; each subscription records the fingerprint of the key it was minted
under, the count is logged at startup, and the send path deletes rows that can
only ever fail — a graveyard that drains itself beats one that is invisible.

**The customer-to-device binding is the same unverified claim ADR 0008
governs.** A caller supplying a customer id is believed, so a hostile binding
would deliver a stranger's order pushes to an attacker's device. That is not
closed here, because closing it means building customer sessions, and ADR 0008
already decided that a half identity layer is more dangerous than none. It is
bounded and made visible instead: an operator can list and delete subscriptions,
the shipped default template carries the display id and item count but **no
money**, and the boundary is pinned with a test named to fail the day customer
sessions arrive. `plugins/webpush`'s subscribe handler joins ADR 0008's list of
places that must change on that day.

The exposure does not widen what a customer-id holder can already learn —
`GET /store/v1/customers/{id}` returns that customer's name, email and addresses
to anyone holding the id today. What is new is that it becomes standing rather
than pull, which is why the remediation endpoints exist.

## Reopening

This decision is reopened by a second consumer of the channel set. The moment a
real SMS provider exists — a producer AND a consumer, not a constant — the
question "should push be a channel" is worth asking again, because the cost that
decided it here is that a channel with one implementation makes every provider
carry a method for it. Until then, the count stands at zero.

It is also reopened, in the opposite direction, by a push delivery ledger
turning out to be needed. That would be a `000002` inside the plugin's own
migrations and would touch nothing else, which is why deferring it is cheap.

## Related

- ADR 0005 — the link schema, and why a subscription is not a link.
- ADR 0008 — the customer identity trust boundary this decision inherits.
- ADR 0009 — a capability with no consumer is an error class.
- ADR 0015 — PostgreSQL is the foundation; state does not leave the cluster.
