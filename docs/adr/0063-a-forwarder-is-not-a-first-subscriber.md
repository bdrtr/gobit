# ADR 0063 — A forwarder is not a first subscriber

**Summary:** The inventory event and the file event are not published, and the
outbound-webhook plugin may not stand in as their first subscriber. It costs
gobit two events it could have shipped green, and a gate holds the rule.

- **Status:** Accepted
- **Date:** 2026-09-08
- **Closes:** gaps.md B7 and B15, as decisions rather than as gaps
- **Extends:** ADR 0051, which judges the one consumer gaps.md named

## Context

Two gap rows asked for an event and neither could land: a published topic no
production file subscribes to is refused, and the exemption map that would
excuse it is empty by policy. `plugins/webhookout` became installable on
2026-09-08 and looked like the answer, because it forwards gobit's events to
receivers an operator registers. It is not one, and the reason is measured: the
plugin's own gate fails the build for a published topic it does not forward, so
its subscription is COMPELLED. Every future topic arrives with a subscriber
already attached, and a fifth topic published and forwarded and touched by
nothing else leaves the whole of `internal/arch` green.

The one consumer the ledger named for the inventory event — the back-in-stock
waitlist, gaps.md C1 — is judged by ADR 0051 and not by this row: it would store
a contact detail a stranger typed, unverified and with no unsubscribe, the class
three files already refuse by name. The other named
consumer needs a fan-out the bus does not do, nothing indexes stock, and the
in-stock filter reads inventory per request. The file module's side is emptier
still: its one cross-module relationship is read synchronously both ways.

Measurement: [measurements/0063](../measurements/0063-a-forwarder-is-not-a-first-subscriber.md).

## Decision

**Neither the inventory event nor the file event is published, and B7 and B15
close as decisions.** What was missing was never the topic; it was somebody
inside gobit who would act on it.

**A generic forwarder may not be a topic's only subscriber.**
`TestEveryTopicHasASubscriberThatChoseIt` refuses it. A forwarder carries every
topic by construction, so routing one through it reaches
`subscriberlessPublications`' state without the reason ever being written.

**The named trigger for the inventory event is a subscriber gobit may lawfully
have**: an identity bound at the storefront, which turns the waitlist's row from
a stranger's e-mail into a customer the framework already holds and lifts ADR
0051's refusal. It is observable — ADR 0057's residue closing.

**The named trigger for the file event is a second holder of an upload's bytes
or address inside this repository.** Today exactly one holds one — the product
image record, written in the same request as the upload. The concrete case is a
cache in front of the object store: `plugins/files3` hands serving to a CDN it
never purges.

**The movement ledger half of B7 is separated and stays OPEN.** It is a table
with an internal reader and the event gate never touched it; the row claimed one
blocker for both halves and was wrong about this one.

## Consequences

- **Two events that would have passed every gate here are not shipped**, and an
  installation wanting stock over a webhook still cannot have it. That is the
  price, refused for want of a consumer rather than of a receiver.
- **The gate credits nobody for a relayed subscription.** `core/plugin`'s host
  hands its caller's topic to the bus, so one site resolves to every plugin's
  topics; counted plainly it blinded the gate on the case it was written for. A
  site whose topic is a parameter is skipped, and a floor holds the skip.
- **`ForwardedTopics` stays the whole set**, and a future forwarder must be
  listed; a listed component subscribing to nothing fails.

## Rejected

- **Publish the events and let webhookout subscribe.** It retires the rule.
- **Write the two topics into `subscriberlessPublications`.** It buys an event
  nothing in gobit uses at the cost of the map's emptiness.
- **Build the waitlist as the consumer.** ADR 0051 does not forbid it — it names
  a PROVEN destination as a different write, to be judged again. What refuses it
  is the tree: no identity is bound anywhere, so it would never actuate.
- **Defer with "when somebody needs it".** Not observable; closing by fiat.
