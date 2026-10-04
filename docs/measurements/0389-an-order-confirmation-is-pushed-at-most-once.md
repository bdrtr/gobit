# An order pushed once — measured 2026-10-05

Evidence for [ADR 0389](../adr/0389-an-order-confirmation-is-pushed-at-most-once.md)
and gap D236. Measured on a tree at 5c58cda6 with Docker's `postgres:16-alpine`,
every Go command run as `GOTOOLCHAIN=go1.26.6 GOMAXPROCS=2 nice -n 19`.

## The double push, reproduced

`TestTheOutboxsTwoDeliveriesPushOnce` (`plugins/webpush/once_integration_test.go`)
does what the order module does, on a database of its own: `outbox.Write` in a
transaction that commits, `bus.Publish` of the same event (the fast path), then
`outbox.Store.Relay`, which reads the row the fast path left open. The bus is
`eventbus.NewInMemory`, the push service an `httptest` server answering 201 for
one device bound to the order's customer.

| Tree | `result.Published` | Requests the push service received | Rows in `webpush_claimed_event` |
|---|---|---|---|
| 5c58cda6, the test alone added | 1 | **2** | the table does not exist |
| with ADR 0389 | 1 | **1** | 1 |

The push count is asserted before the table is read, so the red run fails on
"should have 1 item(s), but has 2" first. Every other test of the package was
green on both trees.

## Why it happened

- The order module publishes after the commit (`publishOrderPlaced`) and writes
  the same event to the outbox in the transaction (`recordOrderPlaced`), under
  an id derived from the order (`outboxEventID`).
- The relay selects `published_at IS NULL` and only the relay sets it
  (`core/eventbus/outbox/relay.go`); `Pending.Event()` republishes under the
  same id (`core/eventbus/outbox/outbox.go`).
- The bus fills an empty id and deduplicates nothing (`normalize`,
  `core/eventbus/eventbus.go`).
- The handler kept no record. `Topic` (`plugins/webpush/sender.go`) lets the
  push service replace only a push it still holds; one already shown is shown
  again.
- The other consumers keep one: analytics on the event id, webhook-out on
  (endpoint, event), notification on (template, order).

## When a delivery comes late

| Path | How late |
|---|---|
| The outbox relay's backoff after failed publishes | 1, 2, 4, 8, 16, 32, 60, 60, 60 minutes, then the dead letter: 4 h 03 min over ten attempts (`core/eventbus/outbox/retry.go`) |
| An outbox dead-letter redrive (`Store.Redrive`) | whenever an operator runs it |
| A bus dead-letter redrive (ADR 0273) | whenever an operator runs it |
| A Redis consumer back from an outage | the group resumes where it stopped; a new group starts at `"0"`; a stream keeps about 10,000 entries (`DefaultMaxLen`) |

There is one consumer group per stream and every handler of an event is fed
from one loop, so "a group resuming after the plugin was off" is not a separate
path: the notification module subscribes unconditionally and keeps the group
moving.

## Why the age comes from `placed_at`

`Event.OccurredAt` cannot be used. `Pending.Event()` builds the event from id,
name and data alone, and `normalize` stamps an empty `OccurredAt` with the
moment of the publish, so a redriven event looks new. `placed_at` is in the
payload (`EventFieldPlacedAt`, RFC 3339 in UTC) and crosses the outbox
unchanged.

The horizon is the push TTL, `ttlSeconds` = 14,400 s = 4 h, which
`sender.go` chose because "one delivered two days later is worse than one not
delivered at all". The retention is 24 h, six times the horizon.

## The window two deliveries open

`TestADeliveryWaitingOnAnotherClaimPushesNothing` writes the event's id in a
raw transaction and keeps it open, runs the handler, polls `pg_stat_activity`
until a backend waits on a `Lock` over `webpush_claimed_event`, and commits.

| Handler | Pushes after the commit |
|---|---|
| ADR 0389: `INSERT … ON CONFLICT DO NOTHING RETURNING` before the fan-out | 0 |
| mutant: `SELECT EXISTS` first, then the INSERT, `true` whatever it did | 1 |
| mutant: the record written after the fan-out | 1 |

## Mutants

Each was applied alone to the green tree and the package run with
`go test -count=1` (`-tags integration` for the integration ones, the whole
package every time). Base run green before the first.

| # | Mutant | Lane | Result | Killed by |
|---|---|---|---|---|
| 1 | claim call removed | integration | killed | the reproduction, second delivery, held claim, failed record, retention |
| 2 | claim's `first` ignored | integration | killed | the reproduction, second delivery, held claim |
| 3 | `ON CONFLICT` dropped | integration | killed | claim twice, second delivery, held claim |
| 4 | `ErrNoRows` branch deleted | integration | killed | claim twice, second delivery, held claim |
| 5 | `SELECT` before the INSERT | integration | killed | held claim |
| 6 | record after the fan-out | integration | killed | the reproduction, second delivery, held claim, failed record |
| 7 | claim keyed on `e.Name` | integration | killed | two orders, the reproduction, held claim, unreadable `placed_at`, retention |
| 8 | claim before the device read | integration | killed | nobody to push to |
| 9 | failed record pushes anyway | integration | killed | failed record |
| 10 | failed record returns nil | integration | killed | failed record |
| 11 | failed device read returns nil | integration | killed | failed device read |
| 12 | horizon check removed | integration | killed | order older than the TTL |
| 13 | unreadable `placed_at` refused | integration | killed | unreadable `placed_at` |
| 14 | empty-id guard removed | integration | killed | event without an id (the CHECK refuses, the handler returns the error) |
| 15 | `forget` call removed | integration | killed | retention |
| 16 | `forget`'s `<` made `>` | integration | killed | retention, second delivery, the reproduction |
| 17 | retention bound as nanoseconds | integration | killed | retention |
| 18 | CHECK dropped | integration | killed | blank id refused |
| 19 | 000002's down also drops `webpush_subscription` | integration | killed | rolling the record back |
| 20 | 000002's down keeps its table | integration | killed | rolling the record back, migration reversible |
| 21 | `tooLate`'s `>` made `>=` | unit | killed | `TestAnOrderIsPushedUpToTheHorizon` |
| 22 | retention equal to the horizon | unit | killed | `TestTheRecordOutlivesThePushHorizon` |
| 23 | horizon a day | unit | killed | horizon is the TTL, the boundary table, retention outlives horizon |
| 24 | `fieldPlacedAt` renamed | unit | killed | `TestTheFieldsReadAreTheOrderModules` |
| 25 | 000002's down without its `DROP INDEX` | integration | survived, equivalent | `DROP TABLE` drops the table's indexes |
| 26 | a failed `forget` returned to the bus | integration | killed | a record that cannot forget still pushes |
| 27 | the id guard catches an empty id only | integration | killed | event without an id, blank case (the CHECK refuses, the handler returns the error) |
| 28 | 000002 adds a table its down forgets | integration | killed | migration reversible (the per-step catalog census), the personal-data declaration |
| 29 | 000002 adds an index on `webpush_subscription` its down forgets | integration | killed | migration reversible (the census one step back; a rollback all the way drops it with the table) |
| 30 | a new `field*` constant misspelling the order's field, read in the template data | unit | killed | `TestTheFieldsReadAreTheOrderModules` |
| 31 | a field read by a string literal | unit | killed | `TestTheFieldsReadAreTheOrderModules` |

Mutants 26 to 31 came from the review. No test reached the branches 26 and 27
change (none made the deletion fail, none used a blank id), so a test was added
for each. 28 to 31 test the censuses that replaced the hand-written lists the
migration and field tests first held, and were run against the censuses only.
All six ran on a green base run.
