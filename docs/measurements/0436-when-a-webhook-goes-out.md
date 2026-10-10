# When a webhook goes out — measured 2026-10-10

Evidence for [ADR 0436](../adr/0436-a-webhook-goes-out-when-it-is-due.md)
and gaps D273, D274 and D275. The tree before the change is 743a1a44 (ADR 0435
on v0.11.0). The servers ran as `cmd/server` with `PLUGINS=webhook-out`, the
in-memory bus and `postgres:16-alpine` in Docker; the integration lanes ran
through testcontainers.

## 1. The fault, from outside

A marketplace running as a separate process beside gobit measured its first
webhook delivery 58.2–60.0 s after the event, and a failed delivery's retry
two delivery passes after the failure in four tries of five, where the ladder
documents one minute. Its report asked for a delivery delay it could set.

## 2. Why the first attempt waited

The subscriber (`onEvent`) only writes the row, due at once
(`next_attempt_at DEFAULT now()`); the one sender was the `webhookout-deliver`
job, `Every` one minute, and the job runner ticks once a minute for every job,
running them one after another with the plugin's job last. A row waited for
the next tick plus the jobs ahead of it in that tick: 0–60 s, and the full
minute when the event is born just after a pass, as the marketplace's tests
were.

## 3. Why a retry took a pass longer (D273)

`markFailedSQL` writes `next_attempt_at = now() + delay` in its own statement,
after the request: the failure is written δ after the pass's claim, δ being the
request plus the row's wait for one of the eight senders. The next pass claims
exactly one tick after the last plus the drift of the jobs ahead of it (o).
The row is due at the next pass only when δ ≤ o(k+1) − o(k); δ is always
positive and o moves by milliseconds, so most steps land a pass late. The
outbox relay escapes it because it records the failure inside the claim's
transaction, whose `now()` is the claim's.

### Reproduced on the default path, at 743a1a44

One receiver answering 500, one `product.created` from `POST
/admin/v1/products`, the server's own runner. Times are UTC, read from the
receiver's log and the server's.

Receiver answering at once (δ of a few milliseconds):

| | at | after the previous attempt | documented |
|---|---|---|---|
| queued | 19:58:57.394 | | |
| attempt 1 | 19:59:56.340 | 58.9 s after queueing | at once |
| attempt 2 | 20:00:56.399 | 60.06 s | 60 s |
| attempt 3 | 20:03:56.323 | 179.9 s | 120 s |

Receiver answering 500 after one second (δ ≈ 1 s):

| | at | after the previous attempt | documented |
|---|---|---|---|
| queued | 20:01:50.987 | | |
| attempt 1 | 20:02:50.857 | 59.9 s after queueing | at once |
| attempt 2 | 20:04:50.823 | 120.0 s | 60 s |
| attempt 3 | 20:07:50.809 | 180.0 s | 120 s |
| attempt 4 | 20:12:50.823 | 300.0 s | 240 s |

Every step of the second run, and the second step of the first, landed one
pass late. The first run's first step made its pass because that tick's jobs
ahead of the plugin ran 59 ms longer than the tick before, more than its δ.

### What the ladder became

| step | documented | with every step a pass late |
|---|---|---|
| 1→2 | 1 min | 2 min |
| 2→3 | 2 min | 3 min |
| 3→4 | 4 min | 5 min |
| 4→5 … 9→10 | 8, 16, 32, 64, 128, 256 min | 9, 17, 33, 65, 129, 257 min |
| 10→13 | 360 min ×3 | 361 min ×3 |
| window | 26 h 31 min | about 26 h 43 min |

`retry.go`'s `firstDelay` and `module.go`'s `every` both said the one-minute
step existed so that the documented schedule was the one the scheduler could
express. The startup log's `retry_window` and the dead letter's message print
26h31m0s. The four-in-five rate the marketplace saw was not measured here; the
5-minute core jobs (`stock-alert`, `gift-card-sweep`) run ahead of the plugin's
job in every fifth tick, which would lengthen o there and let a step through,
and is offered as the likely reason only.

## 4. Rows left for the next pass waited for two (D274)

A pass that ran out of budget reported "N left for the next pass" (`summarize`,
asserted by `job_report_test.go`), while the rows kept the claim's 90 s lease,
past the next pass (+60 s) to the one after (+120 s). Read from the code; it is
the same lease the claim writes, and `TestAPassThatRanOutOfBudgetHandsItsRowsBack`
measures the fix.

## 5. Two claims leased the same rows (D275)

Reproduced against the shipped `claimSQL` in two ways.

Two psql sessions on the plugin's schema, three rows due. Session B opens a
cursor over the claim's locking `SELECT` (the `due` CTE and the `allowed`
scan, `FOR UPDATE SKIP LOCKED`), which takes its snapshot; session A runs the
whole claim statement and commits; B fetches.

```
== A: the full claim statement, autocommit
  id   |        next_attempt_at
-------+-------------------------------
 whd_1 | 2026-10-10 19:56:33.884126+00
 whd_2 | 2026-10-10 19:56:33.884126+00
 whd_3 | 2026-10-10 19:56:33.884126+00
UPDATE 3
== B: FETCH ALL FROM c;
  id   |        next_attempt_at
-------+-------------------------------
 whd_1 | 2026-10-10 19:56:33.884126+00
 whd_2 | 2026-10-10 19:56:33.884126+00
 whd_3 | 2026-10-10 19:56:33.884126+00
```

B locked and returned the three rows A had leased, with A's lease on them: a
row updated by a committed transaction is not locked, so SKIP LOCKED does not
skip it; READ COMMITTED locks its newest version and rechecks only the scan's
own condition, the id, never the due condition the snapshot decided.

The whole statement from two connections at once, 200 rounds, the second
started 0–1.75 ms after the first, `LIMIT 100`, every row due again between
rounds:

| rows due | rounds that leased a row twice | rows leased twice |
|---|---|---|
| 300 | 0 of 200 | 0 |
| 3 000 | 8 of 200 | 797 |
| 20 000 | 8 of 200 | 800 |

A round that hit leased the same hundred rows to both, so both would have sent
them. The window is the time between a claim's snapshot and its first lock,
which grows with the due set. Before this change only overlapping passes of
the job claimed at once, which its election makes rare; the worker makes it
ordinary.

Through the new `claimDue` (the lock, then the statement), the same 200
rounds over 3 000 rows: 0 rounds leased a row twice, every claim returned 100
distinct rows, in 10.3 s.

`lock_timeout` bounds an advisory lock's wait: with one session holding
`pg_advisory_lock(9 << 32)`, `BEGIN; SET LOCAL lock_timeout = '1s'; SELECT
pg_advisory_xact_lock(9 << 32)` failed with `canceling statement due to lock
timeout` after 1.04 s. `TestAClaimStuckBehindAnotherGivesUpInFiveSeconds`
holds the five-second form to SQLSTATE 55P03.

## 6. After the change

The same server built from this change, the same receiver answering 500 after
one second, the same `product.created`:

| | at | after | documented |
|---|---|---|---|
| queued | 20:17:19.509 | | |
| attempt 1 | 20:17:19.518 | 9 ms after queueing | at once |
| failure 1 written | 20:17:20.529 | | |
| attempt 2 | 20:18:20.551 | 60.02 s after failure 1 | 60 s |
| failure 2 written | 20:18:21.567 | | |
| attempt 3 | 20:20:21.584 | 120.02 s after failure 2 | 120 s |
| failure 3 written | 20:20:22.596 | | |
| attempt 4 | 20:24:22.644 | 240.05 s after failure 3 | 240 s |

With the worker idle when the step ends, the step is measured from the
failure, as `markFailedSQL` always wrote it, and the wait beyond it is the
claim and the request's start, about twenty milliseconds. The minute job ran
nine times in the run and `job_run.detail` read "nothing due" every time: the
worker had sent each attempt first.

In the end-to-end ground, `TestARealOrderReachesAReceiverWithoutThePass`
places a real order with the worker running and the minute job never called,
and the delivery arrives within its five-second bound; with the wake taken out
of `onEvent` nothing arrived in 5.15 s.

### What the review of the first form found

The default-path run above was the first form, 15441cd6, with the worker idle
between attempts. An independent review of it found two faults on the default
path and six smaller ones; the first form was amended before it landed.

**A retry due during another delivery's pass left a minute late.** The worker
slept until the next row due after `now()`. A row whose step ended while the
worker was sending another delivery was not due at that pass's claim and was
due by the time the pass ended, so neither saw it, and the worker slept its
minute. On a held clock, a failed delivery whose step ended 0.5 s into another
delivery's one-second attempt was retried 2m0.5s after its failure. The wake is
now measured from the claim's instant, so a row that fell due after the claim
counts, and one already due makes the worker claim again at once: the same
scenario retries 1m0.5s after the failure, the moment the pass it fell due in
ends (`TestARetryDueDuringAnotherPassLeavesWhenThatPassEnds`). Claiming while a
pass is still sending would close the last half second and was not taken: it
is overlapping passes of one worker for a wait bounded by one pass.

**A capped receiver could be leased three times its cap at a minute's end.**
The claim's minute, its lease and what it counted as due came from `now()`,
the transaction's BEGIN, taken before the wait for the claim lock, and the
count compared windows for equality. With a cap of 2, a claim B in 12:01, then
a claim A that began at 12:00:59.995 but got the lock after B, then a claim C
at 12:01:00.5 leased 2, 2 and 2 rows within half a second: A found a window
naming a later minute, took the whole cap and wrote 12:00 back, and C took it
again. The review measured a claim that waited two seconds for the lock
writing a lease measured from 138 µs after its BEGIN. Now the claim reads
`statement_timestamp()` in a statement of its own once it holds the lock
(`now()` there would still be BEGIN), measures what is due, the lease and the
minute from it, and never moves a window back: the same three claims lease 2,
0 and 0, and with a cap of 3 and B taking two, A takes the last one, counted in
B's minute, and C none (`TestAClaimFromAMinuteBeforeDoesNotResetTheCount`). A
claim that waited for the lock now leases a row that fell due during the wait
and measures its lease from after the lock was released
(`TestAClaimsInstantIsReadOnceItHoldsTheLock`).

**Smaller ones, all taken:**

- An outcome was written on the pass's budget, so a 2xx at the budget's last
  instant was not recorded and was sent again after the lease. It is written
  on a context of its own, bounded at five seconds
  (`TestAnAnswerAtTheBudgetsEndIsRecorded`).
- "An attempt in flight at shutdown finishes" held for the worker only; the
  runner cancels the job's run at shutdown and its attempts were cut off. The
  job's attempts now keep the run's deadline and not its cancellation, and the
  runner's Stop waits for them (`TestTheJobsAttemptsInFlightFinishAtShutdown`).
- The cap was described as what is sent in a minute. It caps leases per clock
  minute, and a lease is sent within its pass, so across a minute's boundary a
  receiver can be sent up to twice its cap within sixty seconds; every
  description now says so.
- The ADR's Context carried the marketplace's figures; they are in section 1.
- The mutant "the budget not read before waiting for a slot", reported as
  equivalent, is not: a pass with no budget held its rows until its deadline
  (`TestAPassWithNoBudgetLeftDoesNotWaitForASlot`, the review's test).
- One claim lock for the database. Over 100 000 due rows, half of them a
  receiver capped at 60, one claim held it 100–182 ms over five claims (the
  review measured 103–216 ms); ten claims started at once all finished, the
  last after 1.07 s, and forty all finished, the last after 4.14 s, so beyond
  about fifty processes waking together the last ones give up on the
  five-second `lock_timeout` and try again within a minute. Stated in the
  ADR's Consequences as a cost; not changed.

The amended server, on the default path, with the review's case: receiver 1
answers 500 after one second for draft products, receiver 2 answers 200 after
one second for published ones (filters), product A (draft), and product B
(published) created as A's first step was about to end (UTC):

| | at | |
|---|---|---|
| A queued | 21:47:12.947 | |
| A attempt 1 | 21:47:12.962 | 15 ms after queueing |
| A failure 1 written | 21:47:13.978 | its step ends at 21:48:13.978 |
| B queued, B attempt 1 | 21:48:13.793, 21:48:13.806 | the pass takes B's second |
| A attempt 2 | 21:48:14.827 | 60.85 s after failure 1, as B's pass ended |
| A failure 2 written | 21:48:15.838 | |
| A attempt 3 | 21:50:15.859 | 120.02 s after failure 2 |

The first form would have sent A's second attempt about a minute later.

### What the follow-up review found

A second review, of 66c66e15, confirmed the first round's eight fixed and
found one more fault on the default path and three smaller ones; the commit
was amended again before it landed.

**A hanging receiver's backlog held every other receiver back for minutes.**
The claim took the oldest due rows across all receivers, and a retry keeps its
age, so a receiver whose every request hangs to the ten-second timeout filled
each claim with its own retries. A pass starts at most thirty-two attempts of
ten seconds within its forty-five (eight at a time, at 0, 10, 20 and 30 s), so
the rest of the claim was released and claimed again ahead of everything
else. On a held clock, behind 40, 100 and 200 due rows of such a receiver, a
fresh delivery to a healthy one waited 49 s, 3m39s and 12m29s; on the real
store with the real sender, behind 120 rows it was not sent in five minutes;
the real claim over 150 rows and one fresh delivery leased 100 of the backlog
and not the fresh one.

The claim now takes receivers in turn: `place`, each receiver's rank by age,
already numbered for the cap, orders the LIMIT and the rows the claim returns,
then the attempts made, so a delivery never tried comes first in its turn,
then age. The same three backlogs now delay the fresh delivery 39 s each, the
end of the pass in flight when it was queued
(`TestAHangingReceiversBacklogDoesNotHoldBackAFreshDelivery`); on the real
store, behind 120 hanging rows, it arrived 5.9 ms after the worker started
(`TestAHangingReceiverDoesNotHoldAnotherBack`); the real claim returns the
fresh delivery first and the backlog's oldest second
(`TestReceiversTakeTurnsInAClaim`). The bound left is the one the known
limits state: more receivers whose requests hang, each with an older delivery
never tried, than one pass can attempt. A cap on how many of one receiver's
rows a claim takes was weighed and not taken: with turns it adds nothing when
others are due, and it would slow a receiver that is alone in the queue.

**Smaller ones, all taken:**

- The test of the outcome's own context killed its mutant in 17 runs of 50:
  its answer and the budget's deadline fell on one instant of the held clock,
  and the bubble may run either first. The answer now arrives a millisecond
  past the budget; the mutant dies in every run.
- The mutant "the minute counted from `now()`" was recorded as untestable,
  and the review killed it with a test that waits for a real minute's start
  (a receiver capped at 2 leased 4 within 4 ms). The claim's instant can now
  be named by a test, and `TestAClaimCountsInTheMinuteOfItsInstant` names two
  a fifth of a second apart across a minute's start: each gets the cap.
- The first attempt's timing was stated four ways; the ADR, the CHANGELOG,
  the package godoc and the known limits now say the one thing that is true:
  the first claim after a delivery is queued attempts it, at once when the
  worker is idle or when its pass in flight ends.
- `validateRate`'s 422 still said a cap was what a receiver "takes" in a
  minute; it says leases in a clock minute now.

## 7. The worker against a kick of the existing pass

The simpler mechanism weighed was a non-blocking kick of the job's existing
pass, under the existing claim, from `onEvent` and the redrive, with no new
published name.

| | kick of the existing pass | worker (chosen) |
|---|---|---|
| Published surface | none | `Worker`, `Worker.PluginName`, `Host.RegisterWorker`, `Host.Workers` |
| Who runs the pass | a goroutine started from the bus handler | one loop per serving process, started by `serve` |
| First attempt | at the kick | at the wake |
| A retry at the end of its step | nothing wakes; the job's next pass, a pass late (D273 stays) unless a timer is armed per failure, which outlives every lifecycle | the loop sleeps until the next due row by the database's clock |
| Shutdown | nothing stops or waits for it; the pool closes under in-flight attempts | cancelled first, waited for up to `SHUTDOWN_TIMEOUT` |
| `InProcess`, the e2e ground, `gobit jobs` | every process that consumes events sends, tests included | only `serve` |
| The cap under the existing claim | counted per claim, so each kick could send a receiver its whole cap again | needs the per-minute count either way |
| Concurrent claims under the existing claim | ordinary, and D275 leases rows twice | needs the claim lock either way |

The kick gives the prompt first attempt and nothing else. It does not fix the
ladder, it has no stop, and it runs in every process that consumes events,
which the composition root keeps for `serve` alone ("no PORT or CLOCK in
assemble"). Both mechanisms need the claim lock and the per-minute count once
anything besides the minute job claims. No plugin hook other than a job has a
lifecycle (`module.Module` has none), and a job is the minute clock. So the
worker is the smallest mechanism with the same guarantees; its four names
mirror `Job`'s four.

## 8. Mutants

Each mutant was applied alone to the amended tree, its lane run with
`-count=1` after a green baseline of every lane, and the file restored. Lanes:
the plugin's unit package (`-timeout 180s`), its integration package
(testcontainers), `internal/app`'s unit package and its integration test of
`startJobs`, `core/plugin`, two `internal/arch` gates, and the end-to-end test
by name.

| Mutant | Killed by |
|---|---|
| the wake taken out of `onEvent` | `TestAQueuedDeliveryLeavesBeforeTheClockMoves`, `TestARetryLeavesWhenItsStepEnds`, `TestARetryDueDuringAnotherPassLeavesWhenThatPassEnds`; in e2e `TestARealOrderReachesAReceiverWithoutThePass` |
| the wake case taken out of the worker's `select` | the same three unit tests |
| the worker always sleeping `every` | `TestARetryLeavesWhenItsStepEnds`, `TestARetryDueDuringAnotherPassLeavesWhenThatPassEnds` |
| the worker sleeping the wait plus `every` | the same two |
| the wake measured from the wall clock after the pass, not from the claim | `TestARetryDueDuringAnotherPassLeavesWhenThatPassEnds` |
| the wake time reading rows due after `now()`, not after the claim | `TestTheNextWakeIsTheEarliestStepStillToCome` |
| the wake read after a failed claim | `TestAWorkerOverADownDatabaseDoesNotSpin` (the worker claims in a loop at one instant; the test times out) |
| no claim again after a full claim or a release | `TestABacklogLargerThanAClaimGoesOutWithoutWaiting` |
| the worker's attempts on the worker's context | `TestShutdownLetsTheAttemptsInFlightFinish` |
| the job's attempts on the run's context | `TestTheJobsAttemptsInFlightFinishAtShutdown` |
| the job's attempts without the run's deadline | `TestAPassDoesNotStartWhatItsBudgetCannotFinish`, `TestAPassWithNoBudgetLeftDoesNotWaitForASlot` |
| an outcome written on the pass's budget | `TestAnAnswerAtTheBudgetsEndIsRecorded` |
| the worker not releasing what it did not attempt | `TestShutdownLetsTheAttemptsInFlightFinish`, `TestAWorkerToldToStopDoesNotWaitForASlot` |
| the job's pass not releasing | `TestAPassThatRanOutOfBudgetHandsItsRowsBack` and two unit tests |
| the job's pass on a semaphore of its own | `TestAPassDoesNotStartWhatItsBudgetCannotFinish` (`TestOneProcessKeepsEightInFlight` too, run alone: 16 in flight) |
| the budget not read again once a slot is held | `TestAPassDoesNotStartWhatItsBudgetCannotFinish` |
| the budget not read before waiting for a slot | `TestAPassWithNoBudgetLeftDoesNotWaitForASlot` (the review's; survived the first round) |
| stop not a case of the slot wait | `TestAWorkerToldToStopDoesNotWaitForASlot` |
| the redrive not waking the worker | `TestARedriveWakesTheWorker` |
| the release not comparing the lease | `TestAReleaseLeavesARowAnotherClaimTook` |
| the count never written | `TestACappedReceiverGetsItsCapPerClockMinuteWhoeverClaims`, `TestAClaimWaitsForTheClaimBeforeIt`, `TestAClaimFromAMinuteBeforeDoesNotResetTheCount` |
| the room's comparison inverted | four tests, `TestAReceiverIsSentNoMoreThanItsRate` among them |
| the room comparing windows for equality | `TestAClaimFromAMinuteBeforeDoesNotResetTheCount` |
| the count comparing windows for equality | `TestAClaimFromAMinuteBeforeDoesNotResetTheCount` |
| the window allowed to move back | `TestAClaimFromAMinuteBeforeDoesNotResetTheCount` |
| the count never starting again | `TestACappedReceiverGetsItsCapPerClockMinuteWhoeverClaims` |
| the room ignoring the count | three tests |
| the claim's instant read as `now()` | `TestAClaimsInstantIsReadOnceItHoldsTheLock` |
| what is due measured from `now()` | `TestAClaimsInstantIsReadOnceItHoldsTheLock` (a row due during the lock wait not leased) |
| the lease measured from `now()` | `TestAClaimsInstantIsReadOnceItHoldsTheLock` |
| the claim lock not taken | `TestAClaimWaitsForTheClaimBeforeIt`, `TestAClaimStuckBehindAnotherGivesUpInFiveSeconds`, `TestAClaimsInstantIsReadOnceItHoldsTheLock` |
| no `lock_timeout` | `TestAClaimStuckBehindAnotherGivesUpInFiveSeconds` |
| the lock class 2 | `TestAdvisoryLockClassesAreUnique` |
| the wake time reading dead, delivered or orphaned rows (three mutants) | `TestTheNextWakeIsTheEarliestStepStillToCome` |
| the server's stop not waiting; serve's stop skipping the workers; serve starting none | `TestAWorkerHasReturnedWhenTheServerStopsWaiting`; `TestServeStartsAPluginWorkerAndWaitsForItOnTheWayOut` (two) |
| no admission, no duplicate check, a panic not recovered | `TestAPluginWorkerThatCannotRunStopsTheInstallation` (two), `TestAWorkerThatEndsOrPanicsIsLoggedAndNotStartedAgain` |
| the plugin registering no worker | `TestThePluginRegistersItsSender` |
| the plugin's name not captured; `Host.Workers` not copied | `TestAWorkerIsCollectedAtSetupWithItsPlugin` (two) |
| the opened installation carrying no workers | e2e `TestARealOrderReachesAReceiverWithoutThePass` |

Forty-seven of fifty killed. The three that lived, read before acting:

- The due rows of a receiver with no room left in the claim's numbering: the
  `allowed` scan's `place <= left_now` refuses the same rows; the filter only
  spares numbering a full receiver's backlog. Equivalent.
- `stop` not read again once a slot is held: the attempt it lets start
  finishes on its own context and writes its outcome. The review reached it
  deterministically when the other pass frees a slot just before the stop,
  but its probe's baseline failed once under `-race` load, so it is not a
  gate.
- The minute counted from `now()` instead of the claim's instant lived in
  that round and was called untestable; the follow-up review killed it, and
  the second amendment's round kills it with a named instant (below).

In the first form's round, `TestAClaimWaitsForTheClaimBeforeIt`'s first
fixture let the claim-lock mutant live (both claims wanted the same oldest
rows), and two mutants (`Workers: nil`, `startWorkers(ctx, nil, log)`) did not
compile and were redone so they did.

### The second amendment's round

The fifty mutants above with their anchors moved to the final code, and five
new ones, each alone with `-count=1` after green baselines: 53 of 55 killed.

| New mutant | Killed by |
|---|---|
| the claim oldest first across receivers again | `TestReceiversTakeTurnsInAClaim`, `TestAHangingReceiverDoesNotHoldAnotherBack` |
| the claim's rows returned in no order | `TestReceiversTakeTurnsInAClaim` (survived until its turn-by-turn case analyzed the tables: the analyzed plan returns the rows grouped by receiver; 3 of 3 runs) |
| a delivery never tried not first when the limit cuts a turn | `TestReceiversTakeTurnsInAClaim` (survived until the case with a limit of eight was added) |
| a delivery never tried not first in the order returned | `TestReceiversTakeTurnsInAClaim` |
| a named instant ignored | `TestACappedReceiverGetsItsCapPerClockMinuteWhoeverClaims` and two more |
| the minute counted from `now()` (lived in the earlier rounds) | `TestAClaimCountsInTheMinuteOfItsInstant`, and the two cap tests whose instants are an hour ahead |
| an outcome written on the pass's budget | `TestAnAnswerAtTheBudgetsEndIsRecorded`, now in 50 runs of 50 (17 of 50 before the millisecond) |

Lived: the due CTE's room filter (equivalent, as above) and `stop` not read
again once a slot is held (its attempt finishes on its own context and
writes).

