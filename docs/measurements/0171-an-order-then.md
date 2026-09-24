# An order then — measured 2026-09-25

The evidence behind [ADR 0171](../adr/0171-an-order-can-be-read-as-it-stood.md).

[Measurement 0170](0170-what-the-timeline-left-out.md) found which of the
order's facts are dated rows and which are overwritten. This record is the
reading built on the dated ones.

## 1. Where each answer comes from

| Field | Derived from | At the moment |
|---|---|---|
| `status` | `orders.completed_at`, `canceled_at`, `archived_at` | the latest one at or before it, else pending; null for an undated archive after completion |
| `money.total` | `orders.total` | never changes |
| `money.credited` | `order_credit_lines.created_at`, `amount` | the credits made by then |
| `money.captured`, `refunded` | the payment collection's `movements` (ADR 0170) | the movements up to it |
| `money.outstanding` | `OrderSummary.Outstanding` over the three sums | the live order's formula |
| `lines[].canceled` | `order_line_cancellations.created_at`, `quantity` | the units canceled by then |
| `returns`, `claims`, `exchanges`, `replacements` | each record's `created_at` and transition stamps | records created by then, in the status of their latest stamp |
| `shipments` | each parcel's `created_at` and four transition stamps, through the `order_fulfillment` link | parcels created by then, likewise |
| `contact` | `orders.personal_data_erased_at` | held, erased, or erased since |

`order_summaries` is not read. It is overwritten, and between the two refunds
below it would have said the second one had already happened.

## 2. The cross-check

A reading at the present is not a special case, and so it can be checked
against what is recorded now:

- **Unit** (`TestAStatusReadAfterEveryStampIsTheRecordedStatus`): every state
  each record type can reach, read after its last stamp, gives the recorded
  status. That is 4 order states, 3 return, 3 claim, 6 exchange (including
  funded then completed, and funded then withdrawn), 3 replacement and 6 parcel
  (including delivered with no shipping stamp).
- **End to end** (`TestAnOrderReadNowIsTheLiveOrder`): one real order is
  captured, refunded twice, has one unit canceled, is credited, and is opened
  and shipped. Read now, it agrees with the order's status, with the collection's
  captured and refunded amounts, with the live outstanding, and with the parcel's
  status. Read between the two refunds, it shows the first refund and not the
  second, and no credit, cancellation or parcel yet.

## 3. The clocks

`created_at` of a parcel is the database's clock and its transitions are the
fulfillment process's clock. The first draft took a transition only if it was
not before the record's creation. A shipping stamp a second behind the creation
(`TestAClockSkewDoesNotHideATransition`) was then ignored, and the parcel read
as pending. The rule compares the stamps with each other, and the latest
reached one decides.

## 4. The mutations

All were run with the baseline check and the build check.

| # | Mutation | Red test |
|---|---|---|
| A1 | a cancellation stamp ignored | `TestAStatusReadAfterEveryStampIsTheRecordedStatus` |
| A2 | a movement after the moment counted | `TestTheMoneyIsWhatHadMovedByTheMoment` |
| A3 | the paid total pre-netted of refunds, and the refund term zeroed | **survives — equivalent**: `(c − r) − 0` is `c − r` |
| A3′ | the refunds left out of the formula | `TestTheMoneyIsWhatHadMovedByTheMoment` |
| A4 | a later cancellation counted | `TestARecordThatDidNotExistYetIsLeftOut` |
| A5 | "erased since" reported as "erased" | `TestTheContactIsHeldUntilItsErasure` |
| A6 | a record created after the moment listed | `TestARecordThatDidNotExistYetIsLeftOut` |
| A7 | a stamp compared against the creation (the first draft) | `TestAClockSkewDoesNotHideATransition` |
| A8 | the exchange's funding stamp dropped | the cross-check, after-sales |
| A9 | a parcel's delivery stamp dropped | the cross-check, shipments |
| A10 | a missing `at` taken as now | `TestAMomentIsRequiredAndNotAssumed` |
| A11 | a future moment accepted | `TestAnOrderIsReadOnlyAtAMomentItExisted` |
| A12 | the undated archive guessed | `TestAnUndatedArchiveIsNotGuessed` |
| A13 | the movements not read | e2e `TestAnOrderReadNowIsTheLiveOrder` |
| A14 | the moment passed to the service in its own zone | `TestTheReadingIsPublishedAsTheServiceDerivedIt` |

## 5. Still out of reach

- The contact and addresses before an erasure.
- A claim evidence that was detached.
- Whether an order archived before migration 000007 was completed or archived
  at a moment after its completion.
- A cart at any moment, and what a dispute needs beyond the order (measurement
  0170, §6).
