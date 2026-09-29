# Thirteen handlers and one try — measured 2026-09-29

The evidence behind [ADR 0240](../adr/0240-a-failing-handler-is-called-again.md)
and D163.

## 1. The policy that was

`core/eventbus`'s package comment, "Error and retry policy": a handler's error
is logged and the event counts as processed; no backend retries. The Redis
backend ACKs in a `defer` inside `dispatch`, whatever the handlers return, and
takes over only ANOTHER consumer's pending message after `ClaimMinIdle`, at most
`maxDeliveries` (3) times. The in-memory backend delivers at most once.

An outbox topic is delivered twice in normal operation: the module publishes
after its commit, that publish does not mark the outbox row, and the relay
publishes every unmarked row within a minute (measurements/0164). Outbox topics:
`order.placed`, `order.line_canceled`, `fulfillment.canceled`,
`payment.captured`, `payment.refunded`, `cart.created`, `cart.completed`. The
three `product.*` events are published directly, once.

## 2. The handlers

A read-only audit of every non-test `.Subscribe(`: 25 call sites, 13 handlers.

| Handler | Can fail on a passing fault | What was lost | Other recovery | Its comment |
|---|---|---|---|---|
| order `HandleMoneyMoved` | catalog read, summary write | the paid/refunded summary | the relay's delivery; the next money event re-reads totals | "an error would put the bus into a retry loop" |
| notification `OrderPlaced` | contact read, delivery claim, provider send | the confirmation e-mail; a claimed row blocks later tries | the relay's delivery, before the claim only | "not a retry request" |
| ordercancel `HandleLineCanceled` | links, committed quantities, lines, put-back | written-off units' way back to the shelf | the relay's delivery; a later act on the line recomputes the target | "those the bus should try again" |
| ordercancel `HandleFulfillmentCanceled` | the same, per line | a canceled parcel's units | the same | "those the bus should try again" |
| returns `HandleFulfillmentCanceled` (ADR 0239) | replacement read, put-back, recall | a canceled replacement parcel's units | the relay's delivery | "logged by the bus and not retried" |
| giftcardsale `HandleCaptured` | reads, issue, mail | a sold card | the sweep job (ADR 0212) | accurate |
| webpush `onOrderPlaced` | none, returns nil | — | — | "an error makes the event bus retry" |
| webhookout `onEvent` (10 topics) | the enqueue | the event's webhooks | the relay's delivery on 7 of the 10 | accurate; its "three of the six" count was stale |
| searchpg `productWritten`, `productDeleted` | catalog read, upsert, delete | the index row | the manual reindex | accurate |
| analytics (3 topics) | the insert | a funnel row | the relay's delivery | accurate |
| examples/plugin | none | — | — | — |

No handler retried inside itself, and no helper for it existed.

## 3. The policy now

`invokeHandler`, shared by both backends: call; on an error that is not
`KindInvalid`, wait 250 ms and call again, then 1 s and call a third time; log
the last error with `attempts`. A panic is logged once and not repeated. Each
call gets its own shallow copy of the event.

| Test | Holds |
|---|---|
| `TestAFailingHandlerIsCalledAgain` | two passing faults, three calls, no error logged |
| `TestAHandlerThatKeepsFailingIsCalledThreeTimesAndLogged` | three calls, the last error logged with `attempts=3` |
| `TestAnInvalidEventIsNotTriedAgain` | an invalid event once, a panic once |
| `TestEveryAttemptGetsItsOwnCopy` | the second call sees the original data |
| `TestInMemoryHandlerErrorIsLoggedAndIsolated` (existing) | the failing handler's neighbour runs once, the error is logged |

The comments that described the old policy were changed with it: the package
comment and `Handler`'s, the two ordercancel handlers, the order payment
handler, webpush, webhookout (and its count, now seven of the ten),
notification, searchpg, analytics, the returns recall, `docs/extending.md` and
`docs/known-limits.md`. The ADRs and measurements that describe the old policy
are the record of their day and were not.

## 4. Mutations

| # | Mutation | Killed by |
|---|---|---|
| B1 | no retry | `TestAFailingHandlerIsCalledAgain` and two more |
| B2 | one call too many | `TestAHandlerThatKeepsFailingIsCalledThreeTimesAndLogged` |
| B3 | an invalid event retried | `TestAnInvalidEventIsNotTriedAgain` |
| B4 | a panic retried | the same |
| B5 | one copy of the event for every call | `TestEveryAttemptGetsItsOwnCopy`, `TestInMemoryHandlersGetIndependentData` |
| B6 | the last error logged below ERROR | `TestAHandlerThatKeepsFailingIsCalledThreeTimesAndLogged` |
| G1 | 0239's row back to `current` | `TestTheADRIndexNamesEveryAmendment` |

Seven mutants, all killed on the first run.
