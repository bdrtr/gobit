package checkout

// This file holds the saga's FOURTH step, its PIVOT: capturing the amount that
// was held.
//
// The step's quartet (Name/Restore/Invoke/Compensate), the BlocksRecovery
// marker and the private helpers only it calls — marking a dangling side
// effect, and settling whether the money really went after a failed capture —
// stand together here. What all five steps share stays in steps.go.

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// capturePaymentStep captures the amount that was held.
type capturePaymentStep struct {
	w    *Workflows
	plan *checkoutPlan
}

// captureOutput is the capture step's output written to the execution record.
type captureOutput struct {
	// PaymentID is the identifier of the capture that was made: the provider's,
	// or the first tender's when the tenders that pay first paid everything.
	PaymentID string `json:"payment_id"`
	// OtherPaymentIDs are the order's other captures, in the order they were
	// made, when more than one tender paid it (ADR 0209, ADR 0269).
	OtherPaymentIDs []string `json:"other_payment_ids,omitempty"`
	// Captured is the collection's captured total (minor unit).
	Captured int64 `json:"captured"`
}

// Name returns the step's name.
func (s *capturePaymentStep) Name() string { return StepCapturePayment }

// BlocksRecovery reports that this step cannot be counted as not having run
// WHILE ITS RECORD IS MISSING.
//
// [capturePaymentStep.Invoke] sets the flag BEFORE the call, because every fault
// after the call means "the money may have gone". If the process dies right
// there, there is neither a flag nor a record: no way is left to know from the
// records whether the card was charged. For recovery to assume "it never ran",
// release the stock, cancel the order and free the key would mean the customer
// pays again and is charged A SECOND TIME.
//
// This is the very same asymmetric decision as in the step's own compensation:
// in doubt the cheap error is chosen, and the cheap one is a pending order plus
// manual intervention.
func (s *capturePaymentStep) BlocksRecovery() {}

// Restore rebuilds the capture's identifier and the "attempted" flag FROM THE
// RECORD.
//
// The EXISTENCE of the record says that Invoke returned; that is, the capture
// was attempted, and the flag is set unconditionally. A capture step without a
// record, on the other hand, stops recovery altogether (see
// [capturePaymentStep.BlocksRecovery]).
func (s *capturePaymentStep) Restore(sc *workflow.StepContext, output json.RawMessage) error {
	var out captureOutput
	if err := json.Unmarshal(output, &out); err != nil {
		return errors.Wrap(err, errors.KindInternal, CodeSharedStateInvalid,
			"the output of step %q could not be decoded", StepCapturePayment)
	}

	sc.Shared[sharedCaptureAttempted] = true
	if out.PaymentID != "" {
		sc.Shared[sharedPaymentID] = out.PaymentID
	}

	return nil
}

// Invoke captures the amount and VERIFIES the capture against the collection.
//
// The amount to be captured is given EXPLICITLY (plan.Amount), not zero: zero
// means "take the whole held amount", and if the provider held more than was
// asked for, the customer would be overcharged.
//
// After the capture the collection is read again and captured >= amount is
// verified. The verification is the twin of the rule in the authorization step:
// the status string is a derived summary and it can change in a way that makes
// an incomplete capture look complete.
//
// # If the verification blows up
//
// The money HAS BEEN TAKEN and the engine does not compensate a step that fails
// on its single attempt; that is why the error is wrapped with
// [workflow.ErrUncompensated] and the execution is written compensation_failed.
// Returning nil would approve an unpaid order, and returning a plain error would
// quietly count money that was taken as "rolled back".
//
// # AMBIGUOUS CAPTURE: Capture returning an error DOES NOT MEAN "the money stayed"
//
// The most expensive fault is the provider taking the money and losing the
// response (a network timeout). In that case Capture returns an error, no
// capture identifier is left behind, and a pivot guard that looks at the
// identifier CLOSES: the saga cancels the order, releases the stock, and the
// customer loses both their money and their order. That is exactly what the
// package comment calls "must never happen".
//
// That is why the error path is INVESTIGATED (see [capturePaymentStep.settle]):
// the collection is read again and the roll back is done only when the
// collection PROVES that no capture happened. If there is no proof (the read
// blew up too) or a capture is visible, the saga stays on the FORWARD side — the
// order standing, the stock reserved, the execution compensation_failed — and
// the correction is made by hand.
//
// The decision is asymmetric because the prices are asymmetric: the price of
// rolling back by mistake is money that was taken with nothing behind it, and
// repairing it takes a refund flow, accounting and contact with the customer;
// the price of NOT rolling back by mistake is a pending order, reserved stock
// and a hold left on the card — all visible, all reversible. In doubt the cheap
// error is chosen.
func (s *capturePaymentStep) Invoke(ctx context.Context, sc *workflow.StepContext) (any, error) {
	sessionID, err := sharedText(sc, sharedSessionID)
	if err != nil {
		return nil, err
	}
	holds, err := sharedHolds(sc)
	if err != nil {
		return nil, err
	}
	var heldFirst int64
	for _, hold := range holds {
		heldFirst += hold.Authorized
	}
	collectionID, err := sharedText(sc, sharedCollectionID)
	if err != nil {
		return nil, err
	}
	if (sessionID == "" && len(holds) == 0) || collectionID == "" {
		return nil, errors.Internal(CodeSharedStateInvalid,
			"the capture step could not find the payment session: %s", s.plan.CartID)
	}

	// # A provider whose money comes later (ADR 0284)
	//
	// Its session stays authorized: the order is placed owing that part, and the
	// operator captures it when the money arrives. The tenders that paid first
	// are still captured here, and the verification asks for what they held.
	// When nothing is captured, nothing can have moved, and a fault below rolls
	// the saga back like any fault before the pivot.
	owed := s.plan.Amount
	if s.plan.CapturesLater {
		owed = heldFirst
	}
	capturing := owed > 0
	fail := s.dangling
	if !capturing {
		fail = func(cause error) error { return cause }
	}

	// The flag is set BEFORE the call: EVERY fault that occurs from here on (an
	// error, a panic, a timeout) means "the money may have gone" and the pivot
	// guard is in force. The flag is cleared only on a proven zero capture.
	if capturing {
		sc.Shared[sharedCaptureAttempted] = true
	}

	// # Several captures, the uncertain one first (ADR 0209, ADR 0269)
	//
	// When tenders paid part of the order before the provider, the provider's
	// hold is captured FIRST. It is the capture that can be ambiguous; taken
	// first, a failure with no movement still rolls everything back, the other
	// holds included. The gift card's and the balances' captures are this
	// installation's own ledgers and follow in the order they held; if one
	// fails after money was taken, the collection shows a capture and the
	// execution stops for a person, as any capture that cannot be verified
	// does.
	var paymentID string
	if sessionID != "" && !s.plan.CapturesLater {
		if paymentID, err = s.capture(ctx, sc, sessionID, collectionID, s.plan.Amount-heldFirst); err != nil {
			return nil, err
		}
		sc.Shared[sharedPaymentID] = paymentID
	}
	var others []string
	for _, hold := range holds {
		var holdPaymentID string
		if holdPaymentID, err = s.capture(ctx, sc, hold.SessionID, collectionID, hold.Authorized); err != nil {
			return nil, err
		}
		if paymentID == "" {
			paymentID = holdPaymentID
			sc.Shared[sharedPaymentID] = paymentID
		} else {
			others = append(others, holdPaymentID)
		}
	}

	_, amount, _, captured, _, err := s.w.payments.Collection(ctx, collectionID)
	if err != nil {
		return nil, fail(errors.Wrap(err, errors.KindOf(err), CodePaymentUndercaptured,
			"the capture could not be verified: collection %s could not be read", collectionID))
	}
	// The verification is anchored to the amount known LOCALLY, NOT to the one
	// the payment module reports itself.
	//
	// A captured < amount comparison would use the reference reported by the very
	// system it verifies, and the question would come down to "is the collection
	// internally consistent". When the collection said "0 was going to be
	// collected, 0 was collected", a 3000-unit order was being written as
	// successful with ZERO capture. The authorize step's rule (authorized <
	// s.plan.Amount) is already anchored to the local amount; this is its twin.
	if captured < owed {
		return nil, fail(errors.Conflict(CodePaymentUndercaptured,
			"the amount captured does not cover what must be collected now: %d < %d (collection %s)",
			captured, owed, collectionID))
	}

	// If the collection's amount has drifted from the plan this is a separate
	// fault: it means the payment collection was opened with an amount other than
	// the one the saga opened.
	if amount != s.plan.Amount {
		return nil, fail(errors.Internal(CodePaymentUndercaptured,
			"the payment collection's amount has drifted from the plan: collection %d, plan %d (collection %s)",
			amount, s.plan.Amount, collectionID))
	}

	s.w.log.InfoContext(ctx, "payment captured",
		"cart_id", s.plan.CartID, "payment_id", paymentID,
		"captured", captured, "amount", amount, "captures_later", s.plan.CapturesLater)

	return captureOutput{PaymentID: paymentID, OtherPaymentIDs: others, Captured: captured}, nil
}

// capture takes one session's hold and returns the capture's id; a failed call
// is settled against the collection, and a capture with no id is dangling.
func (s *capturePaymentStep) capture(
	ctx context.Context, sc *workflow.StepContext, sessionID, collectionID string, amount int64,
) (string, error) {
	paymentID, err := s.w.payments.Capture(ctx, sessionID, amount)
	if err != nil {
		return "", s.settle(ctx, sc, collectionID, err)
	}
	if paymentID == "" {
		// The money has been taken but there is no trace of it: not even the
		// refund flow could find it.
		return "", s.dangling(errors.Internal(CodeEmptyIdentifier,
			"the payment module returned an EMPTY capture identifier (session %s, collection %s)",
			sessionID, collectionID))
	}

	return paymentID, nil
}

// dangling marks an error that occurs AFTER the capture as a dangling side
// effect.
func (s *capturePaymentStep) dangling(cause error) error {
	return errors.Wrap(errors.Join(cause, workflow.ErrUncompensated),
		errors.KindInternal, CodePaymentUndercaptured,
		"a capture was made on cart %s but could not be verified; MANUAL INTERVENTION is required", s.plan.CartID)
}

// settle investigates whether the money really went after a FAILED capture call
// and decides whether the saga is rolled back.
//
// There are three outcomes and only the first one allows a roll back:
//
//  1. The collection says NO capture happened (captured == 0). That is the
//     proof: the "capture attempted" flag is cleared, the error is returned as
//     is, and the engine rolls the chain back IN REVERSE ORDER — the hold is
//     freed, the order is canceled, the stock is released. This is the normal
//     fault in which the provider never received the request or declined it
//     outright.
//  2. The collection SEES a capture (captured > 0). The money has gone; the
//     response was lost. NO roll back is done.
//  3. The collection CANNOT BE READ. There is no proof, and rolling back without
//     proof risks destroying a paid order. NO roll back is done.
//
// In the second and third cases the error carries [workflow.ErrUncompensated]:
// the execution is written compensation_failed, and that is the MANUAL
// INTERVENTION signal monitoring must count first. The flow going FORWARD on its
// own (counting the capture as successful and closing the cart) is deliberately
// NOT done: we hold no capture identifier, so there is no trace to write to the
// order or to accounting, and saying "successful" would present an unverifiable
// payment as verified. Reconciling the lost response (finding the identifier at
// the provider and carrying the order forward) is a separate flow and belongs to
// plan Phase 7+.
//
// The collection is read with the cleanup context, NOT with the caller's (see
// [cleanupContext]): the most typical cause of the ambiguity is the context
// dying in the first place, and a question asked on a dead context would go
// unanswered.
func (s *capturePaymentStep) settle(
	ctx context.Context,
	sc *workflow.StepContext,
	collectionID string,
	cause error,
) error {
	cctx, cancel := cleanupContext(ctx)
	defer cancel()

	_, _, _, captured, _, readErr := s.w.payments.Collection(cctx, collectionID)
	switch {
	case readErr != nil:
		s.w.log.ErrorContext(ctx, "capture ambiguous: the collection could not be read, NO roll back is done; manual intervention is required",
			"cart_id", s.plan.CartID, "collection_id", collectionID,
			"error", cause, "read_error", readErr)

		return errors.Wrap(errors.Join(cause, readErr, workflow.ErrUncompensated),
			errors.KindInternal, CodeCaptureAmbiguous,
			"the outcome of the capture on cart %s is UNKNOWN (collection %s could not be read); "+
				"an order that may be paid is not rolled back, MANUAL INTERVENTION is required",
			s.plan.CartID, collectionID)

	case captured > 0:
		s.w.log.ErrorContext(ctx, "capture ambiguous: the money was taken but the call returned an error, NO roll back is done",
			"cart_id", s.plan.CartID, "collection_id", collectionID,
			"captured", captured, "amount", s.plan.Amount, "error", cause)

		return errors.Wrap(errors.Join(cause, workflow.ErrUncompensated),
			errors.KindInternal, CodeCaptureAmbiguous,
			"the capture call on cart %s returned an error but the collection appears to have captured %d units "+
				"(collection %s); a paid order is not rolled back, MANUAL INTERVENTION is required",
			s.plan.CartID, captured, collectionID)

	default:
		// PROOF: there is no money movement at all. The pivot guard is lifted and
		// the saga is rolled back in the usual way.
		delete(sc.Shared, sharedCaptureAttempted)
		s.w.log.WarnContext(ctx, "the capture failed; the collection reports no movement at all, the saga is being rolled back",
			"cart_id", s.plan.CartID, "collection_id", collectionID, "error", cause)
		return cause
	}
}

// Compensate reports that the capture IS NOT ROLLED BACK.
//
// The capture is the saga's PIVOT step: after the money is taken there is no
// automatic way back. A refund is not the capture's compensation but a SEPARATE
// flow (plan Phase 7+) and it touches the customer, the order and accounting one
// by one; hiding it silently inside a compensation step would mean the saga
// creating a real money movement at the very place where it says "rolled back".
//
// That is why an error is returned if the capture happened: the engine writes
// the execution compensation_failed, and that is the MANUAL INTERVENTION signal
// monitoring must count first. Returning nil would be a lie that records the
// execution as "the work was done and ROLLED BACK".
//
// The error is errors.Conflict and that class is NOT retried (see
// workflow.DefaultRetryable): trying a permanent condition three times would
// only produce latency.
//
// If the capture was never ATTEMPTED the call is a no-op: there is nothing to
// roll back and the hold is freed by the authorization step's compensation. If
// it was attempted but there is no identifier (a capture with an unknown
// outcome, see [capturePaymentStep.settle]) compensation still says "could not
// be rolled back" — counting a money movement whose outcome is unknown as
// "rolled back" is no less of a lie than counting a known capture that way.
func (s *capturePaymentStep) Compensate(ctx context.Context, sc *workflow.StepContext) error {
	paymentID, err := sharedText(sc, sharedPaymentID)
	if err != nil {
		return err
	}
	attempted, err := sharedFlag(sc, sharedCaptureAttempted)
	if err != nil {
		return err
	}
	if paymentID == "" && !attempted {
		return nil
	}

	if paymentID == "" {
		s.w.log.ErrorContext(ctx, "compensation: a capture with an unknown outcome cannot be rolled back; reconciliation is required",
			"cart_id", s.plan.CartID, "amount", s.plan.Amount)

		return errors.Conflict(CodeCaptureAmbiguous,
			"the outcome of the capture on cart %s is unknown; it cannot be rolled back in this flow and MANUAL reconciliation is required",
			s.plan.CartID)
	}

	s.w.log.ErrorContext(ctx, "compensation: a captured amount cannot be rolled back; the refund flow is required",
		"cart_id", s.plan.CartID, "payment_id", paymentID, "amount", s.plan.Amount)

	return errors.Conflict(CodeCaptureIrreversible,
		"capture %s (%d %s) cannot be rolled back in this flow; a refund is a SEPARATE flow and must be started BY HAND",
		paymentID, s.plan.Amount, s.plan.CurrencyCode)
}
