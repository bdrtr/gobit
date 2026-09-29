# A box that never left — measured 2026-09-29

The evidence behind [ADR 0239](../adr/0239-a-canceled-parcel-recalls-its-replacement.md)
and D162.

## 1. What there was

| Part | State before |
|---|---|
| a parcel's cancellation | `POST /admin/v1/fulfillments/{id}/cancel`, allowed while pending or shipped; publishes `fulfillment.canceled` |
| the cancellation flow's handler | reads what the parcel holds per order line; a replacement's parcel holds no item, so `len(held) == 0` returns nil |
| the replacement | stays `dispatched`, naming the canceled parcel |
| its promises | confirmed; the units are out of the count as `replacement` movements |
| its claim or exchange | stays `completed` |
| dispatching again | answered `already_sent`; a new parcel under `replacement-<id>` would resolve to the canceled one and be refused (ADR 0088) |
| withdrawing it | refused: a dispatched replacement is taken back by a return |

The units were neither on the shelf nor in a box, the shape D75 found for a
sale's written-off lines.

## 2. The shape

| Piece | Now |
|---|---|
| inventory `RecallReplacementUnits` | under the reservation's lock: a replacement's, confirmed; asks the ledger for a cancellation naming the promise; if none, adds the promise's quantity at its location as a `cancellation` with the promise as reference and no line |
| why the ledger is asked | the unique index on a cancellation's reference was dropped by inventory migration 000007 (ADR 0142); the lock serializes two recalls of one promise, and the second finds the first's row |
| order `ReplacementOfParcel` | `order_replacements` by `fulfillment_id` (partial index, migration 000036); none is "" |
| order `RecallReplacement` | only when dispatched in THIS parcel: status `requested`, moment and parcel cleared in one statement, `recalls + 1`, the lines' and parts' promises cleared, the source reopened unless another of its replacements is dispatched |
| reopening | a claim to `requested`; an exchange to `funded` when `funded_at` is set, else `requested`; `completed_at` cleared |
| the flow | subscribes to `fulfillment.canceled`; reads the replacement the parcel carried; puts back every promise, then recalls the record |
| the next key | `replacement-<id>-<recalls>` once `recalls > 0` |

The inventory's test double held a line on every cancellation, both ways; the
schema's CHECK holds it one way (`line_item_id IS NULL OR reason =
'cancellation'`), and a recall's movement names no line on purpose: a line's
cancellations are summed as its write-offs' target (ADR 0142), and the
replacement's units are not the line's written-off ones.

## 3. Delivery

The bus logs a handler's error and does not retry it (`core/eventbus`, "Error
and retry policy"). `fulfillment.canceled` is also written to the outbox, and
the relay publishes a row the direct publish did not mark, so the handler runs
twice in normal operation (measurements/0164). Both steps of a recall repeat
safely: the second run finds the units back and the record either still
dispatched in the parcel, and finishes it, or no longer, and does nothing.

## 4. D162 — the flow's logs

| Flow | Built by the wiring with | A nil logger becomes |
|---|---|---|
| cart, checkout | `slog.Default().With("workflow", …)` | — |
| fulfilling, invoicing | nothing | `slog.Default()` |
| returns | nothing | `slog.New(slog.DiscardHandler)` |

Ten `ErrorContext` lines in the returns flow were written to the discard
handler in a running installation, among them the partial refund's "a human has
to finish it".

## 5. The tests

| Test | Holds |
|---|---|
| `TestACanceledParcelsUnitsGoBackOnce` (inventory service) | four units back as a cancellation naming the promise; the second call writes nothing |
| `TestOnlyAConfirmedReplacementPromiseIsRecalled` | a held promise and a sale's are refused |
| `TestARecalledReplacementGoesBackOnceAgainstTheDatabase` (inventory, Postgres) | one row, the count grew once |
| `TestACanceledParcelSendsItsReplacementBackToWaiting` (order service) | status, parcel, moment, recalls, every line's and part's promise, the claim reopened |
| `TestARecallForAParcelTheReplacementLeftChangesNothing` | another parcel changes nothing; a repeat counts once |
| `TestAClaimAnotherReplacementSettledStaysSettled` | a sibling that left keeps the claim settled |
| `TestAnExchangeGoesBackToWhereItStood` | funded and owing-nothing exchanges |
| `TestARecallPassesTheSchemasPairings` (order, Postgres) | the row after the recall; sent and recalled again, `recalls` is 2 |
| `TestARecalledFundedExchangeKeepsItsCollection` | `funded` with its collection, on the real CHECKs |
| `TestACanceledParcelRecallsItsReplacement` (returns flow) | every promise, the box's parts included, then the record |
| `TestAParcelWithNoReplacementIsLeftAlone`, `TestAParcelTheReplacementLeftIsLeftAlone` | a sale's parcel and a late event do nothing |
| `TestTheUnitsGoBackBeforeTheRecordIsRecalled` | a failed put-back leaves the record dispatched |
| `TestARecallIsFinishedByTheNextDelivery` | units already back, record recalled |
| `TestTheNextParcelNamesTheRecall` | the key names the count |
| `TestTheFlowLogsWhereTheApplicationLogs` | D162 |
| `TestTheWiredFlowHearsACanceledParcel` | the flow its wiring builds is subscribed, and the handler recalls |
| `TestACanceledReplacementParcelPutsItsGoodsBack` (end to end) | below |

The end-to-end scenario: two units sold (10 → 8), one replaced (8 → 7). The
replacement's parcel is canceled over the admin API; the shelf goes back to 8,
the replacement reads `requested` with `recalls` 1, the claim reads
`requested`. Dispatching again opens a different parcel, the shelf goes to 7 and
the claim to `completed`.

## 6. Mutations

| # | Mutation | Killed by |
|---|---|---|
| I1 | a sale's promise recalled | `TestOnlyAConfirmedReplacementPromiseIsRecalled` |
| I2 | a held promise recalled | the same |
| I3 | the ledger not asked, a second recall adds again | `TestACanceledParcelsUnitsGoBackOnce` |
| I4 | the units not added | the same |
| O1 | a parcel the replacement left recalls it | `TestARecallForAParcelTheReplacementLeftChangesNothing` |
| O2 | the promises kept | `TestACanceledParcelSendsItsReplacementBackToWaiting` |
| O3 | the source left settled | the same, and `TestAnExchangeGoesBackToWhereItStood` |
| O4 | a source another replacement settles reopened | `TestAClaimAnotherReplacementSettledStaysSettled` |
| O5 | a funded exchange reopened as requested (the SQL) | `TestARecalledFundedExchangeKeepsItsCollection` |
| O6 | the recall not counted (the SQL) | `TestARecallPassesTheSchemasPairings` |
| F1 | the record recalled before the units | the flow's recall tests |
| F2 | only a line's own promise recalled | `TestACanceledParcelRecallsItsReplacement` |
| F3 | a parcel the replacement left recalls it (the flow) | `TestAParcelTheReplacementLeftIsLeftAlone` |
| F4 | the next parcel reuses the key | `TestTheNextParcelNamesTheRecall` |
| F5 | the flow not subscribed | first only the end-to-end test; now `TestTheWiredFlowHearsACanceledParcel` |
| F6 | the logs discarded again | `TestTheFlowLogsWhereTheApplicationLogs` |
| F7 | units already back counted as recalled | `TestARecallIsFinishedByTheNextDelivery` |
| G1 | 0090's row back to `current` | `TestTheADRIndexNamesEveryAmendment` |

Eighteen mutants, all killed; F5 survived the flow's package on the first run
and died only end to end, so the wiring test was added and F5 run again.
