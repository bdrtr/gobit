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
// still hold.
//
// # Why this is a flow's answer and not a module's
//
// It is three records in two modules. What was sold and what was written off are
// the order's; what is already in a live parcel is the fulfillment module's; and
// which parcels belong to the order is a Module Link, which neither module reads
// on the other's behalf. The fulfillment module's create endpoint is where the
// answer is NEEDED, and it resolves this flow to get it — the same shape the cart
// module uses for pricing, where the endpoint stays on the module and the
// cross-module decision lives above it (ADR 0135).
//
//	dispatchable = bought − canceled − committed
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
func (w *Workflows) DispatchableQuantities(
	ctx context.Context, orderID string, lineItemIDs []string,
) (map[string]int64, error) {
	if w.orders == nil || w.fulfillments == nil || w.links == nil {
		return nil, errors.Internal(CodeDispatchableUnknown,
			"the fulfilling flow is not wired, so what a parcel may hold cannot be read")
	}

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

	committed, err := w.committedUnits(ctx, orderID)
	if err != nil {
		return nil, err
	}

	// Only the lines the caller ASKED about. Answering for the whole order would
	// make the map grow with the order and say nothing more: the caller is opening
	// one parcel and what it needs is the bound for the lines in it.
	//
	// A line the order does not have is left OUT, which is the only signal the
	// caller can read as "not on this order" — an answer of zero would be
	// indistinguishable from a line that is fully shipped.
	wanted := make(map[string]struct{}, len(lineItemIDs))
	for _, id := range lineItemIDs {
		wanted[id] = struct{}{}
	}

	out := make(map[string]int64, len(lineItemIDs))
	for _, line := range lines {
		if len(wanted) > 0 {
			if _, asked := wanted[line.LineItemID]; !asked {
				continue
			}
		}
		// Clamped at zero rather than reported negative. An order whose parcels
		// already hold more than it sold is a state this flow did not create and
		// cannot fix, and answering "minus two" would make a caller's comparison
		// pass for a quantity of minus three.
		remaining := line.Bought - line.Canceled - committed[line.LineItemID]
		if remaining < 0 {
			remaining = 0
		}
		out[line.LineItemID] = remaining
	}

	return out, nil
}

// committedUnits sums, per line, the units the order's live parcels hold.
//
// The parcels come from the "order_fulfillment" LINK rather than from the
// shipment's free-text reference: the module never validates that field and its
// own record says reading it as the order is reading a convention.
func (w *Workflows) committedUnits(ctx context.Context, orderID string) (map[string]int64, error) {
	byOrder, err := w.links.ListMany(ctx, LinkOrderFulfillment, []string{orderID})
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeDispatchableUnknown,
			"the parcels of order %s could not be read", orderID)
	}

	parcels := byOrder[orderID]
	if len(parcels) == 0 {
		return map[string]int64{}, nil
	}

	committed, err := w.fulfillments.CommittedQuantities(ctx, parcels)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeDispatchableUnknown,
			"the units already in the parcels of order %s could not be read", orderID)
	}

	return committed, nil
}
