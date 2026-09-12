# What a cart never said

Evidence for [ADR 0153](../adr/0153-a-shop-can-see-where-its-carts-go.md).

Measured 2026-09-12, while choosing the next item from the feature list's A
section. Four candidate rows were measured against the tree in parallel and each
measurement was then given to a second reader whose instruction was to refute it;
three of the four proposals did not survive that pass intact, and the corrections
are why this slice is shaped the way it is.

## The row was right about the absence and wrong about the order

The A8.7 row says three things are missing — the contract, the plugin, and the
cart events themselves — and proposes the contract first.

The absence is real and was verified by searching rather than asserted:

```
$ grep -rl 'core/eventbus' internal/modules/cart/
(no match)
```

Eight topics were published by this repository before this slice, and none of them
came from the cart. So the cart module, which every shopper touches first, was the
one module that told nothing about itself.

The proposed ORDER is what did not survive. The adversarial pass built it: an
`Analytics` interface plus an `AnalyticsEvent` type in `core/provider`, with the
three matching lines added to `internal/arch/testdata/published-names.txt`, and no
implementation and no caller anywhere.

```
$ go test ./internal/arch/ -count=1
ok
```

The whole suite is green. The reason is that the "every produced capability has a
consumer" invariant is enforced on three surfaces — interop registrations, event
topics and link definitions — and a provider interface is none of them; the
published-names gate only asks for a LINE in the ledger and says so about itself.
So the row's first slice ships a name ADR 0026 promises to keep until 1.0.0, for a
consumer that does not exist, and nothing in this repository can say so.

That is why the slice is inverted: the missing half is the one nothing can fake.

## What the cart could carry, and what it could not

The cart has no sales channel. ADR 0146 established that and it still holds — the
channel is read where the catalog is touched, and nothing on the creation path
touches one — so the funnel's dimension is the REGION, which every cart has from
its first moment and never changes.

The totals were refused as a payload field for a reason that is visible in the
model: the cart carries a `Revision` and a `TotalsRevision`, and `TotalsStale`
exists precisely because the two can disagree. An amount stamped at creation would
be wrong for most carts and right for none in particular.

## The house pattern, and the fourth module to carry it

Three modules already publish, and all three do the same two things: a row in the
outbox INSIDE the transaction, and a direct publish after the commit. The payment
module's own note says why it builds the payload in ONE place — the order module
builds it twice by hand, and nothing compares the two copies — so this is the
third module to follow that note rather than the first to repeat the mistake.

The cart's creation had to become a transaction to join them. It was a single
insert; an event promised for a row that may roll back is the exact fault the
outbox exists to prevent.

## Two gates fired, and one sentence was already stale

`plugins/webhookout` refuses, in both directions and at build time, to let a
published topic go unforwarded. Adding two topics therefore added two
subscriptions, two constants and two ledger entries to a plugin this slice is not
about — and one entry in that plugin's `unresolvableNames`, because the cart's
outbox writer takes the topic as a PARAMETER and the scanner cannot resolve it
statically. That is the same hand the order, payment and fulfillment repositories
already carry, and the cart is the fourth.

The stale sentence was found by the adversarial pass, in the godoc of
`subscriberlessPublications`: it priced the published topics at FOUR while the
tree held eight, and named two subscribers out of five. It had been wrong for
several slices. This is the D81/D85/D86 class — a published claim that outlived
its decision — and the number is gone rather than corrected, because the census
below it counts the tree.

Two hand-written counts in `internal/arch/module_sql_test.go` and one in
`plugins/webhookout/module.go` were also false the moment this slice landed; the
count gate caught all three, which is the difference between a number that names
its population's path and one that does not.

## Why the consumer stores rows

The bus delivers AT LEAST ONCE and orders nothing. The publishers derive their
event id from the record, so the same event arrives twice in the ordinary case —
once from the direct publish, once from the relay if the first was lost.

A counter incremented per event is therefore wrong by construction, and it is the
shape the row's own wording invites ("a plugin subscribing to order.placed"). The
table keys on the event's id with `ON CONFLICT DO NOTHING` instead, and the funnel
is a GROUP BY: idempotency is a property of the constraint rather than an argument
about arithmetic. The mutation that removes the conflict clause fails the
integration test with a duplicate key, which is the proof that the clause is what
does the work.

An event with NO id is refused rather than written. That is not hypothetical in
this tree: the product module publishes three topics with no id at all.

## The mutations

| # | Mutation | Result |
|---|---|---|
| 90 | the creation writes no outbox row | **bit** (4) |
| 91 | the direct publish builds its own payload | **bit** (1) |
| 92 | the event id is keyed on the cart alone | **bit** (1) |
| 93 | the outbox failure is swallowed | **bit** (1) |
| 94 | a refused completion publishes anyway | **bit** (1) |
| 95 | the cart service accepts a nil bus | **bit** (1) |
| 96 | an event with no id is counted | **bit** (1) |
| 97 | the order event is read with the CART's moment key | **bit** (2 + e2e) |
| 98 | the redelivery is not dropped | **bit** (1) |
| 99 | the window is closed on both ends | **bit** (1) |
| 100 | the day is derived from `now()` | **bit** (1) |
| 101 | webhookout stops forwarding the cart topics | **bit** (2) |
| 102 | the funnel endpoint loses its scope guard | **bit** (e2e) |
| 103 | the completion publishes the creation's topic | **bit** (1) |
| 104 | the plugin subscribes to a misspelled topic | **bit** (2) |

Mutation 97 is the one the design is most exposed to. The two payloads spell the
moment differently — the cart says `occurred_at`, the order says `placed_at` — and
a plugin that may not import either module has to carry the difference by hand. A
handler reading the wrong word refuses every order this shop places, silently, in
a log line nobody reads, with the funnel's numerator stuck at zero. Its witnesses
are deliberately two: a unit test whose subject is the difference itself, and the
end-to-end proof, because the unit test is written against the same hand-repeated
constant it is checking.

Mutation 102 is the one nobody had to write a test for. The authorization matrix
walks the ROUTER and refuses an unclassified endpoint, so a new admin route enters
its population by existing. That is what a derived population buys, and it is the
opposite of the three stale counts above.

Mutation 104 is the hand-repeated constants' guard from one side only.
`TestEverySubscribedTopicHasAPublisher` resolves a subscription's name statically
and fails when nobody publishes it, so a typo in the plugin fails the build. The
reverse — the cart module RENAMING a topic — is caught by the same gate from the
other direction plus webhookout's, which is why the rename is expensive on
purpose.

## What is NOT closed

- **A missed event leaves the funnel one short, forever.** The bus does not
  redeliver on a handler error and there is no repair path. The relay covers the
  ordinary loss; a reconciliation job would be a second history of the same facts.
- **The funnel starts at installation.** It says nothing about carts a shop
  opened before the plugin was named in `PLUGINS`.
- **A cart opened on Monday and completed on Tuesday is two different days.**
  Dividing completions by creations inside one day is an approximation, and the
  endpoint leaves it to the caller rather than hiding it.
- **There is still no `Analytics` CONTRACT.** A shop that wants its events in a
  vendor's product uses the webhook plugin. Whether a provider slot is the right
  shape for that is ADR 0018's question and this slice does not answer it.
- **No storefront funnel, and no per-customer view.** Both would need an identity
  in the payload, which is the thing it deliberately does not carry.

## What was not measured

Whether `cart.created`'s volume is a problem for a real installation's webhook
queue. The cost is named rather than quantified: one delivery per opened cart, at
whatever rate a shop's storefront opens them.
