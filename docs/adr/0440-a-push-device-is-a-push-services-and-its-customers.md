# ADR 0440 — A push device is a push service's and its customer's

**Summary:** The web-push plugin stores a device only at an https endpoint on a browser vendor's push service or one the operator lists, reaches a built-in one only at a public address and follows no redirect, and binds a device only to the customer the installation's identity proves.
It costs a browser on an unlisted push service its subscription until the operator lists it, every binding made before this record, and an installation with no identity its customers' pushes.

- **Status:** Accepted
- **Date:** 2026-10-11
- **Amends:** [0018](0018-web-push-is-a-device-registry-not-a-channel.md), whose customer-to-device binding was a believed claim; narrows the historical [0008](0008-musteri-kimligi-guven-siniri.md) and [0051](0051-an-unidentified-write-is-confined-or-inert.md), whose second open defect this closes

Measurement: [measurements/0440](../measurements/0440-what-a-subscription-could-reach.md)

## Context

`POST /store/v1/webpush/subscribe` answers the publishable key, which every
storefront page prints. It stored any absolute http(s) `endpoint`, and the
sender, a default client that follows redirects, POSTed to it on an operator's
broadcast and on its customer's `order.placed`: a server on the shop's loopback
or private network, the cloud's metadata address and wherever a 307 pointed
heard gobit, and the broadcast's counts and a 404's removal told the subscriber
how it went (D292). The body's `customer_id` was stored as written, on ADR
0008's premise that gobit verifies no customer; since ADR 0043 an installation
binds the identity that does, and a stranger who subscribed a device under a
customer's id received that customer's order confirmations, display id and
order id included (D293). A failed push was logged with its endpoint, which is
the device's capability (D294).

## Decision

A device's endpoint has to be an https URL on a host of the push services
Chrome, Firefox, Safari and Edge use, or at an origin `WEBPUSH_PUSH_SERVICES`
lists, when it subscribes and before every push; the sender resolves a built-in
host itself, connects only to an address that is not loopback, private,
link-local, unique-local, multicast, unspecified or otherwise reserved, and
follows no redirect. A subscription naming a customer binds the device only to
the customer `corehttp.ProvenCustomer` finds the request proves, and stores
nothing otherwise; unbinding keeps the endpoint as its proof.

## Consequences

- The vendor hosts are a list in the plugin: `fcm.googleapis.com`, `android.googleapis.com`, `*.push.services.mozilla.com`, `*.push.apple.com` and `*.notify.windows.com`. Any other is refused with `422 webpush_endpoint_refused` naming the host, and a vendor that moves needs a release or a line of configuration.
- An operator's origin is exact (scheme, host, port, no wildcard) and is reached wherever its name resolves: a push service on the shop's network, or a vendor host its DNS answers inside it, is listed to be reached. Plain http is accepted for a loopback origin only, webhook-out's rule (ADR 0435); the end-to-end ground lists its fake that way.
- Every address a built-in name resolves to has to pass, and the connection goes to the address checked, so a name cannot answer the check with one address and the dial with another. A proxy named by `HTTPS_PROXY` is dialed as configured and resolves the push service itself.
- A redirect is a failed push and keeps the device. A row the rule refuses, stored before it or under an origin since unlisted, is removed unsent at its next push and counted as removed; the log names its host.
- An unproven customer is answered as the cart's body answers one: `401 identity_refused` or the identity's own code, `403 identity_mismatch`, `401 identity_not_bound` with none bound, the identity's class when it cannot answer. A body naming nobody is never asked, and `STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM` does not reach the plugin.
- Migration 000003 unbinds every device, since a binding made on the storefront's word cannot be told from a stranger's. The old customer id moves to `unproven_customer_id` with `updated_at` untouched, the evidence an operator reads to find a stranger's binding; nothing pushes by it, an erasure deletes by it, and the operator empties it once reviewed. A device keeps its broadcasts and gets its customer's confirmations again once its browser subscribes with a proof.
- The plugin resolves the identity lazily itself, since a plugin may not import `internal/core/identity`; the comparison is corehttp's.
- A failed push is logged with the cause beneath the client's error, which names the address and not the URL.

## Rejected

- The address rule alone: gobit would still POST to any public host a stranger names, the shop's own services that trust its address included.
- Resolving the endpoint at subscription: the host list refuses more, and a lookup then says nothing about the address the push will dial.
- A wildcard in the operator's list: an origin reached wherever it resolves would admit every name under it, a stranger's too.
- Following a 307 or 308 to a listed host: a push service answers where the browser was told, and a redirect is not that answer.
- ADR 0439's 404 for an unproven customer: the claim is the caller's own, so there is no existence to hide.
- Ignoring an unproven `customer_id`: a 201 for a binding that did not happen is the silent failure the body's unknown-field refusal exists to stop.
- The identity on unbinding: it binds nobody, the endpoint already deletes the device, and a sign-out's beacon sent after the session is gone would leave the device bound.
- Honoring the trust setting: it serves the cart's and b2b's claim, and believing this one is D293.
- Keeping existing bindings for the operator to review: an attacker's binding receives confirmations until somebody finds it.
