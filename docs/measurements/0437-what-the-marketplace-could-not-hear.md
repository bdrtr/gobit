# What the marketplace could not hear — measured 2026-10-11

Evidence for [ADR 0437](../adr/0437-a-parcels-delivery-and-a-returns-moves-are-on-the-bus.md)
and gaps D276, D277 and D278. Read on a tree at 3a366fa9 (ADR 0435 landed); the
integration lanes ran against Docker's `postgres:16-alpine` through
testcontainers.

## 1. The fault, from outside

A marketplace (kayi) ran as a separate process beside gobit, its own
repository and `go.mod`, talking to gobit over HTTP and the signed outbound
webhook alone. It holds a seller's payout until a week after the seller's units
reached the buyer and reverses it when they come back.

- **Delivery was polled.** `MaturePayouts` in its `kayi/payout.go` reads every
  pending payout's parcels with `GET /admin/v1/fulfillments?reference=` on
  every tick and compares each parcel's status with `"delivered"`, a value
  gobit had not published, held in its `kayi/unpublished.go`. Its godoc says
  why: `webhook-out` forwarded `fulfillment.canceled` and
  `fulfillment.returned` only.
- **A return it did not open was heard late or never.** Its `RefundReturn`
  attributes a return it has no row for from gobit's record when the operator
  asks it to refund; one opened at the storefront or in the panel and never
  refunded through it is never heard. The report's step R opened a return
  through gobit's admin API and measured the attribution (four calls).
- **What the report counted.** 130 calls from the marketplace to gobit over
  the scenario; the webhook job's first delivery of 22 events at 58.2–60.0
  seconds; `fulfillment.delivered` first on its list of changes that would
  remove work (the report's own classification: none required).

The files, by `grep -rl` in its tree:

```
"delivered", parcelDelivered     kayi/payout.go kayi/unpublished.go
orderParcels (the poll)          kayi/gobitread.go kayi/payout.go
a return it did not open         kayi/returns.go
```

## 2. Why three topics and not five

The design drafted five: `fulfillment.shipped`, `fulfillment.delivered`,
`order.return_opened`, `order.return_received`, `order.return_canceled`. The
measured code reads a delivery and needs to hear a return it did not open;
nothing in it reads a collection or a receipt, and a return's refund already
publishes `payment.refunded` with the collection. `order.return_canceled` is
kept with the opening so that a receiver that heard a return open hears how a
return that brought nothing back ended.

## 3. Nothing inside gobit subscribes to them

On the base tree, `grep -rln 'fulfillment\.delivered\|return_opened\|return_canceled\|order\.return'`
over the Go and Markdown files outside the ADRs and measurements answers
nothing. The production `Subscribe` sites are the notification module
(`order.placed`, `order.completed`), web-push (`order.placed`), the order module
(`payment.*`), the payment module (`order.canceled`), the gift card sale flow
(`payment.captured`), the cancellation flow (`order.line_canceled`,
`fulfillment.canceled`, `fulfillment.returned`), the return flow
(`fulfillment.canceled`), analytics, search-pg and `webhook-out`. No template
or job reads a parcel's delivery or a return's opening; the shopper reads a
parcel's moments from `GET /store/v1/orders/{id}/timeline`.

## 4. The gate, before and after

The four event gates (`TestTheEventTopicsHaveASubscriber`,
`TestEveryTopicHasASubscriberThatChoseIt`, `TestEverySubscribedTopicHasAPublisher`,
`TestATopicPublishedTwoWaysHasOnePayloadBuilder`) passed on the base, run with
`-count=1`.

**Experiment 1** — the three publishers and `webhook-out`'s three
subscriptions, no map entry:

```
--- FAIL: TestEveryTopicHasASubscriberThatChoseIt
    the "fulfillment.delivered" event's ONLY subscribers are generic forwarders
    (plugins/webhookout/plugin.go). ... either write the subscriber that acts on
    the event, or write the topic into subscriberlessPublications with the
    installation that listens (ADR 0063).
    (the same for order.return_canceled and order.return_opened)
```

**Experiment 2** — the same tree with the three topics written into the old
`map[string]string`, the gate's own advice:

```
--- FAIL: TestTheEventTopicsHaveASubscriber
    the "fulfillment.delivered" topic was counted as subscriberless but it HAS a
    subscriber (core/plugin/plugin.go ×15, internal/app/commandbus.go ×15).
    The reason for the exemption no longer holds ...
    (the same for order.return_canceled and order.return_opened)
```

The way out the second gate named failed the first the moment it was taken
(D276). The first gate's message said the plugin host relayed every topic; the
command bus (`internal/app/commandbus.go`, ADR 0160, 2026-09-12) had relayed
them too since four days after the message was written (D277).

**Experiment 3** — the fix: one `subscriptionCensus` both gates count from,
which skips a relay under the floor that refuses a topic only a relay shows;
`subscriberlessPublications` as `map[string]outsideListener{Listener, Record}`;
`TestAnOutsideListenerIsDecidedInARecord`. All five gates pass with the three
entries, and the new one failed until this record existed.

## 5. The moves and their entry points

| Move | Service method | Entry points | Moment in the payload |
|---|---|---|---|
| parcel delivered | `Service.MarkDelivered`, the proceed branch | `POST /admin/v1/fulfillments/{id}/deliver`, the panel's parcel move | the `delivered_at` the UPDATE returned |
| return opened | `Service.CreateReturnRecord`, after the lines | `POST /store/v1/orders/{id}/returns`, `POST /admin/v1/orders/{id}/returns`, the panel | the row's `created_at` |
| return withdrawn | `Service.CancelReturn`, after the exchange guard | `POST /admin/v1/orders/{id}/returns/{returnId}/cancel`, the panel | the row's `canceled_at` |

A delivery's moment was first the clock's `now`, as the parcel's cancel and
come-back events' were. The fulfillment module's integration test, which then
compared it with the column in SQL, failed on its first run for one of its two
parcels: the column keeps microseconds, the moment is written truncated to one,
and the server reads the payload's nanosecond text rounded to one, so a clock
moment past the half microsecond named an instant no reader of the parcel sees.
The same shape held for `fulfillment.canceled` and `fulfillment.returned` (D278);
section 7 records how all three now carry the stamp the UPDATE returned.

A provider may report a parcel delivered as it is opened (`providerStatus`
maps `coreprovider.FulfillmentDelivered`); no provider in the tree does, and
such a parcel announces nothing.

## 6. Mutants

Each was applied by an exact replacement whose anchor had to match, run with
`-count=1` after a baseline in which every command below passed, and the tree
restored. FU and OU are the fulfillment and order service packages; FI and OI
the new integration tests; WH `plugins/webhookout`; AR the event gates and the
census tests in `internal/arch`; CC the count gate.

| # | Mutant | Lanes | Killed by |
|---|---|---|---|
| P1 | `MarkDelivered` writes no outbox row | FU, FI | `TestADeliveredParcelIsAnnounced` and five more; the integration test |
| P1o | `CreateReturnRecord` writes no row | OU, OI | `TestAnOpenedReturnSaysItOnTheBus` and three more; the integration test |
| P1c | `CancelReturn` writes no row | OU, OI | `TestAWithdrawnReturnSaysItOnTheBus` and two more; the integration test |
| P2 | the delivery publishes on the no-op branch | FU | `TestAParcelReportedDeliveredTwiceIsAnnouncedOnce` |
| P2c | the withdrawal publishes when the return was already canceled | OU | `TestASecondWithdrawalSaysNothingMore` |
| P3o | the opening publishes before its error check | OU | `TestARefusedReturnSaysNothing`, `TestAReturnWhoseEventCannotBeWrittenIsNotOpened` |
| P4 | the delivery's row is written after the transaction | FU, FI | the fake store refuses it: `TestADeliveredParcelIsAnnounced` and five more; the integration test |
| P4o | the opening's row is written after the transaction | OU, OI | every opening fails on the refusal; the integration test |
| P5 | the delivery's id is keyed on the reference | FU, FI | `TestADeliveredParcelIsAnnounced`; the integration test, whose two parcels of one order left one row |
| P6 | the opening's id is keyed on the order | OU, OI | `TestTwoReturnsOnOneOrderSayItTwice` and two more; the integration test |
| P6c | the withdrawal's id is keyed on the order | OU | `TestAWithdrawnReturnSaysItOnTheBus` (OI survives: it withdraws one return) |
| P7o | the opening's moment is the clock | OU, OI | `TestAnOpenedReturnSaysItOnTheBus`; the integration test's SQL comparison |
| P7c | the withdrawal's moment is the clock | OU, OI | `TestAWithdrawnReturnSaysItOnTheBus`; the integration test |
| P7d | the delivery's moment is the clock, not the row | FI | the integration test (FU survived until the fake kept microseconds, section 7) |
| P8 | the delivery carries `tracking_number` | FU, WH | `TestADeliveredParcelIsAnnounced`; `TestTheTopicFieldsAreEveryPayloadsFields`, `TestTheEventTableIsThePublishedPayloads` |
| P8o | the opening carries `reason` | OU, WH | `TestAnOpenedReturnSaysItOnTheBus`; the same two census gates |
| P10 | the withdrawal and its row before the exchange guard | OU, OI | survives, and is equivalent: the guard's conflict rolls the transaction back, row and status together |
| P11 | the delivery's publish builds its own payload | AR | `TestATopicPublishedTwoWaysHasOnePayloadBuilder` |
| P12 | `webhook-out` drops its `fulfillment.delivered` subscription | WH | `TestEveryForwardedTopicIsActuallySubscribedTo` |
| P13 | `TopicFields` drops `order.return_opened` | WH | `TestTheTopicFieldsAreEveryPayloadsFields` |
| P14 | the forwarded-topic count stays thirteen | CC | `TestTheCountsInTheProseAreTrue` |
| P15 | `ForwardedTopics` drops `order.return_canceled` | WH | `TestTheForwardedTopicsAreEveryPublishedTopic` and three more |
| P16 | `docs/extending.md` drops the `order.return_canceled` row | WH | `TestTheEventTableIsThePublishedPayloads` |
| G1 | the census counts a relay as a subscriber | AR | `TestTheEventTopicsHaveASubscriber` ("HAS a subscriber (core/plugin/plugin.go, …"), both census tests |
| G2 | the `order.return_opened` entry is gone | AR | `TestEveryTopicHasASubscriberThatChoseIt`, `TestTheEventTopicsHaveASubscriber`, `TestAnOutsideListenerIsDecidedInARecord` |
| G3 | an entry names a record number no file carries (`0999`) | AR | `TestAnOutsideListenerIsDecidedInARecord` |
| G4 | the record never spells `order.return_canceled` | AR | `TestAnOutsideListenerIsDecidedInARecord` |
| G5 | web-push subscribes to `order.return_opened`, the entry stays | AR | `TestTheEventTopicsHaveASubscriber` ("HAS a subscriber (plugins/webpush/plugin.go)") |
| G6 | `order.return_canceled` stops being published, the entry stays | AR | `TestTheEventTopicsHaveASubscriber` ("NO LONGER PUBLISHED") and two more |
| G7 | every subscription is taken for a relay | AR | both gates and `TestTheSubscriptionCensusSkipsARelay` |
| G8 | the census drops its floor | AR | `TestTheSubscriptionCensusRefusesATopicOnlyARelayShows` |

P12 leaves AR green: an exempt topic is not checked for a subscriber, and the
plugin's own gate is the one that holds the forwarding.

## 7. Amendment: the review's findings and the stored moment

An independent review of the first commit found two faults in the gate, each
reproduced by a mutant that passed every event gate on it:

- **GM1** — `fulfillment.shipped` published in full (one payload builder,
  outbox and direct publish, forwarded, in `TopicFields` and the table, the
  count line at seventeen) with an entry naming ADR 0437, which rejects the
  topic. The record test accepted it, because it read the topic anywhere in
  the record and the Rejected section names it.
- **GM3** — the two return entries naming ADR 0386, whose Status line names
  them only as amended by 0437. Accepted for the same reason.
- **GM2** — a real subscriber to `order.return_opened` in the notification
  module, written as a helper that takes the topic as a parameter
  (`subscribeTo(bus, "order.return_opened", svc)`). The census took the
  helper's call for a relay by its shape and skipped it, so the dead entry
  passed; the same subscriber written as `bus.Subscribe(constant, …)` failed.
  The first gate's godoc still said a missed subscription "cannot produce a
  silent pass".

The record test now reads only the record's `## Decision` section. The census
skips exactly the relays it is handed — `knownRelays`, `core/plugin`
`Host.Subscribe` and `internal/app` `commandBus.Subscribe`, named by package
and function — and fails, naming the site, any other site whose topic is a
parameter or does not resolve, and any listed relay no subscription sits in.

The maintainer then decided that an event's moment is the stored moment.
`CancelFulfillment` and `MarkReturned` now take `canceled_at` and
`returned_at` from the row their UPDATE returns, as `MarkDelivered` does; the
fake store keeps microseconds as the column does, so the service tests see the
difference; and the fulfillment integration test compares each event's moment
with its column in Go at full precision, a comparison in SQL rounding the text
first.

The rule was then swept over the whole population, derived from
`plugins/webhookout`'s payload census (every forwarded topic's fields ending
in `_at`), not from a list:

| Topic | Moment | Source before | Now |
|---|---|---|---|
| `fulfillment.canceled`, `fulfillment.returned` | `canceled_at`, `returned_at` | the clock | the UPDATE's returned row |
| `fulfillment.delivered` | `delivered_at` | the UPDATE's returned row | unchanged |
| `cart.created` | `occurred_at` | `time.Now()` before the transaction | the INSERT's returned `created_at` |
| `cart.completed` | `occurred_at` | `time.Now()` before the transaction | the UPDATE's returned `completed_at` |
| `order.placed` | `placed_at` | the INSERT's `now()`, returned | unchanged |
| `order.line_canceled` | `canceled_at` | the cancellation row's returned `created_at` | unchanged |
| `order.canceled`, `order.completed` | `canceled_at`, `completed_at` | the UPDATE's returned row | unchanged |
| `order.return_opened`, `order.return_canceled` | `opened_at`, `canceled_at` | the INSERT's and UPDATE's returned rows | unchanged |
| `payment.captured` | `occurred_at` | the capture row's returned `captured_at` | unchanged |
| `payment.refunded` | `occurred_at` | the refund row's returned `created_at` | unchanged |
| `product.created`, `product.updated`, `product.deleted` | none | — | — |

Every one of the thirteen topics that carries a moment now has a test that
compares it with its column in Go at full precision: the fulfillment, order
(two tests), cart and payment integration packages, and service tests for the
four that were fixed. None is left: every move's statement returns its row.

Amendment mutants, each `-count=1` after a green baseline:

| # | Mutant | Lanes | Killed by |
|---|---|---|---|
| GM1 | the review's: `fulfillment.shipped` published with an entry naming ADR 0437 | AR, WH, CC | `TestAnOutsideListenerIsDecidedInARecord` (WH and CC stay green, as the review measured) |
| GM2 | the review's: a helper subscriber to an exempt topic | AR | both gates and `TestKnownRelaysAreTheTreesRelays`: "takes its topic from a parameter of" the notification module's `subscribeTo` |
| GM3 | the review's: the return entries name ADR 0386 | AR | `TestAnOutsideListenerIsDecidedInARecord` |
| A1 | the record check reads the whole record | AR | `TestOnlyARecordsDecisionDecidesATopic` |
| A3 | an unlisted parameter site is taken for a relay again | AR | `TestTheSubscriptionCensusFailsClosed` (the helper case) |
| A4 | an unresolved site is skipped | AR | `TestTheSubscriptionCensusFailsClosed` (the unresolved case) |
| A5 | a listed relay that is gone passes | AR | `TestTheSubscriptionCensusFailsClosed` (the gone relay) |
| A6 | `knownRelays` loses the command bus | AR | both gates, `TestKnownRelaysAreTheTreesRelays` |
| A7 | a relay is named without its receiver type | AR | both gates, `TestKnownRelaysAreTheTreesRelays` |
| M1 | the cancel moment reads another stamp, so the clock stands in | FU, FI | `TestACanceledParcelCarriesTheStoredMoment`; the integration test |
| M2 | the come-back moment is the clock | FU, FI | `TestAParcelThatCameBackCarriesTheStoredMoment`; the integration test |
| M3 | the delivery moment is the clock | FU, FI | `TestADeliveredParcelCarriesTheStoredMoment`; the integration test |
| M4 | `storedMoment` always answers the clock | FU, FI | the three service tests; the integration test |

A first M1 that passed nothing for the stamp failed the build alone (an unused
variable) and was replaced by the semantic one above.

Second amendment, the cart and the population: the same runner, a green
baseline first.

| # | Mutant | Lanes | Killed by |
|---|---|---|---|
| C1 | `cart.created` takes `time.Now()` again | cart service, cart integration | `TestACartsEventsCarryTheStoredMoments`; `TestACartsEventsCarryTheStoredMomentOnTheRealDatabase` |
| C2 | `cart.completed` takes `time.Now()` again | cart service, cart integration | the same two |
| C3 | `payment.captured` carries a clock read after the INSERT | payment integration | `TestMoneyMovedCarriesTheStoredMomentOnTheRealDatabase` |
| C4 | `order.completed` carries the clock | order integration | `TestAnOrdersEventsCarryTheStoredMomentOnTheRealDatabase` |
