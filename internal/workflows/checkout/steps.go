package checkout

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
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
)

// reservationRef is the trace of a reservation taken for one line.
type reservationRef struct {
	// LineItemID is the cart line the reservation was opened for.
	LineItemID string `json:"line_item_id"`
	// ReservationID is the reservation identifier the inventory module produced.
	ReservationID string `json:"reservation_id"`
	// LocationID is the location the stock was RESERVED at.
	//
	// It is not needed in order to release it — compensation uses only the
	// reservation identifier — but it IS written to the record: because the
	// location can be picked per line (see [CompleteCartInput.LocationID]), the
	// lines of one order may have been reserved from different warehouses. An
	// operator intervening by hand must be able to answer the "which warehouse"
	// question from the execution record; without the field the answer could only
	// be found by asking the inventory module one line at a time.
	LocationID string `json:"location_id"`
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

// reserveInventoryStep reserves stock for every line of the cart.
type reserveInventoryStep struct {
	w    *Workflows
	plan *checkoutPlan
}

// reserveOutput is the inventory step's output written to the execution record.
type reserveOutput struct {
	// Reservations are the reservations that were taken.
	Reservations []reservationRef `json:"reservations"`
}

// Name returns the step's name.
func (s *reserveInventoryStep) Name() string { return StepReserveInventory }

// Restore rebuilds the reservations that were taken FROM THE RECORD.
//
// The only thing compensation needs is the reservation identifiers, and they are
// already durable in the step's output: [reserveOutput]. That is why the stock
// of an abandoned execution can be released even after the process has died.
//
// An empty output returns an ERROR: the output of a step that took reservations
// cannot be empty, and quietly putting an empty slice in its place would make
// compensation claim "done" without having found the stock it is meant to
// release.
func (s *reserveInventoryStep) Restore(sc *workflow.StepContext, output json.RawMessage) error {
	var out reserveOutput
	if err := json.Unmarshal(output, &out); err != nil {
		return errors.Wrap(err, errors.KindInternal, CodeSharedStateInvalid,
			"the output of step %q could not be decoded", StepReserveInventory)
	}
	if len(out.Reservations) == 0 {
		return errors.Internal(CodeSharedStateInvalid,
			"the record of step %q holds no reservation; compensation cannot know what to release",
			StepReserveInventory)
	}

	sc.Shared[sharedReservations] = out.Reservations

	return nil
}

// Invoke reserves stock per line and writes the identifiers into the shared map.
//
// The identifiers are written AFTER every successful reservation, not once they
// are all done: compensation (and the engine's best-effort compensation) reads
// that map as the only source of truth it has, and if it cannot find the trace
// of a half-finished step there, the reserved stock would stay dangling.
//
// # A half-finished step does its OWN cleanup
//
// If a line blows up, the reservations taken up to that point are released HERE.
// The reason is the engine's contract: a step that fails on its single attempt
// is NOT compensated, so the debt of "either succeed completely or leave no work
// behind" belongs to the step (see the core/workflow package comment). If the
// cleanup blows up as well, the error is wrapped with
// [workflow.ErrUncompensated]: seeing the sentinel, the engine writes the
// execution as compensation_failed rather than "rolled back", and manual
// intervention is requested.
//
// # An EMPTY reservation identifier does not count as success
//
// If the inventory module returns an empty identifier without an error, the
// reservation WAS made but we do NOT have its trace: neither this step nor
// compensation can release it. Accepting it silently would leave a reservation
// that appears on no list dangling forever; that is why the case is reported
// with [workflow.ErrUncompensated] and the reservations taken up to that point
// are released all the same.
//
// # The location is decided PER line
//
// If the caller did not name a location, the warehouse of every line is decided
// separately (see [reserveInventoryStep.locationFor]) and the lines of one order
// may be reserved from different warehouses. The candidates and the preference
// order are resolved immediately BEFORE the reservation, not during preparation:
// the candidates are a fact read without a lock, and every millisecond between
// the read and the reservation is a chance for a warehouse that made the list to
// be exhausted by the time Reserve is reached. The race does not close
// completely — the only thing that closes it is Reserve's own lock — but the
// window is not widened for nothing; that is why the ordering is asked for ONCE
// per line.
//
// The ordering is a new point at which a line can blow up as well, and just like
// the reservation it falls to [reserveInventoryStep.unwind]: the reservations of
// the previous lines are released. In a multi-warehouse cart this happens more
// easily — the first line may have been reserved from one warehouse while the
// second line is found in no warehouse at all.
func (s *reserveInventoryStep) Invoke(ctx context.Context, sc *workflow.StepContext) (any, error) {
	refs := make([]reservationRef, 0, len(s.plan.Lines))

	for i := range s.plan.Lines {
		line := s.plan.Lines[i]

		locationID, reservationID, err := s.reserveLine(ctx, line)
		if err != nil {
			return nil, s.unwind(ctx, sc, refs, line, locationID, err)
		}
		if reservationID == "" {
			return nil, s.unwind(ctx, sc, refs, line, locationID, errors.Join(
				errors.Internal(CodeEmptyIdentifier,
					"the inventory module returned an EMPTY reservation identifier for line %s; the reserved stock cannot be released",
					line.LineItemID),
				workflow.ErrUncompensated))
		}

		refs = append(refs, reservationRef{
			LineItemID:    line.LineItemID,
			ReservationID: reservationID,
			LocationID:    locationID,
		})
		sc.Shared[sharedReservations] = refs
	}

	s.w.log.DebugContext(ctx, "stock reserved",
		"cart_id", s.plan.CartID, "lines", len(refs))
	return reserveOutput{Reservations: refs}, nil
}

// locationFor returns the CANDIDATE locations the line's stock can be reserved
// from.
//
// It returns a LIST rather than a single location, and the reason is concrete:
// the candidates are read without a lock, the reservation is made under a lock,
// and in the window between them the chosen warehouse can run out. Had it
// returned a single location the caller would have nowhere to fall back to, and
// the order would be dropped while stock sat in another warehouse (see
// [reserveInventoryStep.reserveLine]).
//
// # If the caller named one there is NO CHOICE
//
// If [CompleteCartInput.LocationID] is set, a single-element list is returned
// and no module is asked. The location named is not a preference but an
// INSTRUCTION; treating it as a "candidate" and having the fulfillment module
// approve it could silently change the caller's decision.
//
// # If it is empty the candidates come from the INVENTORY module
//
// Which warehouses hold enough units is a FACT and it is the inventory module's
// job. Which of them it ships from is a DECISION and it belongs to the
// fulfillment module (see [reserveInventoryStep.rankCandidates]). The split is
// deliberate: gathering the two halves on one surface would tie the stock query
// to fulfillment policy, or fulfillment policy to the stock schema.
//
// # If there is no candidate THIS package makes the call
//
// The inventory module returns an empty list, not an error (see
// [Inventory.LocationsWithStock]); it is this step that draws the "cannot be
// ordered" conclusion, and the class is the SAME one Reserve returns on
// insufficient stock (errors.Conflict, [CodeReservationFailed]). Asking the
// fulfillment module about an empty list would produce an error of the same
// class too, but it would point at the wrong module: what is missing is not a
// warehouse to ship from but the STOCK to reserve, and what the operator needs
// to see in the message is the item and the quantity.
func (s *reserveInventoryStep) locationFor(ctx context.Context, line planLine) ([]string, error) {
	if s.plan.LocationID != "" {
		return []string{s.plan.LocationID}, nil
	}

	candidates, err := s.w.inventory.LocationsWithStock(ctx, line.InventoryItemID, line.Quantity)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, errors.Conflict(CodeReservationFailed,
			"no location can reserve %d of item %s", line.Quantity, line.InventoryItemID)
	}

	return candidates, nil
}

// reserveLine reserves the line's stock and returns the location that was used.
//
// # Why we do not settle for a single candidate
//
// The candidate list is read WITHOUT A LOCK, while the reservation is made under
// a lock. In the window between them the warehouse at the head of the order may
// have run out of stock, and Reserve then returns errors.Conflict. An
// implementation that settled for one candidate would drop the WHOLE order — and
// that while ANOTHER warehouse held enough stock.
//
// This is not merely a theoretical race: the order is deterministic, meaning
// every concurrently arriving order tries the SAME warehouse and they all
// collide on the same row. A deterministic order does not reduce contention, it
// concentrates it.
//
// # The order is asked for ONCE
//
// The fulfillment module is called once per line and gives the preference order;
// falling back means moving on to the next entry in that list. Asking again on
// every exhaustion would produce the same answer (the order is deterministic)
// but would re-read the policy records every time: N queries instead of one for
// a line with N candidates, and every one of them a round trip that lengthens
// the race window between the candidates being read WITHOUT A LOCK and the
// reservation being made UNDER one.
//
// # Why this is NOT retrying the step
//
// The engine's step retry is deliberately off (see [Workflows.CompleteCart]): if
// Reserve is called twice it produces two reservations. There is no such risk
// here — the call being fallen back from FAILED, which means it left no
// reservation behind. What is being tried is not the same work but ANOTHER
// warehouse for the same work.
//
// # A fallback happens only on a CONFLICT
//
// errors.Conflict means "not enough stock" in the [Inventory.Reserve] contract,
// and another warehouse may answer differently. The other error classes (an
// unreachable database, invalid input) give the SAME answer at every warehouse;
// insisting on them would hide the fault and multiply the latency by the number
// of candidates.
//
// If the caller DID name a location the order has a single entry and there is
// nowhere to fall back to: the location named is not a preference but an
// instruction.
//
// # The loop TERMINATES
//
// The order is a finite slice and every turn advances by one element;
// termination is bounded by the length of the slice, independently of what the
// fulfillment module returns. This is the second gain from asking for the order
// once: termination used to depend on the chosen candidate being removable from
// the list — that is, on the module not going OUTSIDE the candidate set.
func (s *reserveInventoryStep) reserveLine(
	ctx context.Context, line planLine,
) (locationID, reservationID string, err error) {
	candidates, err := s.locationFor(ctx, line)
	if err != nil {
		return "", "", err
	}

	ranked, err := s.rankCandidates(ctx, line, candidates)
	if err != nil {
		return "", "", err
	}

	var lastErr error

	for i, chosen := range ranked {
		reservationID, err := s.w.inventory.Reserve(ctx,
			line.InventoryItemID, chosen, line.Quantity, line.LineItemID)

		switch {
		case err == nil:
			return chosen, reservationID, nil
		case !errors.IsConflict(err):
			return chosen, "", err
		}

		lastErr = err

		s.w.log.DebugContext(ctx, "warehouse exhausted, moving on to the next candidate",
			"cart_id", s.plan.CartID, "line_item_id", line.LineItemID,
			"location_id", chosen, "rank_length", len(ranked), "rank_index", i)
	}

	return "", "", lastErr
}

// rankCandidates has the fulfillment module rank the candidates into PREFERENCE ORDER.
//
// If the caller named a location there is no ordering and no module is asked:
// the location named is not a preference but an INSTRUCTION; treating it as a
// "candidate" and having the fulfillment module approve it could silently change
// the caller's decision.
//
// Otherwise the question splits in TWO: which warehouses hold enough stock is a
// FACT (the inventory module, already called and in our hands), which of them it
// ships from is a DECISION (the fulfillment module). This package building the
// order would be the worst of all — the cart flow has nothing to say about
// warehouse policy.
//
// The only context that enters the decision is the order's REGION, and it comes
// from the plan. The fulfillment module knows from its own records whether a
// warehouse serves that region; what this package carries is not the policy but
// the policy's QUESTION. Anything beyond the region (e.g. the delivery address)
// is deliberately not passed on: the execution record is a durable ledger and
// plan Section 8 asks that sensitive data not be written there.
//
// # The answer is checked in three places
//
// If the fulfillment module returns an empty order, an identifier that is not a
// candidate, or the same candidate twice, the error is errors.Internal. All
// three are violations of the contract, and had they not been checked the fault
// would have surfaced one module away from its cause: a reservation would be
// tried at a warehouse that is not a candidate, and a duplicated candidate would
// lead to the same warehouse being visited twice.
func (s *reserveInventoryStep) rankCandidates(
	ctx context.Context, line planLine, candidates []string,
) ([]string, error) {
	if s.plan.LocationID != "" {
		return []string{s.plan.LocationID}, nil
	}

	ranked, err := s.w.fulfillment.RankLocations(ctx, s.plan.RegionID, candidates)
	if err != nil {
		return nil, err
	}
	if len(ranked) == 0 {
		return nil, errors.Internal(CodeReservationFailed,
			"the fulfillment module returned an EMPTY order out of %d candidates (item %s)",
			len(candidates), line.InventoryItemID)
	}

	seen := make(map[string]struct{}, len(ranked))
	for _, chosen := range ranked {
		if !slices.Contains(candidates, chosen) {
			return nil, errors.Internal(CodeReservationFailed,
				"the fulfillment module ranked a location that is not a candidate: %s (item %s)",
				chosen, line.InventoryItemID)
		}
		if _, dup := seen[chosen]; dup {
			return nil, errors.Internal(CodeReservationFailed,
				"the fulfillment module ranked the same location twice: %s (item %s)",
				chosen, line.InventoryItemID)
		}
		seen[chosen] = struct{}{}
	}

	return ranked, nil
}

// unwind does the half-finished reservation's own cleanup and produces the final
// error.
//
// The cleanup is retried with the SAME policy as the engine's compensation (see
// [retryCleanup]) and every attempt touches only the REMAINING reservations:
// releasing an already released reservation is pointless, and pruning the list
// keeps it visible which identifier is really left dangling.
//
// locationID is the line's warehouse and it is empty in TWO cases: when the
// order could not be built at all (there is no candidate, or all of them were
// filtered out) and when ALL the warehouses in the order were tried and
// exhausted. The message says "unselected" in both, and that is what the message
// DOES NOT SAY — which of the two happened is read FROM THE CODE (if there is no
// candidate, this package's code; if the filtering emptied it, the fulfillment
// module's code; if it was exhausted, the inventory module's code). Writing the
// plan's location would be wrong: the field is optional, and putting an empty
// field into the message in a flow that picks a warehouse per line would make
// the operator say "we tried to reserve at an empty location".
//
// # The CODE of the underlying error is preserved
//
// The wrapping class (Kind) was already inherited from the underlying error; the
// code is inherited too, and [CodeReservationFailed] is only a FALLBACK for an
// error that carries no code. The pattern is taken from the engine's own
// wrapping (see [github.com/bdrtr/gobit/internal/core/workflow.CodeStepFailed])
// and the rationale is written down there with a measured price: the transport
// layer writes a single machine-readable field (the code) into the body, and if
// that field flattens to a single value the client cannot tell different faults
// apart.
//
// The price is even more concrete here. Three separate worlds blow up in the
// same class (409) in this step: no warehouse holds enough stock, the chosen
// warehouse was exhausted in the race, or no candidate SERVES the order's
// region. The third is NOT a stock problem but the consequence of a fulfillment
// policy the operator wrote, and its fix lies somewhere else. Had the code been
// overwritten, "stock could not be reserved" would be reported with full shelves
// and the operator would not find the place to look — the message chain carries
// the cause, but the transport layer only publishes the outermost message.
func (s *reserveInventoryStep) unwind(
	ctx context.Context,
	sc *workflow.StepContext,
	refs []reservationRef,
	line planLine,
	locationID string,
	cause error,
) error {
	location := locationID
	if location == "" {
		location = "unselected"
	}
	code := errors.CodeOf(cause)
	if code == "" {
		code = CodeReservationFailed
	}
	failure := errors.Wrap(cause, errors.KindOf(cause), code,
		"stock could not be reserved for line %s (item %s, location %s, quantity %d)",
		line.LineItemID, line.InventoryItemID, location, line.Quantity)
	if len(refs) == 0 {
		return failure
	}

	cctx, cancel := cleanupContext(ctx)
	defer cancel()

	remaining := refs
	releaseErr := retryCleanup(cctx, func() error {
		var err error
		remaining, err = s.w.releaseAll(cctx, remaining)
		return err
	})
	sc.Shared[sharedReservations] = remaining
	if releaseErr == nil {
		return failure
	}

	s.w.log.ErrorContext(ctx, "the half-finished stock reservation could not be released; manual intervention is required",
		"cart_id", s.plan.CartID, "leaked", len(remaining), "error", releaseErr)

	return errors.Wrap(errors.Join(failure, releaseErr, workflow.ErrUncompensated),
		errors.KindInternal, CodeReservationLeaked,
		"cart %s has %d reservations left dangling", s.plan.CartID, len(remaining))
}

// Compensate releases all the stock that was reserved; it is IDEMPOTENT.
//
// An already released reservation does not fail on the second call, so
// compensation can be retried. The ones that could not be released STAY in the
// shared map: if compensation is retried only those are tried, and the engine's
// record shows which reservation is dangling.
//
// If a capture was made the stock is NOT RELEASED (see
// [Workflows.skipAfterCapture]): a paid order stays standing and its goods must
// still be reserved; releasing them would mean selling the same stock a second
// time.
func (s *reserveInventoryStep) Compensate(ctx context.Context, sc *workflow.StepContext) error {
	skip, err := s.w.skipAfterCapture(ctx, sc, StepReserveInventory, s.plan.CartID)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	refs, err := sharedRefs(sc)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		return nil
	}

	remaining, releaseErr := s.w.releaseAll(ctx, refs)
	sc.Shared[sharedReservations] = remaining
	if releaseErr != nil {
		return errors.Wrap(releaseErr, errors.KindOf(releaseErr), CodeReservationLeaked,
			"cart %s has %d reservations that could not be released", s.plan.CartID, len(remaining))
	}

	s.w.log.InfoContext(ctx, "compensation: stock reservations released",
		"cart_id", s.plan.CartID, "reservations", len(refs))
	return nil
}

// createOrderStep places an order from the cart's snapshot.
type createOrderStep struct {
	w    *Workflows
	plan *checkoutPlan
}

// createOrderOutput is the order step's output written to the execution record.
type createOrderOutput struct {
	// OrderID is the identifier of the order that was placed.
	OrderID string `json:"order_id"`
}

// Name returns the step's name.
func (s *createOrderStep) Name() string { return StepCreateOrder }

// Restore rebuilds the identifier of the placed order FROM THE RECORD.
func (s *createOrderStep) Restore(sc *workflow.StepContext, output json.RawMessage) error {
	var out createOrderOutput
	if err := json.Unmarshal(output, &out); err != nil {
		return errors.Wrap(err, errors.KindInternal, CodeSharedStateInvalid,
			"the output of step %q could not be decoded", StepCreateOrder)
	}
	if out.OrderID == "" {
		return errors.Internal(CodeSharedStateInvalid,
			"the record of step %q holds no order identifier", StepCreateOrder)
	}

	sc.Shared[sharedOrderID] = out.OrderID

	return nil
}

// Invoke places the order and writes its identifier into the shared map.
//
// The EXECUTION identifier is put into the snapshot as the idempotency key: a
// call repeated within the same execution does not place a new order. The
// "order.placed" event is published by the order module itself; this step
// publishes no event at all (see the package comment).
//
// # An EMPTY order identifier does not count as success
//
// If the order module returns an empty identifier without an error, the order
// WAS placed but we do NOT have its trace. Accepting the identifier silently
// would produce two lies: compensation would think "no order was ever placed"
// and do nothing (an ORPHAN order stays standing), and the result would report a
// "successful" order whose order_id is empty. The case is reported with
// [workflow.ErrUncompensated]; the stock is still released, but the execution is
// written compensation_failed instead of "rolled back".
func (s *createOrderStep) Invoke(ctx context.Context, sc *workflow.StepContext) (any, error) {
	payload, err := s.plan.orderSnapshotJSON(sc.ExecutionID)
	if err != nil {
		return nil, err
	}

	orderID, err := s.w.orders.PlaceOrderJSON(ctx, payload)
	if err != nil {
		return nil, err
	}
	if orderID == "" {
		return nil, errors.Wrap(errors.Join(
			errors.Internal(CodeEmptyIdentifier,
				"the order module returned an EMPTY order identifier; an order that may have been placed cannot be canceled"),
			workflow.ErrUncompensated),
			errors.KindInternal, CodeEmptyIdentifier,
			"cart %s may have an order without an identifier left dangling; MANUAL INTERVENTION is required", s.plan.CartID)
	}
	sc.Shared[sharedOrderID] = orderID

	s.w.log.InfoContext(ctx, "order placed",
		"cart_id", s.plan.CartID, "order_id", orderID, "amount", s.plan.Amount)
	return createOrderOutput{OrderID: orderID}, nil
}

// Compensate cancels the order; it is IDEMPOTENT.
//
// If no order was ever placed (there is no identifier) the call is a no-op:
// compensation does not go looking for a record that does not exist.
//
// If a capture was made the order is NOT CANCELED (see
// [Workflows.skipAfterCapture]): canceling an order whose money has been taken
// would cost the customer both their money and their order. The order stays
// standing and the manual-intervention signal is read from the execution's
// status.
func (s *createOrderStep) Compensate(ctx context.Context, sc *workflow.StepContext) error {
	skip, err := s.w.skipAfterCapture(ctx, sc, StepCreateOrder, s.plan.CartID)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	orderID, err := sharedText(sc, sharedOrderID)
	if err != nil {
		return err
	}
	if orderID == "" {
		return nil
	}

	if cancelErr := s.w.orders.CancelOrder(ctx, orderID, cancelReason(sc)); cancelErr != nil {
		return cancelErr
	}

	s.w.log.InfoContext(ctx, "compensation: order canceled",
		"cart_id", s.plan.CartID, "order_id", orderID)
	return nil
}

// cancelReason produces the order's cancellation reason.
//
// The reason carries the execution identifier: whoever looks at a canceled order
// must be able to find in the record which flow and which execution rolled it
// back.
func cancelReason(sc *workflow.StepContext) string {
	return "complete_cart compensation (execution: " + sc.ExecutionID + ")"
}

// authorizePaymentStep opens the payment collection, opens a session and
// authorizes.
type authorizePaymentStep struct {
	w    *Workflows
	plan *checkoutPlan
}

// authorizeOutput is the authorization step's output written to the execution
// record.
type authorizeOutput struct {
	// CollectionID is the identifier of the payment collection.
	CollectionID string `json:"collection_id"`
	// SessionID is the identifier of the payment session.
	SessionID string `json:"session_id"`
	// Status is the session's status as returned by the provider.
	Status string `json:"status"`
	// Authorized is the amount actually held (minor unit).
	Authorized int64 `json:"authorized"`
}

// Name returns the step's name.
func (s *authorizePaymentStep) Name() string { return StepAuthorizePayment }

// Restore rebuilds the payment collection and the session FROM THE RECORD.
func (s *authorizePaymentStep) Restore(sc *workflow.StepContext, output json.RawMessage) error {
	var out authorizeOutput
	if err := json.Unmarshal(output, &out); err != nil {
		return errors.Wrap(err, errors.KindInternal, CodeSharedStateInvalid,
			"the output of step %q could not be decoded", StepAuthorizePayment)
	}
	if out.CollectionID == "" || out.SessionID == "" {
		return errors.Internal(CodeSharedStateInvalid,
			"the record of step %q holds no collection or session identifier", StepAuthorizePayment)
	}

	sc.Shared[sharedCollectionID] = out.CollectionID
	sc.Shared[sharedSessionID] = out.SessionID

	return nil
}

// Invoke opens the collection, opens the session and has the amount held.
//
// # THE FULL PAYMENT RULE
//
// If the amount held does not cover the amount that must be collected, the step
// FAILS: authorized < plan.Amount. The rule looks at the NUMBER, not at the
// provider's STATUS string, because on a partial authorization the status is
// still "authorized" and a saga that only looked at the status would approve an
// unpaid order — that was the most serious finding in this project.
//
// # The half-finished step
//
// If the authorization blows up or falls short, the session is canceled HERE;
// otherwise a partially held amount would stay dangling on the customer's card,
// and the engine does not compensate a step that fails on its single attempt. If
// the cancellation blows up as well, the error is wrapped with
// [workflow.ErrUncompensated].
//
// If the session cannot be opened, all that is left behind is an EMPTY
// collection and it is not cleaned up: a collection holds no money, it is only a
// ledger line saying "this much was going to be collected", and the payment
// module's surface has no delete.
//
// # EMPTY identifiers are stopped here
//
// If the collection or the session identifier comes back empty the step fails at
// once. This is the CHEAPEST breaking point of the identifiers on the payment
// path: since no authorization has been made yet there is no held amount on the
// customer's card, and the only price is a reservation that gets rolled back.
// Going on with an empty identifier would lead to the capture step saying "I
// could not find the session", or to compensation quietly falling through to a
// no-op.
func (s *authorizePaymentStep) Invoke(ctx context.Context, sc *workflow.StepContext) (any, error) {
	collectionID, err := s.w.payments.CreateCollection(ctx,
		s.plan.CartID, s.plan.CurrencyCode, s.plan.Amount)
	if err != nil {
		return nil, err
	}
	if collectionID == "" {
		return nil, errors.Internal(CodeEmptyIdentifier,
			"the payment module returned an EMPTY collection identifier: %s", s.plan.CartID)
	}
	sc.Shared[sharedCollectionID] = collectionID

	sessionID, err := s.w.payments.OpenSessionWithData(ctx,
		collectionID, s.plan.PaymentProviderID, sc.ExecutionID, s.plan.PaymentData)
	if err != nil {
		return nil, err
	}
	if sessionID == "" {
		return nil, errors.Internal(CodeEmptyIdentifier,
			"the payment module returned an EMPTY session identifier: %s (collection %s)",
			s.plan.CartID, collectionID)
	}
	sc.Shared[sharedSessionID] = sessionID

	status, authorized, err := s.w.payments.Authorize(ctx, sessionID)
	if err != nil {
		return nil, s.releaseHold(ctx, sessionID, err)
	}
	if authorized < s.plan.Amount {
		return nil, s.releaseHold(ctx, sessionID, errors.Conflict(CodePaymentUnderauthorized,
			"the amount held does not cover what must be collected: %d < %d (session %s, status %q)",
			authorized, s.plan.Amount, sessionID, status))
	}

	s.w.log.InfoContext(ctx, "payment authorized",
		"cart_id", s.plan.CartID, "collection_id", collectionID, "session_id", sessionID,
		"authorized", authorized, "amount", s.plan.Amount)

	return authorizeOutput{
		CollectionID: collectionID,
		SessionID:    sessionID,
		Status:       status,
		Authorized:   authorized,
	}, nil
}

// releaseHold frees the hold of a half-finished authorization.
//
// The cancellation is retried with the SAME policy as the engine's compensation
// (see [retryCleanup]): leaving the hold on the customer's card dangling because
// of a transient fault is an outcome that would not have happened had the same
// fault been caught in the compensation chain.
func (s *authorizePaymentStep) releaseHold(ctx context.Context, sessionID string, cause error) error {
	cctx, cancel := cleanupContext(ctx)
	defer cancel()

	if err := retryCleanup(cctx, func() error {
		return s.w.payments.Cancel(cctx, sessionID)
	}); err != nil {
		s.w.log.ErrorContext(ctx, "the half-finished payment session could not be canceled; manual intervention is required",
			"cart_id", s.plan.CartID, "session_id", sessionID, "error", err)

		return errors.Wrap(errors.Join(cause, err, workflow.ErrUncompensated),
			errors.KindInternal, CodePaymentUnderauthorized,
			"the hold of session %s could not be freed", sessionID)
	}
	return cause
}

// Compensate cancels the payment session and frees the hold.
//
// # A NO-OP after the capture
//
// The capture CLOSES the hold (see CapturePayment in the payment module): the
// part that is taken turns into a capture, the part that is not is freed. So if
// the capture happened there is NO hold left to free, and attempting the
// cancellation would only produce errors.Conflict. Reporting that money which
// was taken is not compensated is the capture step's job (see
// [capturePaymentStep.Compensate]); having two steps report the same situation
// would produce nothing but noise.
func (s *authorizePaymentStep) Compensate(ctx context.Context, sc *workflow.StepContext) error {
	skip, err := s.w.skipAfterCapture(ctx, sc, StepAuthorizePayment, s.plan.CartID)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	sessionID, err := sharedText(sc, sharedSessionID)
	if err != nil {
		return err
	}
	if sessionID == "" {
		return nil
	}

	if cancelErr := s.w.payments.Cancel(ctx, sessionID); cancelErr != nil {
		return cancelErr
	}

	s.w.log.InfoContext(ctx, "compensation: payment session canceled",
		"cart_id", s.plan.CartID, "session_id", sessionID)
	return nil
}

// capturePaymentStep captures the amount that was held.
type capturePaymentStep struct {
	w    *Workflows
	plan *checkoutPlan
}

// captureOutput is the capture step's output written to the execution record.
type captureOutput struct {
	// PaymentID is the identifier of the capture that was made.
	PaymentID string `json:"payment_id"`
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
	collectionID, err := sharedText(sc, sharedCollectionID)
	if err != nil {
		return nil, err
	}
	if sessionID == "" || collectionID == "" {
		return nil, errors.Internal(CodeSharedStateInvalid,
			"the capture step could not find the payment session: %s", s.plan.CartID)
	}

	// The flag is set BEFORE the call: EVERY fault that occurs from here on (an
	// error, a panic, a timeout) means "the money may have gone" and the pivot
	// guard is in force. The flag is cleared only on a proven zero capture.
	sc.Shared[sharedCaptureAttempted] = true

	paymentID, err := s.w.payments.Capture(ctx, sessionID, s.plan.Amount)
	if err != nil {
		return nil, s.settle(ctx, sc, collectionID, err)
	}
	if paymentID == "" {
		// The money has been taken but there is no trace of it: not even the
		// refund flow could find it.
		return nil, s.dangling(errors.Internal(CodeEmptyIdentifier,
			"the payment module returned an EMPTY capture identifier (session %s, collection %s)",
			sessionID, collectionID))
	}
	sc.Shared[sharedPaymentID] = paymentID

	_, amount, _, captured, _, err := s.w.payments.Collection(ctx, collectionID)
	if err != nil {
		return nil, s.dangling(errors.Wrap(err, errors.KindOf(err), CodePaymentUndercaptured,
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
	if captured < s.plan.Amount {
		return nil, s.dangling(errors.Conflict(CodePaymentUndercaptured,
			"the amount captured does not cover what must be collected: %d < %d (collection %s)",
			captured, s.plan.Amount, collectionID))
	}

	// If the collection's amount has drifted from the plan this is a separate
	// fault: it means the payment collection was opened with an amount other than
	// the one the saga opened.
	if amount != s.plan.Amount {
		return nil, s.dangling(errors.Internal(CodePaymentUndercaptured,
			"the payment collection's amount has drifted from the plan: collection %d, plan %d (collection %s)",
			amount, s.plan.Amount, collectionID))
	}

	s.w.log.InfoContext(ctx, "payment captured",
		"cart_id", s.plan.CartID, "payment_id", paymentID,
		"captured", captured, "amount", amount)

	return captureOutput{PaymentID: paymentID, Captured: captured}, nil
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

// clearCartStep closes the cart and finalizes the reservations.
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

		if confirmErr := s.w.inventory.ConfirmReservation(ctx, refs[i].ReservationID); confirmErr != nil {
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
