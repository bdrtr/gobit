# What the timeline left out — measured 2026-09-25

The evidence behind [ADR 0170](../adr/0170-an-orders-timeline-tells-every-movement.md).

The feature list's C8 row asks for an order, a cart or a price as it stood at
a moment T, with a dispute file as the first consumer. ADR 0167 already did it
for prices. Three read-only surveys of the tree at `16fdc77` asked what an
order and a cart keep of their past.

## 1. No event log to rebuild from

- `event_outbox` keeps published rows and nothing prunes them. Only a few
  topics go through it (`order.placed`, `order.line_canceled`,
  `payment.captured`, `payment.refunded`, `cart.created`, `cart.completed`,
  `fulfillment.canceled`), and their payloads are ids and a timestamp. Payment
  events carry no amount, on purpose.
- `audit_log` records the admin request (method, path, status, actor) and not
  the change.
- The Redis streams are trimmed at about 10,000 entries per stream.
- A cart is overwritten in place: its quantities, contact, totals and
  addresses, and its codes, which are deleted. The only past copy is the
  checkout's snapshot in `workflow_executions.input`.

So "the cart at T" has no source at all, and "the order at T" has only the
order's own rows.

## 2. What the order's own rows keep

Most order facts are written once and dated: lines, tax breakdowns, line
cancellations, credits, returned and replaced items, and every after-sales
record's opening. Status changes are one-way, each stamped by a column that is
never cleared: `completed_at`, `canceled_at`, `archived_at`, and the return,
claim, exchange and replacement moments. Three things are overwritten:

| State | Where | What is lost |
|---|---|---|
| the money the order records | `order_summaries.paid_total`, `refunded_total` | every value but the last |
| contact and addresses | `orders.email`, ten `order_addresses` columns | everything, at erasure |
| claim evidence | `order_claim_evidence` | the row, on detach |

The payment module keeps what the order summary does not. Every capture is a
`payments` row with its `captured_at` and amount, and every refund is a
`refunds` row with its `created_at` and amount.

## 3. What the timeline showed of that

`service/timeline.go` said "everything that happened to an order", and it
showed:

| Fact | Row | On the timeline before |
|---|---|---|
| each capture | `payments` | the first one only, carrying the lifetime captured total |
| each refund | `refunds` | the last one only, carrying the lifetime refunded total |
| a line cancellation | `order_line_cancellations` | no |
| a credit | `order_credit_lines` | no |
| a replacement opened, dispatched, canceled | `order_replacements` | no |
| an exchange funded | `order_exchanges.funded_at` | no |
| the erasure | `orders.personal_data_erased_at` | no |

The money row is **D126**. A refund of 1,000 and then one of 500 gave one
entry dated at the second with the amount 1,500. The first refund had no
entry, and no entry was the amount that moved at its moment. A collection
captured across more than one session, which the admin surface can do (ADR
0165 leaves splitting to it), is several captures, and the timeline gave them
as one.

The admin endpoint's published description was stale as well (**D127**). It
said an entry with a null moment is "an exchange that was completed or
canceled, whose columns exist and which nothing writes". The timeline's own
godoc says both endings have been dated since ADR 0117 and that nothing
produces an undated entry.

## 4. What changed

- The payment collection offers `movements`: every capture and refund, oldest
  first, with its own id, the payment it went through, its kind, its amount and
  its moment. It is one `UNION ALL` query, run only when the field is asked
  for, like the two moments.
- The timeline asks for `movements` instead of the totals and the two
  moments, and adds entries from the order module's own tables. Replacements
  are found through the order's claims and exchanges, because a replacement
  row names its source and not its order.

| Kind | Clock | Amount | Quantity | Customer sees |
|---|---|---|---|---|
| `payment.captured` (each) | application | what it took | | no |
| `payment.refunded` (each) | database | what it gave back | | no |
| `order.line_canceled` | database | | units | yes |
| `order.credited` | database | the credit | | no |
| `replacement.opened` / `dispatched` / `canceled` | database | | | yes |
| `exchange.funded` | database | the difference | | no |
| `order.personal_data_erased` | database | | | no |

## 5. The mutations

All were run with the mutation script's baseline check. The script now also
reports a mutation that does not compile as invalid instead of counting it
as killed.

| # | Mutation | Red test |
|---|---|---|
| T1 | only the first movement becomes an entry | `TestEveryMovementIsItsOwnEntry` |
| T2 | an entry carries a running figure | same |
| T3 | a refund is put on the application clock | same |
| T4 | an unreadable movement is skipped | `TestAMovementTheTimelineCannotReadIsAnError` |
| T5 | the customer is shown the credit | `TestTheCustomerSeesTheNewGoodsAndNotTheNewMoney` |
| T6 | the credits are not composed | e2e `TestTheTimelineTellsEveryMovement`. The first attempt did not compile and was reported as killed, which is why the script now checks. |
| T7 | the exchange's funding is dropped | `TestAFundedExchangeReportsItsFunding` |
| T8 | the erasure is dropped | `TestTheErasureIsDated` |
| T9 | the movements are read even when not asked for | `TestTheMovementsAreReadOnlyWhenAskedFor` |
| T10 | the query reports a refund as a capture | e2e `TestTheTimelineTellsEveryMovement` |
| T11 | the replacement query loses its exchange branch | `TestAnOrdersReplacementsAreFoundThroughItsClaimsAndExchanges` |
| T12 | the admin view drops `quantity` | `TestALineCancellationCarriesItsQuantityOnBothViews` |
| T13 | the store view drops `quantity` | same |
| T14 | the repository stops reading the erasure stamp | **survived** until `TestTheOrderReadCarriesTheMomentOfItsErasure` was written, because the timeline tests build the model themselves |

## 6. What the next slice of C8 has to face

- The `detail` of the placed, return-opened, claim-opened and exchange-opened
  entries is the record's status today, not then.
- `order_summaries` keeps no history. The payment movements do, so money at T
  can be summed from them rather than read from the summary.
- A detached claim evidence leaves no trace.
- After erasure, the contact and addresses at an earlier T are gone. The
  timeline now says when they went.
- The cart at T has no source. A dispute file also needs what no module
  keeps: provider references beyond the session's external id, carrier
  checkpoints, what the customer was sent, and the buyer's IP.
