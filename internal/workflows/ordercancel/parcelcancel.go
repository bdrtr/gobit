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

// fieldFulfillmentID is the only payload key this flow reads off the parcel
// event; everything else about the parcel is asked for by identity.
const fieldFulfillmentID = "fulfillment_id"

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
// inventory write failing — because those the bus should try again.
func (w *Workflow) HandleFulfillmentCanceled(ctx context.Context, e eventbus.Event) error {
	fulfillmentID := text(e, fieldFulfillmentID)
	if fulfillmentID == "" {
		return errors.Invalid(CodeEventUnusable,
			"the %q event carries no %s", e.Name, fieldFulfillmentID)
	}
	if w.links == nil || w.fulfillment == nil || w.orders == nil || w.inventory == nil {
		return errors.Internal(CodeNotReady,
			"the cancellation flow is not wired, so a canceled parcel cannot be acted on")
	}

	// The orders are read from the LINK rather than from the event's reference
	// field. The fulfillment module never validates that field and says so in its
	// own record, so reading it as an order identifier would be reading a
	// convention; the link is the binding this repository trusts (ADR 0134).
	//
	// A parcel is bound to MORE than one order since ADR 0197: the order it was
	// opened for and the additions that joined it. Its items are the first
	// order's lines, but the link does not say which order that is, so each
	// line is put back against the bound order that HAS it.
	orderIDs, err := w.ordersOfParcel(ctx, fulfillmentID)
	if err != nil || len(orderIDs) == 0 {
		return err
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
		return err
	}

	for lineItemID, releasedByParcel := range held {
		owner, onAnOrder := owners[lineItemID]
		if !onAnOrder {
			// A parcel holding a line the order does not have is a state ADR 0135
			// now refuses to create. An older one can exist, and nothing here can
			// decide what it means.
			w.log.WarnContext(ctx,
				"a canceled parcel held a line no bound order has, so nothing was put back",
				"fulfillment_id", fulfillmentID, "order_ids", orderIDs,
				"order_line_item_id", lineItemID)

			continue
		}
		orderID, line := owner.orderID, owner.line

		// The live parcels AFTER this one was canceled. The canceled parcel is
		// already excluded by the module's own answer, which is what makes the
		// arithmetic below a difference between two states rather than a
		// subtraction this flow has to remember.
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

		if err := w.putBack(ctx, orderID, line.VariantID, lineItemID, target,
			parcelReleaseReference(fulfillmentID, lineItemID)); err != nil {
			return err
		}

		w.log.InfoContext(ctx,
			"a canceled parcel let a line's written-off units reach the shelf",
			"fulfillment_id", fulfillmentID, "order_id", orderID,
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
	VariantID  string `json:"variant_id"`
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
		w.log.DebugContext(ctx, "a canceled parcel is bound to no order",
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
		committed := func(ctx context.Context) (map[string]int64, error) {
			if read {
				return cached, nil
			}
			out, err := w.committedQuantities(ctx, orderID)
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
	ctx context.Context, orderID string,
) (map[string]int64, error) {
	byOrder, err := w.links.ListMany(ctx, linkOrderFulfillment, []string{orderID})
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeEventUnusable,
			"the parcels of order %s could not be read", orderID)
	}

	parcels := byOrder[orderID]
	if len(parcels) == 0 {
		return map[string]int64{}, nil
	}

	committed, err := w.fulfillment.CommittedQuantities(ctx, parcels)
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
func (w *Workflow) putBack(
	ctx context.Context, orderID, variantID, lineItemID string, target int64, reference string,
) error {
	itemID, locationID, found, err := w.shelf(ctx, orderID, variantID)
	if err != nil || !found {
		return err
	}

	alreadyBack, err := w.inventory.ReturnCanceled(
		ctx, itemID, locationID, lineItemID, target, reference)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), CodeEventUnusable,
			"the shelf could not be brought up to %d for %s", target, reference)
	}
	if alreadyBack {
		w.log.DebugContext(ctx, "the line was already at its target on the shelf",
			"reference", reference, "target", target)
	}

	return nil
}
