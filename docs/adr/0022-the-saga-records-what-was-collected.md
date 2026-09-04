# ADR 0022 — The saga records on the order what was actually collected

- **Status:** Accepted
- **Date:** 2026-09-05
- **Phase:** after the roadmap

## Context

`order/service.Service.SetOrderSummaryTotals` had a service method, a repository
method and a generated query. It had NO production caller.

The method is not a sketch — it is one of the more carefully argued pieces of
the order module. Its godoc explains why the write is a MERGE rather than an
overwrite (an at-least-once caller must not be able to shrink a total), why a
shrinking report is ignored rather than rejected (an error would put a
subscriber into an endless retry), why it runs under the order's lock, and why
an over-collection is recorded rather than clipped. It even names who should
call it:

> The side that knows the result of the collection is the complete_cart workflow
> or a subscriber listening to the payment events.

Neither existed. The consequence reached every order in the system: `paid_total`
stayed 0 and `outstanding` stayed the full total, on both `/admin/v1/orders/{id}`
and `/store/v1/orders/{id}`. **An operator could not tell a paid order from an
unpaid one**, which is the first question anyone asks about an order.

The `order/api` package doc had already assigned the owner —
*"SetOrderSummaryTotals are NOT on the surface; both of them are the workflow's"*
— so the wiring was not undecided. It was unfinished.

## Decision

**The checkout saga reads the payment collection after the capture and records
its amounts on the order.**

The write happens in `clearCartStep`, the saga's post-pivot step, under that
step's existing discipline: a failure is logged at ERROR and added to
`CompleteCartResult.Warnings`, and does NOT fail the saga.

`CompleteCartResult` gains `PaymentTotalsRecorded`, alongside `CartCompleted`
and `ReservationsConfirmed`, so the caller can see which of the three
post-pivot facts were written.

## Rejected options

**A. A step of its own.** It would be cleaner to name, and it was the first
design. Three things ruled it out. The saga's step NAME LIST is what recovery
compares against the record, so a new name makes every saga in flight at the
moment of deployment unrecoverable. `CompleteCartResult` is produced by
`clearCartStep`, so a middle step could log its failure but could not put the
warning in the answer. And the work is the same category as what that step
already does: post-pivot bookkeeping that must not fail a paid order.

**B. A subscriber on payment events.** The method's own godoc offers this as the
alternative, and its merge semantics were designed for it. It cannot be built
today: **the payment module publishes no events at all.** Building an event
surface for the payment module is a larger decision than this one and would have
to answer what a payment event carries before it could be subscribed to.

**C. Carrying the amount from the capture step.** The capture step already reads
the collection three steps earlier, so a second read looks wasteful. It is
nevertheless the right call. On the RECOVERY path this step can run when the
capture step did not re-execute, so a carried number would have to survive in
the execution record — teaching `captureOutput` a money field to save one call.
And the capture step's read is a VERIFICATION anchored to the locally known
amount, which deliberately discards the refunded figure; reusing its numbers
would tie what an order says it was paid to a check that answers a different
question.

**D. Recording the amount the saga ASKED to capture.** The plan's amount is an
intention. A provider may capture less than was asked, an over-collection is a
real fact (an exchange-rate difference, a correction on the provider's side),
and a refund may already stand against the same collection. What is written is
the payment module's own figure, which is also what makes the number worth
comparing: an order summary that disagrees with its collection is then a real
divergence rather than two systems telling different stories.

**E. Failing the saga when the write fails.** The money has moved and the order
has been placed. An error here would mark the execution failed and show the
customer a failure for a flow that succeeded — the exact outcome
`internal/workflows/checkout/doc.go` spends a section arguing against.

**F. Not writing it at all, because the figure would then live in two places.**
This was the standing position, asserted by an e2e test whose comment argued
that a "paid total" kept in two places could diverge and that reconciling them
was Phase 7+ work. It was answered rather than dropped.

The duplication was already decided: `order_summaries` exists as a table with
these columns, `SetOrderSummaryTotals` was written to fill them, and the B2B
spending window already READS `refunded_total` from it. What was missing was
never the decision — it was the writer. The summary is a DERIVED report and the
collection stays the source of truth, which is why the number written is the
collection's own. And divergence is not a risk created by having two figures; it
is a condition that becomes DETECTABLE because there are two, which is the same
argument ADR 0020 makes about a session and its provider.

The phase that comment deferred to no longer exists.

## Consequences

**`paid_total` is now right at checkout. `refunded_total` is right only at
checkout.** Refunds are made through the payment module's own admin API
(`RefundPayment`), which has no order-side caller, so a refund made later
updates the payment module and never reaches the order summary.

That has a named victim: the B2B spending window subtracts
`order_summaries.refunded_total` (`order/queries/spending.sql`, `SumCustomerSpend`),
so **a refunded B2B order still does not return the employee's budget.** This
decision does not fix that, and saying so is the point — the remaining half is a
refund path that reports back, and it needs either option B or an admin flow
that writes both sides.

**An order can still be paid and read as unpaid, and it now says so.** If the
write fails, `PaymentTotalsRecorded` is false and the warning names it. That is
the same shape as the hole ADR 0020 closed for payment sessions, one level up,
and it is a candidate for the same treatment: a reconciliation job comparing
order summaries against payment collections. Not built here; the scheduler that
would run it exists (ADR 0019).

**The happy path makes one more call to the payment module.** Measured as a
second `Collection` read in the same saga; see rejected option C for why it is
not folded into the first.

## Reopening

This decision is superseded the day the payment module publishes events. A
subscriber is the better home — it covers the whole lifetime of an order rather
than the moment of checkout, which is exactly what the refund gap needs — and
`SetOrderSummaryTotals` was written for that caller. Nothing about the merge
semantics changes when it arrives; the saga's write simply becomes redundant and
can be removed.

## Related

- ADR 0017 — nothing scheduled acts. This write is not scheduled and not an act
  of recovery; it is the saga recording its own outcome.
- ADR 0019 — the scheduler, which a future summary reconciliation would use.
- ADR 0020 — the same defect shape one level down: a committed fact whose
  downstream record silently did not happen.
- ADR 0021 — the previous finding from the same measured inventory.
