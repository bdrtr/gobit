package fulfilling

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
)

// CodeDispatchableUnknown reports that what a parcel may hold could not be read.
const CodeDispatchableUnknown = "fulfilling_dispatchable_unknown"

// dispatchableLine is one line of the order module's answer.
type dispatchableLine struct {
	LineItemID string `json:"line_item_id"`
	Bought     int64  `json:"bought"`
	Canceled   int64  `json:"canceled"`
}

// DispatchableQuantities answers, per order line, how many units a NEW parcel may
// still hold, counted the way the fulfillment module holds a parcel to its
// order (ADR 0409).
//
//	dispatchable = ceiling − held = bought − canceled − held
//
// # Who asks
//
// The order module's panel surface draws its open form with it, so the form
// offers each line what the module will take. The module itself asks
// [Workflows.DispatchCeilings] and counts the parcels under the order's lock;
// this answer is the same arithmetic read outside that lock, an offer rather
// than a promise.
//
// # Held is counted by reference, not through the link
//
// What the order's live outgoing parcels hold is the fulfillment module's sum
// over the parcels whose reference is the order, the count its open is checked
// against. A count through the "order_fulfillment" link would miss a parcel
// whose link write failed, or one opened before ADR 0140, and offer units the
// module then refuses.
//
// A line missing from the answer is a line the order does not have. That is a
// meaning rather than an absence, and the caller has to treat it as a refusal:
// otherwise a typo in a line identifier opens a parcel for goods no order sold.
//
// # Returns are not subtracted, and that is deliberate
//
// A returned unit shipped, came back and was restocked when it arrived. Whether it
// ships again is a new decision rather than a quantity still owed, and subtracting
// it would bound a parcel by goods that already left once.
//
// A parcel bringing a return back is not an outgoing one, so it is never held
// (ADR 0384).
func (w *Workflows) DispatchableQuantities(
	ctx context.Context, orderID string, lineItemIDs []string,
) (map[string]int64, error) {
	if w.fulfillments == nil {
		return nil, errors.Internal(CodeDispatchableUnknown,
			"the fulfilling flow is not wired, so what a parcel may hold cannot be read")
	}

	ceilings, err := w.DispatchCeilings(ctx, orderID, lineItemIDs)
	if err != nil {
		return nil, err
	}

	held, err := w.fulfillments.CommittedQuantitiesForReference(ctx, orderID)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeDispatchableUnknown,
			"the units the parcels of order %s hold could not be read", orderID)
	}

	// Clamped at zero rather than reported negative. An order whose parcels
	// already hold more than it may ship is a state this flow did not create and
	// cannot fix, and answering "minus two" would make a caller's comparison
	// pass for a quantity of minus three.
	out := make(map[string]int64, len(ceilings))
	for line, ceiling := range ceilings {
		out[line] = max(ceiling-held[line], 0)
	}

	return out, nil
}

// DispatchCeilings answers, per order line, how many units the order may ship
// at all: what it sold less what was written off, whatever any parcel holds
// (ADR 0409). nil lineItemIDs asks for every line, and a line the order does
// not have is left out, as [Workflows.DispatchableQuantities] leaves it.
//
// # Why the fulfillment module asks for this and not for what is owed
//
// What is owed counts the parcels through the "order_fulfillment" link, which
// the module writes after its own transaction. A parcel that has committed and
// is not linked yet is in the module's tables and not in that count, so holding
// a new parcel to it, under any lock, could over-hold (gap D265). The module
// holds a parcel to this ceiling instead and counts the parcels itself, by the
// reference it stores, under the order's lock: both sides of the comparison are
// then read the same way at the same moment.
func (w *Workflows) DispatchCeilings(
	ctx context.Context, orderID string, lineItemIDs []string,
) (map[string]int64, error) {
	if w.orders == nil {
		return nil, errors.Internal(CodeDispatchableUnknown,
			"the fulfilling flow is not wired, so what an order may ship cannot be read")
	}

	lines, err := w.soldLines(ctx, orderID)
	if err != nil {
		return nil, err
	}

	wanted := make(map[string]struct{}, len(lineItemIDs))
	for _, id := range lineItemIDs {
		wanted[id] = struct{}{}
	}
	out := make(map[string]int64, len(lines))
	for _, line := range lines {
		if len(wanted) > 0 {
			if _, asked := wanted[line.LineItemID]; !asked {
				continue
			}
		}
		out[line.LineItemID] = max(line.Bought-line.Canceled, 0)
	}

	return out, nil
}

// soldLines reads, per line, what the order sold and what was written off.
func (w *Workflows) soldLines(ctx context.Context, orderID string) ([]dispatchableLine, error) {
	raw, err := w.orders.DispatchableLinesJSON(ctx, orderID)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeDispatchableUnknown,
			"the lines of order %s could not be read", orderID)
	}

	var lines []dispatchableLine
	if err := json.Unmarshal(raw, &lines); err != nil {
		return nil, errors.Internal(CodeDispatchableUnknown,
			"the lines of order %s could not be decoded: %v", orderID, err)
	}

	return lines, nil
}
