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

	// The order is read from the LINK rather than from the event's reference
	// field. The fulfillment module never validates that field and says so in its
	// own record, so reading it as an order identifier would be reading a
	// convention; the link is the binding this repository trusts (ADR 0134).
	orderID, found, err := w.orderOfParcel(ctx, fulfillmentID)
	if err != nil || !found {
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

	lines, err := w.orderLines(ctx, orderID)
	if err != nil {
		return err
	}

	// The live parcels AFTER this one was canceled. The canceled parcel is
	// already excluded by the module's own answer, which is what makes the
	// arithmetic below a difference between two states rather than a subtraction
	// this flow has to remember.
	committedAfter, err := w.committedQuantities(ctx, orderID)
	if err != nil {
		return err
	}

	for lineItemID, releasedByParcel := range held {
		line, onTheOrder := lines[lineItemID]
		if !onTheOrder {
			// A parcel holding a line the order does not have is a state ADR 0135
			// now refuses to create. An older one can exist, and nothing here can
			// decide what it means.
			w.log.WarnContext(ctx,
				"a canceled parcel held a line the order does not have, so nothing was put back",
				"fulfillment_id", fulfillmentID, "order_id", orderID,
				"order_line_item_id", lineItemID)

			continue
		}

		quantity := releasedUnits(
			line.Bought, line.Canceled, committedAfter[lineItemID], releasedByParcel)
		if quantity == 0 {
			continue
		}

		if err := w.putBack(ctx, orderID, line.VariantID, quantity,
			parcelReleaseReference(fulfillmentID, lineItemID)); err != nil {
			return err
		}

		w.log.InfoContext(ctx,
			"a canceled parcel released units a write-off had counted as gone",
			"fulfillment_id", fulfillmentID, "order_id", orderID,
			"order_line_item_id", lineItemID, "quantity", quantity)
	}

	return nil
}

// releasedUnits is how many units THIS parcel's cancellation puts back.
//
// # The arithmetic, and why it is a difference of two windows
//
// [returnableUnits] says the total that may ever go back for a line is
// `min(canceledTotal, bought − committed)`. Both sides of that move: a line
// cancellation grows `canceledTotal`, and a parcel cancellation shrinks
// `committed`. So the amount owed to the shelf after this parcel went away is
//
//	after  = min(canceledTotal, bought − committedAfter)
//
// and what was already owed while the parcel still counted is
//
//	before = min(canceledTotal, bought − committedAfter − heldByThisParcel)
//
// The difference is what this act releases, and it is a difference of STATES
// rather than of records — so it needs no memory of what previous acts returned,
// exactly like the line cancellation's own formula. Two parcels canceled in
// either order put back the same total, and a redelivered event computes the same
// number and writes it under the same reference, which the ledger refuses twice.
//
// A line nobody wrote off gives `canceledTotal = 0` and releases nothing: those
// units are still sold, and a parcel going away makes them dispatchable again
// rather than sellable again.
func releasedUnits(bought, canceledTotal, committedAfter, heldByThisParcel int64) int64 {
	// There is no early return for "nothing was canceled" or "the parcel held
	// nothing", and the absence is deliberate: a mutation that deleted such a
	// guard survived every test, because the two windows are equal in both cases
	// and the difference is already zero. A guard nothing can make fail is a guard
	// that says the arithmetic does not cover a case it covers.
	after := windowOwed(bought, canceledTotal, committedAfter)
	before := windowOwed(bought, canceledTotal, committedAfter+heldByThisParcel)
	if after <= before {
		return 0
	}

	return after - before
}

// windowOwed is how many of a line's canceled units belong on the shelf while
// `committed` of them are in live parcels.
//
// Clamped at zero on both ends: a line whose parcels hold more than it sold is a
// state this flow did not create, and a negative window would make the difference
// above report units that do not exist.
func windowOwed(bought, canceledTotal, committed int64) int64 {
	window := bought - committed
	if window <= 0 {
		return 0
	}

	return min(canceledTotal, window)
}

// parcelReleaseReference is the ledger reference of one line's release.
//
// It is the PARCEL and the LINE rather than a cancellation id, because this act
// is not a cancellation: several write-offs can share one parcel and one parcel
// can release several lines. The pair is what happens once, so it is what the
// ledger holds unique — a redelivered event writes nothing, which is the
// guarantee the bus's at-least-once delivery requires.
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

// orderOfParcel answers which order a parcel was opened for.
//
// found is false for a parcel bound to no order. That is not a fault: the binding
// is a Module Link written by the flow that opens the parcel, and one opened
// straight through the admin endpoint has none — there is nothing to put back
// against, because nothing wrote the units off.
func (w *Workflow) orderOfParcel(
	ctx context.Context, fulfillmentID string,
) (orderID string, found bool, err error) {
	byParcel, err := w.links.ListManyByTo(ctx, linkOrderFulfillment, []string{fulfillmentID})
	if err != nil {
		return "", false, errors.Wrap(err, errors.KindOf(err), CodeEventUnusable,
			"the order of parcel %s could not be read", fulfillmentID)
	}

	orders := byParcel[fulfillmentID]
	if len(orders) == 0 {
		w.log.DebugContext(ctx, "a canceled parcel is bound to no order",
			"fulfillment_id", fulfillmentID)

		return "", false, nil
	}

	return orders[0], true, nil
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
	ctx context.Context, orderID, variantID string, quantity int64, reference string,
) error {
	itemID, locationID, found, err := w.shelf(ctx, orderID, variantID)
	if err != nil || !found {
		return err
	}

	alreadyBack, err := w.inventory.ReturnCanceled(ctx, itemID, locationID, quantity, reference)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), CodeEventUnusable,
			"the released stock of %s could not be put back", reference)
	}
	if alreadyBack {
		w.log.DebugContext(ctx, "the released units were already put back",
			"reference", reference)
	}

	return nil
}
