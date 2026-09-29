# The mail nobody could send — measured 2026-09-29

The evidence behind [ADR 0243](../adr/0243-a-failed-confirmation-is-sent-again-by-an-operator.md)
and D166.

## 1. What there was

| Part | State |
|---|---|
| `Notify` | claims `(template, reference)`, sends once, writes `sent`, `failed` or `skipped` |
| its godoc | "Resending has to be the DELIBERATE decision of a human being looking at the log" |
| the API | one route, `GET /admin/v1/notifications`; a route test pinned it read-only, now `TestTheOnlyWriteIsAResendOfARecord` |
| the scope | `notification:read` only: "a write scope IS NOT DEFINED: there is no endpoint it could be given to" |
| a claimed record met again | skipped, logged "the notification has already been sent", whatever its status |
| since ADR 0240 | the bus calls a failing `OrderPlaced` twice more; each finds the failed record and logs that line |

The templates the log holds: `order.placed`, sent by the module's own handler
from the order record; the invitation, the sold gift card's code and the stock
alert, sent through `notification.interop` by `auth`, `giftcardsale` and
`stockalert`, which hold their content. The log holds no recipient and no
message body.

## 2. The shape

| Piece | Now |
|---|---|
| `ReopenFailedNotificationDelivery` | `status = 'pending'`, `error = ''`, the current provider, `WHERE status = 'failed'`; no row otherwise |
| `ResendDelivery` | refuses another template or status (409 `notification_not_resendable`), rebuilds the confirmation with `orderConfirmation`, the event's builder, reopens, and sends through `deliver`, the half of `Notify` after the claim |
| the endpoint | `POST /admin/v1/notifications/{id}/resend`, `notification:write`, no body; the answer is the record after the attempt |
| the skip log | "the notification was attempted before; skipped" |
| the route test | `TestTheOnlyWriteIsAResendOfARecord`: the routes are the listing and the resend, which takes no body and so chooses no key |

## 3. The tests

| Test | Holds |
|---|---|
| `TestAFailedConfirmationIsSentAgainOnAnOperatorsWord` | the bus's second call skipped, the provider reached once; the resend sends, the record says sent, the address is read again |
| `TestAResendThatFailsAgainSaysSo` | a second failure is written and returned |
| `TestOnlyAFailedConfirmationIsSentAgain` | a sent record and an invitation are refused, and the provider is not reached |
| `TestAResendThatLosesTheRaceSendsNothing` | a record failed when read and no longer failed when reopened reaches no provider |
| `TestTwoResendsOfOneFailureReopenItOnce` (Postgres) | the first reopen names the provider, the second finds nothing to reopen |
| `TestAResendAnswersWithTheRecord`, `TestAResendNeedsTheWriteScope`, `TestARefusedResendIsAConflict` (API) | the path's id, `notification:write` against `notification:read`, the 409 |

## 4. Mutations

| # | Mutation | Killed by |
|---|---|---|
| N1 | any status reopened (the SQL) | `TestTwoResendsOfOneFailureReopenItOnce` |
| N2 | a sent record not refused before the order is read | `TestOnlyAFailedConfirmationIsSentAgain`, its message |
| N3 | any template resent | the same, the invitation |
| N4 | the reopened record not delivered | the three resend tests |
| N5 | the endpoint under the read scope | `TestAResendNeedsTheWriteScope` |
| N6 | a lost reopen ignored | `TestAResendThatLosesTheRaceSendsNothing` |

Six mutants, all killed. N6 survived the first run: the reopen guard was held
by the SQL test, and nothing held that a lost reopen stops the send, which is
what keeps two operators pressing at once from sending two e-mails; the race
test was added and N6 run again.
