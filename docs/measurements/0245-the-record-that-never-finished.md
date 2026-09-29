# The record that never finished — measured 2026-09-30

The evidence behind [ADR 0245](../adr/0245-a-delivery-a-dead-attempt-left-pending-is-sent-again.md)
and D168.

## 1. What there was

| Part | State |
|---|---|
| an attempt | claims the record `pending`, calls the provider under `sendTimeout` (15 s), writes the outcome under another 15 s with cancellation stripped |
| an outcome that cannot be written | logged at ERROR, "the record stayed 'pending'" |
| a process that dies between the call and the write | the record stays pending |
| `models.DeliveryPending` | "has to be examined by hand" |
| ADR 0243's resend | failed only; pending answered 409 |

## 2. The shape

| Piece | Now |
|---|---|
| `staleAttempt` | `2 * sendTimeout`: the call and the write are each bounded by it |
| `ReopenForResend` (was `ReopenFailedDelivery`) | `status = 'failed' OR (status = 'pending' AND updated_at < now() - stale)`; the age is the database's, which stamped `updated_at` |
| `resendable` | the same rule on the process clock, read first so the refusal says why before the order is read; the reopen decides |

## 3. The tests

| Test | Holds |
|---|---|
| `TestAnAttemptThatDiedIsReopenedAndOneInFlightIsNot` (Postgres) | a record claimed a moment ago is not reopened; aged two minutes it is, and the reopen stamps a new moment |
| `TestAConfirmationADeadAttemptLeftPendingIsSentAgain` | an outcome that could not be written leaves the record pending; aged an hour it is sent, the provider reached twice |
| `TestAnAttemptStillInFlightIsNotRaced` | a fresh pending record answers 409, saying why, and the provider is reached once |
| `TestAnAttemptTwentySecondsOldMayStillBeSending` | twenty seconds is inside the bound: refused |

## 4. Mutations

| # | Mutation | Killed by |
|---|---|---|
| P1 | no stale pending record reopened (the SQL) | `TestAnAttemptThatDiedIsReopenedAndOneInFlightIsNot` |
| P2 | a pending record of any age reopened (the SQL) | the same, and `TestTwoResendsOfOneFailureReopenItOnce` |
| P3 | no pending record resendable | `TestAConfirmationADeadAttemptLeftPendingIsSentAgain` |
| P4 | every pending record resendable | `TestAnAttemptStillInFlightIsNotRaced`, its message |
| P5 | one send timeout as the bound | `TestAnAttemptTwentySecondsOldMayStillBeSending` |
| G1 | 0243's row back to `current` | `TestTheADRIndexNamesEveryAmendment` |

Six mutants, all killed. P4 and P5 had no test before the run: the store's
own refusal gave the same code, so the in-flight test was given the refusal's
message, and the bound was given a record inside it.
