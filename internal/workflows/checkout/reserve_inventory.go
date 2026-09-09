package checkout

// This file holds the saga's FIRST step: reserving the stock.
//
// The step's quartet (Name/Restore/Invoke/Compensate) stands here together with
// the private helpers only it calls — choosing the location, reserving the
// line, ranking the candidates, unwinding a half-finished reservation — and
// with [reservationRef], the trace a reservation leaves behind. The steps do
// not call one another; what all five DO share stays in steps.go.

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
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

// reserveInventoryStep reserves stock for every line of the cart.
type reserveInventoryStep struct {
	w    *Workflows
	plan *checkoutPlan
	// served are the warehouses the order's sales channels ship from, resolved
	// ONCE at the start of the step.
	//
	// It is empty when the plan names no channel, and when the channels it
	// names are bound to no warehouse — the two are the same answer here and
	// deliberately so: neither is a restriction. Resolving it per line would
	// ask the link service the same question once per line for a set that
	// cannot change inside one step.
	served map[string]bool
}

// reserveOutput is the inventory step's output written to the execution record.
type reserveOutput struct {
	// Reservations are the reservations that were taken.
	Reservations []reservationRef `json:"reservations"`
	// Unreserved names the lines that finished the step with NO reservation,
	// deliberately: an uncounted variant, or one permitting backorder that no
	// warehouse could cover (ADR 0048).
	//
	// It is written so that the RECORD, and not the plan, says which of the two
	// an empty reservation list is: a legitimate outcome or a lost trail. The
	// plan cannot answer that — a backorder-permitting line may equally well
	// have been covered and reserved — so a record that named no reservation for
	// such a cart used to be indistinguishable from one whose identifiers went
	// missing (see [reserveInventoryStep.Restore]).
	//
	// It is also the only place an operator can read WHICH line went out
	// unreserved; a reservation that was never taken leaves no
	// [reservationRef] to look for.
	Unreserved []string `json:"unreserved,omitempty"`
}

// Name returns the step's name.
func (s *reserveInventoryStep) Name() string { return StepReserveInventory }

// Restore rebuilds the reservations that were taken FROM THE RECORD.
//
// The only thing compensation needs is the reservation identifiers, and they are
// already durable in the step's output: [reserveOutput]. That is why the stock
// of an abandoned execution can be released even after the process has died.
//
// # The record has to ACCOUNT FOR EVERY LINE
//
// The step leaves exactly one of two traces per line: a reservation, or the
// line's id under [reserveOutput.Unreserved]. So a whole record satisfies
// len(Reservations) + len(Unreserved) == len(plan.Lines), and anything else
// means the record lost something. Quietly putting an empty slice in its place
// would make compensation claim "done" without having found the stock it is
// meant to release.
//
// A record written before ADR 0048 satisfies the same identity without carrying
// the new key: that step reserved every line of the plan and skipped none, so
// its reservation count IS the line count and the absent field decodes as an
// empty list.
//
// Since ADR 0048 an EMPTY reservation list is a legitimate outcome on its own.
// A line the merchant does not count takes no reservation, and neither does a
// backorder-permitting line that no warehouse could cover; a cart made only of
// those finishes the step having reserved nothing, and refusing that would make
// an ordinary order unrecoverable.
//
// The PLAN cannot tell those apart from a lost trail, which is why the
// accounting is read out of the record instead. A line that permits backorder
// is reserved whenever stock exists, so a plan of such lines is consistent with
// both "reserved nothing, correctly" and "reserved two and lost both"; a guard
// that asked the plan alone let the second one through. What the plan is still
// asked for is its LENGTH — the number of lines the step had to answer for.
func (s *reserveInventoryStep) Restore(sc *workflow.StepContext, output json.RawMessage) error {
	var out reserveOutput
	if err := json.Unmarshal(output, &out); err != nil {
		return errors.Wrap(err, errors.KindInternal, CodeSharedStateInvalid,
			"the output of step %q could not be decoded", StepReserveInventory)
	}
	if len(out.Reservations)+len(out.Unreserved) != len(s.plan.Lines) {
		return errors.Internal(CodeSharedStateInvalid,
			"the record of step %q accounts for %d of the plan's %d lines (%d reserved, %d deliberately not); compensation cannot know what to release",
			StepReserveInventory, len(out.Reservations)+len(out.Unreserved), len(s.plan.Lines),
			len(out.Reservations), len(out.Unreserved))
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
// behind" belongs to the step (see the internal/core/workflow package comment). If the
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
//
// # Two lines are NOT reserved, and each answers one flag (ADR 0048)
//
// A line the merchant does not count ([planLine.Unmanaged]) is skipped before
// any module is asked: there is no stock to set aside, so there is nothing to
// reserve, nothing to release and no warehouse to choose. That is the flag ADR
// 0040 already spends at the storefront badge, read here so the till and the
// badge answer the same question.
//
// A line that permits backorder ([planLine.AllowBackorder]) is skipped only
// AFTER the reservation was attempted and no warehouse could cover it. The
// distinction matters: stock that EXISTS is still reserved, and only the
// refusal is lifted. What it does NOT do is take a level negative or promise a
// date — the inventory module has neither, and pre-order is a decision of its
// own.
//
// The one backorder line that is skipped WITHOUT asking is the one carrying no
// inventory item at all. Nothing counts its stock, so no warehouse can ever
// cover it and there is no item to ask about; [Workflows.inventoryItems] left
// it empty for exactly that reason and [checkoutPlan.validate] has already
// refused the other reading of an empty item — a counted line that does NOT
// permit backorder. Asking the inventory module about an empty identifier would
// put a made-up question to another module to reach the same answer.
//
// # Every line leaves a trace in the OUTPUT, reservation or not
//
// A skipped line's id goes into [reserveOutput.Unreserved]. The record then
// accounts for every line of the plan, which is what lets
// [reserveInventoryStep.Restore] tell "reserved nothing, legitimately" from
// "lost the identifiers", and what lets an operator see which line went out
// with no stock behind it.
//
// Only errors.Conflict is forgiven, because that is the class that means "no
// warehouse can cover this line" ([Inventory.Reserve] and
// [reserveInventoryStep.locationFor] both use it). A database that cannot be
// reached or a fulfillment module breaking its contract answers the same way at
// every warehouse and still drops the order; forgiving those would turn an
// outage into silently unreserved orders.
func (s *reserveInventoryStep) Invoke(ctx context.Context, sc *workflow.StepContext) (any, error) {
	served, err := s.w.locationsServingChannels(ctx, s.plan.SalesChannelIDs)
	if err != nil {
		return nil, err
	}
	if err := s.checkDeclaredLocation(served); err != nil {
		return nil, err
	}
	s.served = served

	refs := make([]reservationRef, 0, len(s.plan.Lines))
	unreserved := make([]string, 0, len(s.plan.Lines))

	for i := range s.plan.Lines {
		line := s.plan.Lines[i]

		if line.Unmanaged {
			s.w.log.DebugContext(ctx, "the variant is not counted; the line takes no reservation",
				"cart_id", s.plan.CartID, "line_item_id", line.LineItemID,
				"variant_id", line.VariantID)
			unreserved = append(unreserved, line.LineItemID)
			continue
		}
		if line.AllowBackorder && line.InventoryItemID == "" {
			// Nothing counts this variant's stock, so the answer is the one an
			// exhausted warehouse gives, arrived at without a question.
			//
			// The condition names the FLAG as well as the empty item, although
			// the plan already refuses the other reading of an empty item (see
			// checkoutPlan.validate). A counted line that refuses backorder
			// must keep blowing up loudly
			// if it ever reaches here, rather than being skipped by a guard that
			// no longer looks at why the item is missing.
			s.w.log.InfoContext(ctx, "the variant is counted but linked to no inventory item; backorder is permitted so the order stands",
				"cart_id", s.plan.CartID, "line_item_id", line.LineItemID,
				"variant_id", line.VariantID, "quantity", line.Quantity)
			unreserved = append(unreserved, line.LineItemID)
			continue
		}

		locationID, reservationID, err := s.reserveLine(ctx, line)
		if err != nil {
			if line.AllowBackorder && errors.IsConflict(err) {
				// The order is NOT refused and the line takes no reservation.
				// It is logged at INFO rather than DEBUG because an operator
				// answering "where does this line ship from" needs to see that
				// the answer is "from stock the shop does not have yet"; the
				// record names the line as unreserved but cannot say which
				// warehouse it will one day come from.
				s.w.log.InfoContext(ctx, "no warehouse can cover the line; backorder is permitted so the order stands",
					"cart_id", s.plan.CartID, "line_item_id", line.LineItemID,
					"variant_id", line.VariantID, "inventory_item_id", line.InventoryItemID,
					"quantity", line.Quantity, "error", err)
				unreserved = append(unreserved, line.LineItemID)
				continue
			}
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
		"cart_id", s.plan.CartID, "lines", len(refs), "unreserved", len(unreserved))
	return reserveOutput{Reservations: refs, Unreserved: unreserved}, nil
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

	// The channel's warehouses are applied AFTER the stock question and before
	// the fulfillment module's preference, which is where they belong: which
	// warehouses HOLD the units is a fact, which of them this channel may ship
	// from is the merchant's rule, and only then does the ordering matter.
	kept := s.withinChannel(candidates)
	if len(kept) == 0 {
		return nil, errors.Conflict(CodeChannelHasNoStock,
			"no warehouse serving this order's sales channel can reserve %d of item %s; "+
				"%d warehouse(s) hold the units and none of them ships for the channel",
			line.Quantity, line.InventoryItemID, len(candidates))
	}

	return kept, nil
}

// withinChannel keeps the candidates the order's channels are served by.
//
// With no restriction in force every candidate is kept, which is what makes an
// installation that has bound nothing behave exactly as it did before the
// binding existed.
func (s *reserveInventoryStep) withinChannel(candidates []string) []string {
	if len(s.served) == 0 {
		return candidates
	}

	kept := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if s.served[candidate] {
			kept = append(kept, candidate)
		}
	}

	return kept
}

// checkDeclaredLocation refuses a named warehouse the channel is not served by.
//
// A declared location is an INSTRUCTION rather than a preference — that is why
// [reserveInventoryStep.locationFor] asks no module when one is given — and an
// instruction that contradicts the merchant's own binding is a conflict rather
// than something to silently correct. Answering it by choosing another
// warehouse would mean the flow deciding where an administrative order ships
// from; answering it by obeying would make the binding a suggestion.
func (s *reserveInventoryStep) checkDeclaredLocation(served map[string]bool) error {
	if s.plan.LocationID == "" || len(served) == 0 || served[s.plan.LocationID] {
		return nil
	}

	return errors.Conflict(CodeLocationOutsideChannel,
		"location %s does not ship for this order's sales channel; the order names a "+
			"warehouse the channel is not served by", s.plan.LocationID)
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

// locationsServingChannels resolves the warehouses the given channels ship
// from.
//
// # Why an empty answer is not an error
//
// Two different situations produce it and neither is a fault: the caller named
// no channel (an administrative order, and the flow behaves as it did before
// the binding existed), or the channels it named are bound to no warehouse.
// The second is the merchant's "I have not configured this", and reading it as
// "this channel ships from nowhere" would refuse every order on every
// installation the day the binding shipped.
//
// # Why a link failure DOES fail the order
//
// The set is what narrows the reservation. A read that failed and was treated
// as "no restriction" would place the order from a warehouse the channel may
// not serve, which is exactly what the binding exists to prevent — and the
// merchant would have no way to see that the rule had been skipped.
func (w *Workflows) locationsServingChannels(
	ctx context.Context, channelIDs []string,
) (map[string]bool, error) {
	if len(channelIDs) == 0 {
		return nil, nil
	}

	bound, err := w.links.ListManyByTo(ctx, LinkLocationSalesChannel, channelIDs)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeChannelLocationsUnreadable,
			"the warehouses serving the order's sales channels could not be read")
	}

	served := map[string]bool{}
	for _, locationIDs := range bound {
		for _, locationID := range locationIDs {
			served[locationID] = true
		}
	}

	return served, nil
}
