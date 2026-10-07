package ordercancel

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
)

// topicFulfillmentCanceled is the second event this flow listens to.
//
// The name is REPEATED as a literal for [topicLineCanceled]'s reason: this flow
// and the fulfillment module do not know each other's types, and reaching for a
// constant across that line would tie them together at compile time.
const topicFulfillmentCanceled = "fulfillment.canceled"

// topicFulfillmentReturned is the third: a parcel that came back undelivered
// holds its units only as far as a return or a replacement speaks for them, so
// marking it come back is an input of the same window (ADR 0423). The name is
// repeated for [topicLineCanceled]'s reason.
const topicFulfillmentReturned = "fulfillment.returned"

// fieldFulfillmentID is the parcel the event names; what it held is asked for
// by identity.
const fieldFulfillmentID = "fulfillment_id"

// fieldReference is what the parcel was opened for: its order in every
// flow this repository ships (ADR 0409).
const fieldReference = "reference"

// fieldReturnID is the order return a parcel was bringing back, empty
// for a parcel that went out (ADR 0420).
const fieldReturnID = "return_id"

// linkOrderFulfillment binds an order to the parcels opened for it. The name is
// repeated here for the topic's reason.
const linkOrderFulfillment = "order_fulfillment"

// HandleFulfillmentCanceled puts back the stock a canceled parcel was holding.
//
// # What was wrong
//
// ADR 0135 refused to withdraw a shipment when a line is written off under it —
// a framework must not decide what a shop has to — and it named the shop's own
// resolution in the same sentence: cancel the parcel. Canceling it flipped a
// status and did nothing else. The units that parcel held had been counted as
// GONE by the line cancellation, so they never went back on the shelf; once the
// parcel was canceled they were not in a box either. They were neither sold, nor
// shipped, nor stock. The prescribed cure lost the goods (gap D75).
//
// # Why it is the same flow and not a new one
//
// This package's subject is the stock of units that were written off, and which
// of them can come back is one arithmetic with two inputs: what the order wrote
// off, and what a parcel still holds. A second flow would own half of that and
// the halves would drift.
//
// # A missing piece is not an error, for [Workflow.HandleLineCanceled]'s reason
//
// A parcel whose order has no cancellation releases nothing and returns nil. What
// DOES return an error is a fault that may pass — a module unreachable, an
// inventory write failing — because those the bus tries again, twice within a
// second and a quarter (ADR 0240).
func (w *Workflow) HandleFulfillmentCanceled(ctx context.Context, e eventbus.Event) error {
	return w.recountParcel(ctx, e)
}

// HandleFulfillmentReturned puts back the written-off units of a parcel that
// came back to the sender undelivered (ADR 0423).
//
// A line written off while its parcel was on the way put nothing back: the
// units had left, and the window [targetOnShelf] computes was empty. Once the
// parcel is marked come back it holds its units only as far as a return or a
// replacement speaks for them, so the window opens, and this recounts it as
// [Workflow.HandleFulfillmentCanceled] does for a canceled parcel: the same
// target under the order's dispatch lock, brought up to and never past, so a
// redelivery or a write-off arriving after it puts nothing back twice. A parcel
// bringing a return back holds none of the order's outgoing units, and its
// coming back puts nothing back, as its cancel does not.
func (w *Workflow) HandleFulfillmentReturned(ctx context.Context, e eventbus.Event) error {
	return w.recountParcel(ctx, e)
}

// recountParcel brings each line a parcel held up to its shelf target, after
// the parcel was canceled or came back: both lower what the order's parcels
// hold, and the target is computed from the state the act finds.
func (w *Workflow) recountParcel(ctx context.Context, e eventbus.Event) error {
	fulfillmentID := text(e, fieldFulfillmentID)
	if fulfillmentID == "" {
		return errors.Invalid(CodeEventUnusable,
			"the %q event carries no %s", e.Name, fieldFulfillmentID)
	}
	if w.links == nil || w.fulfillment == nil || w.orders == nil || w.inventory == nil {
		return errors.Internal(CodeNotReady,
			"the cancellation flow is not wired, so a parcel's units cannot be acted on")
	}

	// The orders are read from the LINK first. A parcel is bound to MORE than one
	// order since ADR 0197: the order it was opened for and the additions that
	// joined it. Its items are the first order's lines, but the link does not say
	// which order that is, so each line is put back against the bound order that
	// HAS it.
	//
	// When the link names none, an OUTGOING parcel's reference is its order:
	// the fulfillment module holds every outgoing parcel to the order its
	// reference names and counts an order's parcels by it (ADR 0409, ADR 0420),
	// so a parcel whose link write failed (D264) releases its units as a linked
	// one does. A reference no order answers to releases nothing. A parcel
	// bringing a return back is bound to no order and holds none of its
	// outgoing units (ADR 0384), so its cancel releases nothing either; the
	// event names its return.
	orderIDs, err := w.ordersOfParcel(ctx, fulfillmentID)
	if err != nil {
		return err
	}
	byReference := false
	if len(orderIDs) == 0 {
		reference := text(e, fieldReference)
		if reference == "" || text(e, fieldReturnID) != "" {
			return nil
		}
		orderIDs, byReference = []string{reference}, true
	}

	held, err := w.fulfillment.QuantitiesOfFulfillment(ctx, fulfillmentID)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), CodeEventUnusable,
			"what parcel %s was holding could not be read", fulfillmentID)
	}
	if len(held) == 0 {
		// A parcel with no item breakdown — the shape the checkout opens for a
		// whole order — releases nothing this flow can attribute to a line.
		return nil
	}

	owners, err := w.lineOwners(ctx, orderIDs)
	if err != nil {
		if byReference && errors.KindOf(err) == errors.KindNotFound {
			w.log.DebugContext(ctx, "a parcel's reference names no order, so nothing was put back",
				"event", e.Name, "fulfillment_id", fulfillmentID, "reference", orderIDs[0])

			return nil
		}

		return err
	}

	for lineItemID, releasedByParcel := range held {
		owner, onAnOrder := owners[lineItemID]
		if !onAnOrder {
			// A parcel holding a line the order does not have is a state ADR 0135
			// now refuses to create. An older one can exist, and nothing here can
			// decide what it means.
			w.log.WarnContext(ctx,
				"a parcel held a line no bound order has, so nothing was put back",
				"event", e.Name, "fulfillment_id", fulfillmentID, "order_ids", orderIDs,
				"order_line_item_id", lineItemID)

			continue
		}
		orderID, line := owner.orderID, owner.line

		// What the order's parcels hold AFTER this one was canceled or came
		// back. The module's own answer already excludes a canceled parcel and
		// holds one that came back only as far as a return or a replacement
		// speaks for it (ADR 0423), which is what makes the arithmetic below a
		// difference between two states rather than a subtraction this flow
		// has to remember.
		committedAfter, err := owner.committed(ctx)
		if err != nil {
			return err
		}

		// The SAME invariant the write-off computes, from the state this act
		// finds — the parcel is already excluded from committedAfter, so the
		// window is the one that holds now.
		target := targetOnShelf(line.Bought, line.Canceled, committedAfter[lineItemID])
		if target == 0 {
			continue
		}

		// A backordered line's claim gives up what the released units no longer
		// hold, and says what its stock never lost (ADR 0392).
		owed, settleErr := w.settle(ctx, orderID, lineItemID, line.Bought, target)
		if owed == nil {
			return settleErr
		}
		if err := errors.Join(w.putBack(ctx, line.stockParts(), lineItemID, target,
			parcelReleaseReference(fulfillmentID, lineItemID), owed), settleErr); err != nil {
			return err
		}

		w.log.InfoContext(ctx,
			"a parcel canceled or come back let a line's written-off units reach the shelf",
			"event", e.Name, "fulfillment_id", fulfillmentID, "order_id", orderID,
			"order_line_item_id", lineItemID, "target", target,
			"released_by_this_parcel", releasedByParcel)
	}

	return nil
}

// parcelReleaseReference names the act in the ledger.
//
// It is the PARCEL and the LINE rather than a cancellation id, because this act
// is not a cancellation: several write-offs can share one parcel and one parcel
// can release several lines.
//
// It is no longer what makes a redelivery harmless — that is the target (ADR
// 0142) — and it no longer has to be unique, because the same act writes twice
// when its target grows. What it is for is an operator reading the ledger and
// asking which act put these units back.
func parcelReleaseReference(fulfillmentID, lineItemID string) string {
	return fulfillmentID + ":" + lineItemID
}

// orderLine is one line of the order, as this flow needs it.
type orderLine struct {
	LineItemID string `json:"line_item_id"`
	Bought     int64  `json:"bought"`
	Canceled   int64  `json:"canceled"`
	// SpokenFor is how many of the line's units a return or a replacement
	// speaks for (ADR 0423).
	SpokenFor int64  `json:"spoken_for"`
	VariantID string `json:"variant_id"`
	// Components are what one unit of a bundle line held when it was sold
	// (ADR 0235); empty for any other line.
	Components []lineComponent `json:"components,omitempty"`
}

// lineComponent is one component of a bundle line, as the order answers it.
type lineComponent struct {
	VariantID string `json:"variant_id"`
	Quantity  int64  `json:"quantity"`
}

// stockParts is what the line's units are made of: its own variant, or its
// components.
func (l orderLine) stockParts() []stockPart {
	if len(l.Components) == 0 {
		return []stockPart{{variantID: l.VariantID, perUnit: 1}}
	}
	parts := make([]stockPart, 0, len(l.Components))
	for _, c := range l.Components {
		parts = append(parts, stockPart{variantID: c.VariantID, perUnit: c.Quantity})
	}
	return parts
}

// orderLines reads what the order sold and what it wrote off, per line.
//
// The schema is the one the order module's DispatchableLinesJSON writes, and it
// is repeated here because the two packages cannot import each other — the type
// it marshals is unexported, so there is not even a name to link to. That the two
// agree is provable only by a test that runs both, which is why one exists.
func (w *Workflow) orderLines(ctx context.Context, orderID string) (map[string]orderLine, error) {
	raw, err := w.orders.DispatchableLinesJSON(ctx, orderID)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeEventUnusable,
			"the lines of order %s could not be read", orderID)
	}

	var lines []orderLine
	if err := json.Unmarshal(raw, &lines); err != nil {
		return nil, errors.Internal(CodeEventUnusable,
			"the lines of order %s could not be decoded: %v", orderID, err)
	}

	out := make(map[string]orderLine, len(lines))
	for _, line := range lines {
		out[line.LineItemID] = line
	}

	return out, nil
}

// spokenOf is, per line, how many units a return or a replacement speaks for:
// the figure a parcel that came back undelivered holds its units to (ADR 0423).
func spokenOf(lines map[string]orderLine) map[string]int64 {
	spoken := make(map[string]int64, len(lines))
	for id, line := range lines {
		if line.SpokenFor > 0 {
			spoken[id] = line.SpokenFor
		}
	}

	return spoken
}

// ordersOfParcel answers which orders a parcel is bound to: the one it was
// opened for and the additions that joined it (ADR 0197).
//
// None is not a fault: a parcel bound to no order has nothing to put back
// against, because nothing wrote its units off.
func (w *Workflow) ordersOfParcel(ctx context.Context, fulfillmentID string) ([]string, error) {
	byParcel, err := w.links.ListManyByTo(ctx, linkOrderFulfillment, []string{fulfillmentID})
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeEventUnusable,
			"the orders of parcel %s could not be read", fulfillmentID)
	}

	orders := byParcel[fulfillmentID]
	if len(orders) == 0 {
		w.log.DebugContext(ctx, "a parcel is bound to no order",
			"fulfillment_id", fulfillmentID)
	}

	return orders, nil
}

// lineOwner is the bound order a parcel's line belongs to, with the line.
type lineOwner struct {
	orderID string
	line    orderLine
	// committed reads the order's live parcels once and remembers them, so a
	// parcel of many lines of one order costs one read of them.
	committed func(ctx context.Context) (map[string]int64, error)
}

// lineOwners maps every line of the bound orders to the order that has it.
//
// A line id belongs to one order, so the first order that has it is the one.
// Each order's live parcels are read lazily and once.
func (w *Workflow) lineOwners(ctx context.Context, orderIDs []string) (map[string]lineOwner, error) {
	owners := map[string]lineOwner{}
	for _, orderID := range orderIDs {
		lines, err := w.orderLines(ctx, orderID)
		if err != nil {
			return nil, err
		}

		var (
			cached map[string]int64
			read   bool
		)
		spoken := spokenOf(lines)
		committed := func(ctx context.Context) (map[string]int64, error) {
			if read {
				return cached, nil
			}
			out, err := w.committedQuantities(ctx, orderID, spoken)
			if err != nil {
				return nil, err
			}
			cached, read = out, true

			return cached, nil
		}

		for lineItemID, line := range lines {
			if _, taken := owners[lineItemID]; !taken {
				owners[lineItemID] = lineOwner{orderID: orderID, line: line, committed: committed}
			}
		}
	}

	return owners, nil
}

// committedQuantities sums, per line, the units the order's LIVE parcels hold.
//
// It is [Workflow.committedQuantity] widened to the whole order: the line
// cancellation asks about one line and this asks about every line the canceled
// parcel held, and issuing one query per line would be a round trip per item.
func (w *Workflow) committedQuantities(
	ctx context.Context, orderID string, spoken map[string]int64,
) (map[string]int64, error) {
	committed, err := w.heldUnderLock(ctx, orderID, spoken)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeEventUnusable,
			"the units still in the parcels of order %s could not be read", orderID)
	}

	return committed, nil
}

// putBack writes the units onto the shelf they left from.
//
// It is the tail [Workflow.HandleLineCanceled] runs too, lifted out so the two
// acts cannot disagree about which shelf or about what a repeated delivery means.
//
// The window is in the LINE's units and each part is brought up to it times
// its units per line unit (ADR 0235): a written-off gift box puts back one
// towel and two soaps. The inventory module keeps each part's total per line
// AND item, so the parts of one line are separate targets and a redelivery
// finds each already there. A part that tracks no stock is skipped as a line
// that tracks none is; an error stops the act and the bus delivers it again,
// which the parts already back answer as done.
//
// A backordered line's part is credited only the units its stock lost, at the
// warehouse its claim was filled from (ADR 0392, [shelfTarget]); a part whose
// claim never filled lost none.
func (w *Workflow) putBack(
	ctx context.Context, parts []stockPart, lineItemID string, window int64, reference string,
	owed *lineOwed,
) error {
	for _, part := range parts {
		itemID, tracked, err := w.itemOf(ctx, part.variantID)
		if err != nil {
			return err
		}
		if !tracked {
			continue
		}

		partTarget := shelfTarget(window, part.perUnit, owed.undeducted[itemID])
		if partTarget == 0 {
			w.log.DebugContext(ctx, "none of the part's written-off units left its shelf",
				"reference", reference, "order_line_item_id", lineItemID, "inventory_item_id", itemID)

			continue
		}

		locationID, found := owed.filledAt[itemID]
		if !found {
			locationID, found = owed.soldAt[itemID]
		}
		if !found {
			// A checkout that failed before its last step leaves reservations
			// and no sale, and its compensation released them: nothing was
			// deducted, and no number of retries makes a sale appear.
			w.log.WarnContext(ctx,
				"a canceled line's stock cannot be put back: the ledger holds no sale for it, "+
					"so either the checkout never deducted it or the movement was written "+
					"without an order",
				"reference", reference, "order_line_item_id", lineItemID, "inventory_item_id", itemID)

			continue
		}

		alreadyBack, err := w.inventory.ReturnCanceled(
			ctx, itemID, locationID, lineItemID, partTarget, reference)
		if err != nil {
			return errors.Wrap(err, errors.KindOf(err), CodeEventUnusable,
				"the shelf could not be brought up to %d of item %s for %s", partTarget, itemID, reference)
		}
		if alreadyBack {
			// A second delivery, or the other act having got there first:
			// nothing is owed and nothing was written.
			w.log.DebugContext(ctx, "the line was already at its target on the shelf",
				"reference", reference, "inventory_item_id", itemID, "target", partTarget)

			continue
		}

		w.log.InfoContext(ctx, "the stock of a canceled line was brought up to its target",
			"reference", reference, "order_line_item_id", lineItemID,
			"inventory_item_id", itemID, "location_id", locationID, "target", partTarget)
	}

	return nil
}
