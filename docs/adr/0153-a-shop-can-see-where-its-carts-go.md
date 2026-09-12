# ADR 0153 — A shop can see where its carts go

**Summary:** The cart module publishes `cart.created` and `cart.completed`, and
one plugin turns them, beside `order.placed`, into a daily funnel an operator can
read. It costs two more mandatorily forwarded topics and a transaction where there
was one insert, and it gives a shop the denominator of every conversion question.

- **Status:** Accepted
- **Date:** 2026-09-12

## Context

A shop's first question about its storefront is a ratio: of the carts opened, how
many became orders. The numerator has been on the bus since the order module
existed; the denominator was nowhere, because the cart module published nothing
and imported the event bus zero times.

The feature list's A8.7 row proposes the contract first: an `Analytics` interface
in `core/provider`, then a plugin. That order was measured and refused — the
interface was planted with no implementation and no caller, its ledger lines
added, and the whole `internal/arch` suite stayed green. That is a promise kept
until 1.0.0 for a consumer that does not exist, which is what ADR 0063 refuses.

Measurement: [measurements/0153](../measurements/0153-what-a-cart-never-said.md)

## Decision

The cart module publishes two events through the house pattern — an outbox row
inside the transaction, a direct publish after the commit — carrying the cart, its
region and its currency and nothing else. One plugin subscribes to those two and to
`order.placed`, writes ONE ROW PER EVENT keyed on the event's own id, and answers
`GET /admin/v1/analytics/funnel`.

## Consequences

The completion and the placement are kept apart, and that is the most useful
thing the endpoint shows: the saga places the order at its second step and
completes the cart at its last, so an order failing in between leaves a placement
with no completion. Deriving one from the other could not say it.

Counting is a property of the TABLE. The bus delivers at least once and the
publishers derive the event id from the record, so the plugin writes rows with
`ON CONFLICT (id) DO NOTHING` and groups them; a counter would have turned one
redelivery into a ratio that never happened. An event with NO id is refused: an
empty key would take the primary key and make every later event look like a
redelivery of it.

Opening a cart is a transaction now, where it was one insert: a cart written
without its promised event is what the outbox exists to prevent, and the reverse
is its mirror. The service also refuses to be built without a bus — the outbox
covers a lost publish, so a missing bus would lose no event, it would make every
subscriber hear a minute late with nothing saying so.

Two more topics are MANDATORILY forwarded to whatever endpoints an operator
registered, and `cart.created` is the highest-volume topic in the tree: every
abandoned basket is now a webhook delivery. That cost is named in the plugin that
pays it, and it is why there is no per-LINE topic.

The payload carries no money and no identity: the totals move until the last
calculation and the contact address is the module's declared personal data, while
the region and the currency are facts of the cart's first moment and so survive a
redelivery in a way an amount would not.

Fifteen mutations, fifteen bites. Two are worth naming: the plugin reading the
CART's moment key out of the order's event — the two payloads spell it
differently — is caught only end to end and by the one unit test written for it,
and dropping the funnel's scope guard is caught by the authorization matrix,
whose population is derived from the router rather than listed.

## Rejected

- **The row's own order: an `Analytics` contract first.** Verified inert — the
  interface, the ledger lines and no implementation pass every gate.
- **`plugins/webhookout` as the first subscriber.** ADR 0063: a forwarder is the
  same event leaving the building under another name, not a consumer.
- **Per-line cart events.** Every published topic reaches every operator's
  endpoints; a topic per click is a bill somebody else pays.
- **Counters instead of rows.** Correct only if the bus delivered exactly once.
- **Deriving it from the modules' own tables with a job.** A second history of
  the same facts, over rows a shop is allowed to delete.
