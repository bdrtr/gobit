package checkout

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// Keys of the data carried between steps.
//
// The keys are compensation's ONLY source of truth: a Compensate learns what its
// own Invoke produced from here (see [workflow.StepContext].Shared). The prefix
// prevents a collision if some other component uses the same map later on.
const (
	sharedReservations = "checkout.reservations"
	sharedOrderID      = "checkout.order_id"
	sharedCollectionID = "checkout.collection_id"
	sharedSessionID    = "checkout.session_id"
	sharedPaymentID    = "checkout.payment_id"
	// sharedCaptureAttempted reports that the capture call was STARTED and is the
	// real trigger of the pivot guard (see [Workflows.skipAfterCapture]).
	//
	// The flag is set BEFORE the call, because the answer to the question "did
	// the money go" is NOT in the capture identifier: when the provider takes the
	// money and loses the response, Capture returns an error and no identifier is
	// left behind. A guard tied to the identifier closes in that case, and the
	// saga would then roll back a paid order.
	//
	// The flag is cleared ONLY when the collection PROVES that no capture
	// happened (see capturePaymentStep.settle).
	sharedCaptureAttempted = "checkout.capture_attempted"
	// sharedRedeemed holds the promotion uses this saga has taken, so that the
	// compensation can release exactly those and no others.
	sharedRedeemed = "checkout.redeemed"
)

// sharedRedemptions reads the promotion uses from the shared map.
//
// It follows [sharedRefs]'s contract: a key that was never written is an empty
// slice, and a key of an unexpected type is an error rather than a silent
// nothing — compensation reporting "done" without having found the work it is
// meant to undo is how a spent coupon stays spent.
func sharedRedemptions(sc *workflow.StepContext) ([]redeemedRef, error) {
	raw, exists := sc.Shared[sharedRedeemed]
	if !exists {
		return nil, nil
	}
	refs, ok := raw.([]redeemedRef)
	if !ok {
		return nil, errors.Internal(CodeSharedStateInvalid,
			"key %q has an unexpected type: %T", sharedRedeemed, raw)
	}

	return refs, nil
}

// sharedRefs reads the reservation traces from the shared map.
//
// If the key was never written it returns an empty slice: that is the normal
// case in which the step has not taken any reservation yet. If the key is SET
// but its type is unexpected it returns an error; returning empty silently would
// make compensation claim "done" without having found the work it is meant to
// undo.
func sharedRefs(sc *workflow.StepContext) ([]reservationRef, error) {
	raw, exists := sc.Shared[sharedReservations]
	if !exists {
		return nil, nil
	}
	refs, ok := raw.([]reservationRef)
	if !ok {
		return nil, errors.Internal(CodeSharedStateInvalid,
			"key %q has an unexpected type: %T", sharedReservations, raw)
	}
	return refs, nil
}

// sharedText reads an identifier from the shared map.
//
// If the key is missing it returns the empty string; if the type is unexpected
// it returns an error (see [sharedRefs]).
func sharedText(sc *workflow.StepContext, key string) (string, error) {
	raw, exists := sc.Shared[key]
	if !exists {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", errors.Internal(CodeSharedStateInvalid,
			"key %q has an unexpected type: %T", key, raw)
	}
	return value, nil
}

// sharedFlag reads a flag from the shared map.
//
// If the key is missing it returns false; if the type is unexpected it returns
// an error (see [sharedRefs]).
func sharedFlag(sc *workflow.StepContext, key string) (bool, error) {
	raw, exists := sc.Shared[key]
	if !exists {
		return false, nil
	}
	value, ok := raw.(bool)
	if !ok {
		return false, errors.Internal(CodeSharedStateInvalid,
			"key %q has an unexpected type: %T", key, raw)
	}
	return value, nil
}

// skipAfterCapture says whether compensation runs after the PIVOT.
//
// If capture was ATTEMPTED the compensation chain DOES NOT GO ON: the order is
// not canceled, the stock is not released, the hold is not freed. The reason is
// a single sentence — rolling the order back while the money has been taken
// would cost the customer both their money and their order; yet that is exactly
// the opposite of what the saga is trying to compensate for.
//
// The measure is not "did capture SUCCEED" but "was capture ATTEMPTED" (see
// [sharedCaptureAttempted]). A guard that looked at success would close in the
// case where the payment provider takes the money and loses the response — that
// is, in the very case where the guard is needed most. The flag is cleared only
// when the collection PROVES that no capture happened, so the "roll back"
// decision rests on evidence rather than on the presence of an identifier.
//
// Two things guarantee that the decision does not stay silent: every skip is
// logged as ERROR, and the execution ends up compensation_failed either way — if
// the capture step succeeded its own Compensate returns a "cannot be undone"
// error, and if it failed its error carries [workflow.ErrUncompensated].
func (w *Workflows) skipAfterCapture(ctx context.Context, sc *workflow.StepContext, step, cartID string) (bool, error) {
	paymentID, err := sharedText(sc, sharedPaymentID)
	if err != nil {
		return false, err
	}
	attempted, err := sharedFlag(sc, sharedCaptureAttempted)
	if err != nil {
		return false, err
	}
	if paymentID == "" && !attempted {
		return false, nil
	}

	w.log.ErrorContext(ctx, "compensation skipped: capture was attempted, an order that may be paid is not rolled back",
		"step", step, "cart_id", cartID, "payment_id", paymentID, "capture_attempted", attempted)
	return true, nil
}

// cleanupContext produces a bounded context for a step's OWN cleanup that is
// unaffected by cancellation.
//
// The engine runs the compensation chain with context.WithoutCancel, but the
// cleanup inside Invoke stays on the step's context; yet one of the cases in
// which cleanup is needed most is precisely the context dying. Even when the
// saga is detached from the caller's cancellation (see [sagaContext]) its own
// time budget can run out, and the moment it does is the moment a half-finished
// side effect comes to a stop. The budget is the same as the compensation
// budget: both do the same job (undoing a side effect).
func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), CompensationTimeout)
}

// retryCleanup retries a step's OWN cleanup with the compensation policy.
//
// In-step cleanup (releasing a half-finished reservation, the hold of a
// half-finished authorization) does the SAME job as the engine's compensation;
// the only difference is which path the error was caught on. That is why the
// policy has to be the same as well: otherwise a transient fault would produce
// manual intervention — or not — depending only on which path caught it. The
// rationale is the same as [compensationRetry]'s: the price of a failed
// compensation is manual intervention.
//
// Permanent errors (errors.Conflict, errors.Invalid) and a dead context are
// already filtered out by [workflow.DefaultRetryable]; retrying them would only
// produce latency.
func retryCleanup(ctx context.Context, attempt func() error) error {
	policy := compensationRetry()
	backoff := policy.Backoff

	for i := 1; ; i++ {
		err := attempt()
		if err == nil {
			return nil
		}
		if i >= policy.MaxAttempts || !workflow.DefaultRetryable(err) {
			return err
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return err
		case <-timer.C:
		}

		backoff = time.Duration(float64(backoff) * policy.Multiplier)
		if backoff > policy.MaxBackoff {
			backoff = policy.MaxBackoff
		}
	}
}

// releaseAll releases the given reservations and returns THE ONES IT COULD NOT
// RELEASE.
//
// The chain DOES NOT STOP at the first error: one reservation failing to be
// released is no reason for the others to stay dangling. The errors are joined
// with errors.Join.
func (w *Workflows) releaseAll(ctx context.Context, refs []reservationRef) ([]reservationRef, error) {
	var (
		remaining []reservationRef
		failures  []error
	)
	for i := range refs {
		if err := w.inventory.ReleaseReservation(ctx, refs[i].ReservationID); err != nil {
			remaining = append(remaining, refs[i])
			failures = append(failures, errors.Wrap(err, errors.KindOf(err), CodeReservationLeaked,
				"reservation %s could not be released (line %s)", refs[i].ReservationID, refs[i].LineItemID))
			continue
		}
		w.log.DebugContext(ctx, "reservation released",
			"reservation_id", refs[i].ReservationID, "line_item_id", refs[i].LineItemID)
	}
	return remaining, errors.Join(failures...)
}
