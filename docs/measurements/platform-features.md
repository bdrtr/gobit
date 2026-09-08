# Platform features — measured against the brief, 2026-09-05

Moved out of `docs/gaps.md` on 2026-09-08 unchanged. A measurement is
evidence, not a decision: it is kept in full and read when a number is
questioned, not on the way to understanding the repository.

The brief: an outbound event stream (webhook + NATS) so a customer can wire
their own ERP or Slack; feature flags and A/B testing in the core; multi-store,
multi-currency and multi-language in one installation; and an audit log with a
"what happened on this order" timeline.

### Outbound events: four events, no retry, and no multi-process fan-out

**Exactly four domain events exist repo-wide** — `order.placed`,
`product.created`, `product.updated`, `product.deleted` — published from three
call sites. Payloads are deliberately narrow and every value is a string,
including money and counts.

For an outbound stream, three properties decide the design and all three are
already written down:

- ~~**There is no retry and no dead-letter queue, by explicit decision.** A
  handler error is logged and the event counts as processed; the ADR-grade
  reasoning is the poison pill — redelivery without a DLQ lets one broken event
  lock the consumer. An outbound sender must therefore build its own retry.~~
  **Half wrong, and both halves were corrected on 2026-09-06.** The decision is
  real but it is about the BUS's subscriber side, and this bullet let it stand
  for the whole system — including the outbox relay, which is a different layer
  with a different failure mode. The relay now has both: a doubling backoff and
  a dead letter after ten attempts (B12). The poison-pill reasoning is exactly
  what it is built around — the pill is the reason the ceiling exists, and the
  dead letter is the DLQ whose absence made redelivery unsafe. An outbound
  sender still builds its own retry for what happens INSIDE its handler; what
  it no longer has to build is the retry of the delivery itself.

  The measurement that made this a defect rather than a preference: the relay
  reads the OLDEST pending rows up to its limit, so a limit's worth of
  permanently failing rows fills every batch. Against a real PostgreSQL with
  the previous code, five consecutive passes published nothing and a healthy
  event written behind two poisoned ones finished with `attempts = 0`. The
  backlog did not slow delivery down; it ENDED it.
- **Redis cannot fan out to N processes as configured.** Every subscriber joins
  one consumer group, and a group delivers each message to exactly ONE consumer.
  Different group names fan out; nothing in the repository uses different names.
- **Under Redis the handler context carries nothing** — no request id, no logger,
  no identity, no tracing span. Only the event id and its data cross the process
  boundary, so an outbound sender must read everything from the payload.

No NATS, Kafka or RabbitMQ appears anywhere, including `go.mod`.

~~And one structural blocker for shipping this as a plugin: **the plugin host
cannot register a job.** Its surface is Container, Logger, Setting, AddModule,
AddRoutes and four provider registrations — an outbound-delivery plugin could
mount a route but could not schedule its own retry pass.~~ **Corrected
2026-09-06: the blocker was removed (B13) and the plugin it was blocking now
ships (C5).** `plugin.Host.RegisterJob` exists, and the host's surface is
fourteen methods rather than nine — Container, Logger, Setting, AddModule,
AddRoutes, RegisterJob, Jobs, RegisterCallback, RegisterErrorReporter, Subscribe
and the four provider registrations. `plugins/webhookout` is exactly the
outbound-delivery plugin this paragraph called impossible: it mounts its route
AND schedules its own minute-by-minute delivery pass through that surface, and
`plugins/paymentpaytr`'s hourly `pendingWatch` was the first consumer.

The outbox is the right foundation and it is already there (ADR 0023): the event
is written inside the transaction that promises it, and a relay publishes it.
An external subscriber would hang off that relay — which, as of 2026-09-06,
retries with a growing delay and gives up out loud instead of retrying forever.
~~The one piece of that machinery an operator still cannot reach is the way
back: `Redrive` and `Discard` have no caller outside their tests.~~ **Reachable
since 2026-09-06:** `gobit deadletters` is their caller — a read-only listing
plus a redrive and a discard verb, each of which requires the event id to be
typed back with `-confirm`. The guard is a repeated ID rather than a `-force`
flag on a measured argument about what goes wrong: the mistake these verbs
attract is not "did not mean to discard", it is "meant a different id", and a
constant flag carries no information about the target while migrating into a
runbook line where it stops being a decision.

### Feature flags: there is no substrate, not even a settings table

**Zero flags, and no place to put one.** Eighty-two tables and not one is a
settings or configuration table. Behaviour varies only by environment variables
read once at process start, and **nothing reloads** — no signal handler, no file
watcher, no polling. The plugin set is fixed at startup too.

What CAN change without a restart is DATA, and there are exactly three live
axes: `product.status`, the price-list window evaluated against the clock, and
`sales_channel.is_disabled`, which is applied inside the key-to-channel
resolution query itself. Those are the existing proof that a per-request
database read is affordable on this path.

Two things a flag design has to settle:

- **The evaluation point.** A database-backed flag means a per-request read; the
  sales-channel resolution is the precedent for what that costs.
- **A/B testing needs a stable per-visitor key, and there is no visitor.** The
  storefront identity is a publishable key representing a CHANNEL, not a person
  (ADR 0008). Assignment would have to come from the embedding application —
  which is consistent with ADR 0025 making gobit a library.

The measurement also names `allow_backorder` again, as the live proof of what a
flag without a reader becomes — and the 2026-09-06 sweep found it has three
siblings in the same module, none of which a boolean-column count would have
separated from the thirteen that do have readers. The sweep's own false
positives are the lesson for a flag SUBSTRATE too: eleven of its fifteen naive
findings were response fields reporting an outcome to a client, which is a
different thing from a flag entirely, and a design that cannot tell the two
apart in its own storage will not be able to audit itself either.

### Multi-store, multi-currency, multi-language: two of three

- **Multi-store works today** through sales channels: a publishable key binds to
  channels, and the catalog is filtered on them in SQL — with the visibility
  rule that an unassigned product is visible everywhere and an assigned one only
  in its channels.
- **Multi-currency works**: currencies are a table with their decimal digits,
  regions carry a currency, and prices are per currency.
- **Multi-language does not exist.** There is no locale column, no translation
  table, no `Accept-Language` handling anywhere. A product has one title, one
  subtitle and one description.

That third one is the honest gap, and it is bigger than it looks: translated
content is not a column on the product, it is a decision about where translated
values live and how the storefront selects one — and it touches the search
index, the panel and the invoice.

### ~~Order timeline: the facts exist, scattered, and two of them are unreachable~~ — all four holes are closed, the last two on 2026-09-06

What an order itself records: `placed_at`, `completed_at`, `canceled_at` with a
reason, plus database CHECKs tying each stamp to its status. Returns carry
`received_at` and `canceled_at`; claims carry both transitions.

Four measured holes, and all four are now closed. The last two went on
2026-09-06; the first two went on 2026-09-05 and this section had not been
updated, which is the divergence class this file keeps producing:

- ~~**Archiving leaves no timestamp.** The status flips to `archived`,
  `completed_at` is deliberately untouched, and there is no `archived_at`. When
  an order was archived is not recorded.~~ Closed 2026-09-06 (D5). The stamp
  exists and the timeline emits a dated `order.archived` entry.
- ~~**`order_exchanges.completed_at` and `canceled_at` exist and are never
  written** — there is no Complete or Cancel query for an exchange at all.~~
  Closed 2026-09-06 (D4), by writing one and dropping the other.
- ~~**The money timeline is unreachable through the read layer.**
  `payments.captured_at` and the refund rows are the two facts a support team
  asks for first — "when was it paid", "when did the refund go out" — and there
  is no query provider that exposes them.~~ Closed 2026-09-05 (B6).
- ~~**There is no order↔fulfillment link, and nothing creates a fulfillment for an
  order.** The link definition was assigned to the fulfillment module, which
  never declared it. So "where is the parcel" cannot be answered from an order
  at all.~~ Closed 2026-09-05 (B5, D8).

**And the timeline's documentation described behaviour the code did not have.**
Its godoc said an undated fact — an exchange that finished — came back LAST
rather than being dropped; the entry builder emitted no such entry at all, and
the branch that would have produced it was unreachable. It has been corrected
and the branch is gone, replaced by a DATED withdrawal entry. The timeline had
zero tests of any kind when it was built on 2026-09-05, which is how a false
sentence survived a day in a file everybody had read. It has tests now for the
entry builder; the composed read itself still has none, because it needs a
wired query catalog the module's fake store cannot supply.

The audit log built this session does not close this: it records the REQUEST —
who called what and what came back — and says in its own header that it does not
record the change. It answers "who touched this" and not "what happened".

~~So the timeline is a read-side composition over facts that mostly exist, plus
two that do not: the order↔fulfillment binding, and a money-event surface.~~ The
timeline is a read-side composition over facts that ALL exist now. Both of the
two that did not were built on 2026-09-05, and the two stamps it was reading
around were built on 2026-09-06.

---
