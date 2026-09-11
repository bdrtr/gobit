package checkout

// This file holds the saga's LAST step: the post-pivot bookkeeping that records
// on the order what was collected, closes the cart and finalizes the
// reservations.
//
// The step's quartet (Name/Restore/Invoke/Compensate) stands here together with
// recordPaymentTotals, the only helper it has. What all five steps share stays
// in steps.go.

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/internal/core/workflow"
)

// clearCartStep is the saga's post-pivot bookkeeping: it records on the order
// what was collected, closes the cart and finalizes the reservations.
//
// # Why the payment totals are recorded HERE and not in their own step
//
// Three reasons, and the third is the one that decides it.
//
// The work belongs to this category: it runs AFTER the pivot, so its failure
// must not fail the saga, and this step is where that discipline already lives.
//
// A step of its own would change the saga's step NAME LIST, and recovery
// compares those names against the record — so every saga in flight at the
// moment of deployment would become unrecoverable (see
// [Workflows.sagaSteps]).
//
// And the warning has to reach the caller. [CompleteCartResult] is produced by
// this step and carries the Warnings field; a middle step could log its failure
// but could not put it in the answer.
type clearCartStep struct {
	w    *Workflows
	plan *checkoutPlan
}

// Name returns the step's name.
func (s *clearCartStep) Name() string { return StepClearCart }

// Restore does NOTHING, and that is deliberate.
//
// The step does not write to the shared map and its compensation does not read
// from it either (the compensation is empty anyway: closing a cart is not
// undone, and if the order is canceled the cart is not reopened). It implements
// the interface all the same so that recovery DOES NOT STOP at this step: a step
// that does not implement [workflow.Recoverable] turns the whole chain into
// manual intervention, and the situation here is that there is NOTHING to
// restore — not that it cannot be restored.
func (s *clearCartStep) Restore(_ *workflow.StepContext, _ json.RawMessage) error { return nil }

// Invoke stamps the cart completed, confirms the reservations and produces the
// flow's result.
//
// # Module faults are NOT returned as errors
//
// The step runs AFTER the pivot (the capture). Returning an error would write
// the execution failed and would show the customer an error for a flow whose
// money has been taken and whose order has been placed; on top of that the
// compensation chain would run for nothing (the pivot guard skips it anyway, see
// [Workflows.skipAfterCapture]). Instead the faults are logged as ERROR and
// written into the [CompleteCartResult.Warnings] field; the order IS VALID, but
// a human must look at it.
//
// The only error path is the corruption of the data carried between steps: that
// is a programming error, not an external fault, and swallowing it silently
// would mean the result being returned with missing fields.
//
// The remaining inconsistency is bounded and repairable: a cart that could not
// be stamped looks open (but a second execution cannot be started for the same
// cart because of the idempotency key), and a reservation that was not confirmed
// stays "active" — the stock is still reserved, it is only not counted as
// deducted. None of these is as expensive as a capture that was not refunded.
//
// The step's output is the workflow's output: a second call made with the same
// key reads this body from the execution record and returns it.
func (s *clearCartStep) Invoke(ctx context.Context, sc *workflow.StepContext) (any, error) {
	result := CompleteCartResult{
		CartID:       s.plan.CartID,
		CurrencyCode: s.plan.CurrencyCode,
		Amount:       s.plan.Amount,
	}

	var err error
	if result.OrderID, err = sharedText(sc, sharedOrderID); err != nil {
		return nil, err
	}
	if result.PaymentCollectionID, err = sharedText(sc, sharedCollectionID); err != nil {
		return nil, err
	}
	if result.PaymentSessionID, err = sharedText(sc, sharedSessionID); err != nil {
		return nil, err
	}
	if result.PaymentID, err = sharedText(sc, sharedPaymentID); err != nil {
		return nil, err
	}
	refs, err := sharedRefs(sc)
	if err != nil {
		return nil, err
	}

	// The money is recorded FIRST, before the cart and the reservations. All
	// three are best-effort here, so the order among them is a priority
	// ordering: of the three facts, the one whose absence is worst is what was
	// paid. An order that reads "nothing collected" is one an operator will
	// treat as unpaid.
	if totalsErr := s.recordPaymentTotals(ctx, result.OrderID, result.PaymentCollectionID); totalsErr != nil {
		s.w.log.ErrorContext(ctx,
			"the collected amount could not be recorded on the order; the order is VALID and PAID "+
				"but reads as unpaid, manual repair is required",
			"cart_id", s.plan.CartID, "order_id", result.OrderID,
			"payment_collection_id", result.PaymentCollectionID, "error", totalsErr)
		result.Warnings = append(result.Warnings,
			"the collected amount could not be recorded on the order: "+totalsErr.Error())
	} else {
		result.PaymentTotalsRecorded = true
	}

	if markErr := s.w.carts.MarkCompleted(ctx, s.plan.CartID); markErr != nil {
		s.w.log.ErrorContext(ctx, "the cart could not be stamped completed; the order is VALID, manual repair is required",
			"cart_id", s.plan.CartID, "order_id", result.OrderID, "error", markErr)
		result.Warnings = append(result.Warnings, "the cart could not be stamped completed: "+markErr.Error())
	} else {
		result.CartCompleted = true
	}

	result.ReservationIDs = make([]string, 0, len(refs))
	confirmed := true
	for i := range refs {
		result.ReservationIDs = append(result.ReservationIDs, refs[i].ReservationID)

		if confirmErr := s.w.inventory.ConfirmReservation(
			ctx, refs[i].ReservationID, result.OrderID); confirmErr != nil {
			confirmed = false
			s.w.log.ErrorContext(ctx, "the reservation could not be confirmed; the order is VALID, manual repair is required",
				"cart_id", s.plan.CartID, "order_id", result.OrderID,
				"reservation_id", refs[i].ReservationID, "error", confirmErr)
			result.Warnings = append(result.Warnings,
				"the reservation could not be confirmed ("+refs[i].ReservationID+"): "+confirmErr.Error())
		}
	}
	result.ReservationsConfirmed = confirmed

	return result, nil
}

// recordPaymentTotals reads the collection's amounts and writes them onto the
// order.
//
// # Why the collection is read AGAIN
//
// The capture step reads it too, three steps earlier, and this is deliberately
// a second read rather than a carried value.
//
// The reason is RECOVERY. This step can run in a recovery where the capture
// step did not re-execute — its work is already recorded — so a carried number
// would have to survive in the execution record, which means teaching
// [captureOutput] a money field for the sake of saving one call.
//
// The second reason is that the capture step's read is a VERIFICATION, not a
// measurement: it compares against the amount known LOCALLY on purpose, and it
// discards the refunded amount because verification does not need it. Reusing
// its numbers here would tie what an order says it was paid to a check that
// exists to answer a different question.
//
// What is written is therefore the payment module's own figure, which is also
// what makes the pairing meaningful: an order summary that disagrees with its
// collection is a real divergence rather than two systems telling different
// stories about the same event.
func (s *clearCartStep) recordPaymentTotals(ctx context.Context, orderID, collectionID string) error {
	_, _, _, captured, refunded, err := s.w.payments.Collection(ctx, collectionID)
	if err != nil {
		return err
	}

	return s.w.orders.SetOrderSummaryTotals(ctx, orderID, captured, refunded)
}

// Compensate does nothing.
//
// There are two reasons and either one is enough: the step is the saga's LAST,
// meaning there is no step after it that could blow up; and the work it does has
// no way back either — ConfirmReservation actually deducts the reserved stock,
// and stock cannot be given back without "creating" it (see ReleaseReservation
// in the inventory module: a confirmed reservation returns errors.Conflict).
//
// The ONLY case in which the engine could call this compensation is the step
// itself being attempted more than once (best-effort compensation); since steps
// are not retried, that path is closed too. Returning nil is therefore correct,
// not a silent loss.
func (s *clearCartStep) Compensate(_ context.Context, _ *workflow.StepContext) error {
	return nil
}
