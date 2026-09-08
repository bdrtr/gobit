# Measurement 0063 — Can `plugins/webhookout` be a topic's first subscriber?

Serves [ADR 0063](../adr/0063-a-forwarder-is-not-a-first-subscriber.md), which
closes gaps.md B7 and B15. Everything here was run on 2026-09-08 at base
`698b649`, in a worktree, with `-count=1`.

## 1. The census: what is published, and who listens

Walked over `productionTrees` (`.`, `cmd`, `core`, `internal`, `plugins`);
`examples/` is a separate Go module and is outside every arch scan.

| Topic | Publisher | Subscribers |
|---|---|---|
| `order.placed` | `internal/modules/order/service/events.go` | notification module, `plugins/webpush`, `plugins/webhookout` |
| `product.created` | `internal/modules/product/service/events.go` | `plugins/searchpg`, `plugins/webhookout` |
| `product.updated` | same | `plugins/searchpg`, `plugins/webhookout` |
| `product.deleted` | same | `plugins/searchpg`, `plugins/webhookout` |

Four topics. `subscriberlessPublications` is empty, as the row claimed.

Two publish sites do NOT declare a topic and were confirmed to be forwarding
sites rather than omissions: the outbox relay republishes a name read back out
of a table, and the order module's `WriteOutboxEvent` takes the name as a
parameter its caller decides.

## 2. The claim under test

`plugins/webhookout` subscribes to all four. The question is whether that
subscription is EVIDENCE of a consumer or a mechanical consequence.

It is mechanical. `TestTheForwardedTopicsAreEveryPublishedTopic`, which lives in
the plugin, fails in both directions: a published topic the plugin does not
forward is a build failure. So the plugin cannot decline a new topic.

## 3. The experiment

A fifth topic, `inventory.level_changed`, was published from a production file
and consumed by nothing.

**Step A — the topic alone.** Both gates fail, which is correct:

```
TestTheEventTopicsHaveASubscriber
  [the publish site in product's publishProductEvent]: the
  "inventory.level_changed" event is published but NO PRODUCTION FILE
  subscribes to it.

TestTheForwardedTopicsAreEveryPublishedTopic
  [...] does not contain "inventory.level_changed"
```

**Step B — add it to `ForwardedTopics` and `Setup`, and nothing else.**

```
ok  github.com/bdrtr/gobit/internal/arch       3.639s
ok  github.com/bdrtr/gobit/plugins/webhookout  0.107s
go vet ./...   (clean)
```

The whole of `internal/arch` is green over a topic no code in the repository
does anything with. That is the finding: after webhookout,
`TestTheEventTopicsHaveASubscriber` can never fail for a new topic again.

Mutations were reverted from copies taken aside, not by `git checkout`.

## 4. Were the alternative consumers real? No, and each for its own reason

- **`plugins/searchpg`** indexes `product_id`, a weighted `tsvector` over title,
  handle, subtitle, tags, variants, SKUs and description, and `indexed_at`.
  There is no stock column and no stock predicate. The storefront's in-stock
  filter (ADR 0040) reads inventory synchronously per request through the query
  provider, so it needs no event.
- **The back-in-stock waitlist (gaps.md C1)** is blocked by ADR 0051
  independently of B7, and the tree already says so in three places — the review
  migration, `SubmitInput`'s godoc, and `claimContact` in
  `storefront_schema_test.go` — each naming C1 by number as the thing that fails
  A15. It would store an unverified contact detail with no unsubscribe.
  gaps.md's C1 row says only "waits on B7", which is incomplete.
- **Real-time stock (C16)** needs a fan-out the bus does not do: the Redis
  backend distributes a stream's messages ACROSS a consumer group rather than
  broadcasting, which is written in `core/eventbus`'s own package doc.
- **The notification module** subscribes to `order.placed` only, and stores no
  recipient address at all, by decision.

For B15 the field is emptier. The file module has never imported `eventbus`. Its
single cross-module relationship is read synchronously in BOTH directions: the
product module resolves `file.interop` to verify an upload id before recording
it, and `ImagesOfUpload` answers the reverse over the `upload_product_image`
link. Nothing waits to be told.

## 5. The row was wrong about half of B7

B7 reads "OPEN, blocked by its own first consumer" over both of its halves. The
movement ledger half is not blocked by that: it is a TABLE with an internal
reader, and no event gate touches it. It is also not covered by what exists —
`audit_log` records the REQUEST and not the change, holds no delta and no item,
and never sees a reservation taken by the checkout saga, which carries no admin
actor. The ledger stays open on its own row.

## 6. The gate, and its two floors

`TestEveryTopicHasASubscriberThatChoseIt` in `internal/arch/consumers_test.go`.

**A defect found while writing it, worth recording.** The first version passed
the step-B mutation. `core/plugin`'s `Host.Subscribe` hands its caller's topic
to the bus, and the constant resolver follows a parameter back to the callers,
so that ONE call site resolves to every topic every plugin subscribes to:

```
path="core/plugin/plugin.go" names=[order.placed product.created product.updated
  product.deleted order.placed product.created product.updated product.deleted
  inventory.level_changed order.placed] forwarder=""
```

Counted plainly, webhookout's forwarding arrives a second time under a path that
is not a forwarder prefix, and every forwarder-only topic passes. A site whose
topic argument is a PARAMETER is now skipped as a relay. This is the same class
the two existing gates already name for the publish side.

**Mutation proofs, all with `-count=1`:**

| Mutation | Result |
|---|---|
| Fifth topic, forwarded by webhookout only | FAIL — `the "inventory.level_changed" event's ONLY subscribers are generic forwarders` |
| Same mutation, before the relay fix | PASS — the defect above |
| `genericForwarders` key changed to a path that does not exist | FAIL — `subscribes to NOTHING this scan can see` |
| `relaysItsTopic` forced to `true` | FAIL — four topics reported `visible ONLY through a relayed subscription`, plus the forwarder floor |
| Unmutated tree | PASS |

The population is the published topics, walked from source. It is not derived
from the property audited. The forwarder list is DATA, and floor two is what
keeps a stale entry from making the test quietly weaker — the same instrument
D13's table exemption uses.
