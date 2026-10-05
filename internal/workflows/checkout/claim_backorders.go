package checkout

// This file holds what the last step does for a line the stock step let through
// without stock: it records the line as a claim the inventory module fills from
// the next units that arrive where the order may ship from (ADR 0392).

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// orderLineAnswer is one line of the order as the order module answers it. The
// schema is the one the order module's DispatchableLinesJSON writes, repeated
// here because the two packages cannot import each other.
type orderLineAnswer struct {
	LineItemID string `json:"line_item_id"`
	Bought     int64  `json:"bought"`
	Canceled   int64  `json:"canceled"`
	VariantID  string `json:"variant_id"`
}

// owedUnits is one backordered line's units of one item.
type owedUnits struct {
	cartLine string
	itemID   string
	quantity int64
}

// claimBackorders records a claim for every counted line, and every counted part
// of a bundle line, the stock step let through without stock, and settles each
// against the order's write-offs and live parcels read after the claim. It
// returns the warnings for the checkout's answer.
//
// It runs AFTER the pivot, so nothing here fails the order: a claim that could
// not be recorded is a warning, and the line is then owed what ADR 0048 owed it
// — nothing — which the warning and the gap ledger say (D242).
//
// # Why before the confirm
//
// A write-off is published only after it commits, and the settlement below
// reads the write-offs after the claim exists, so a line written off before the
// checkout finished is withdrawn either here or by the cancellation flow, which
// finds the claim. Putting the claims before the confirm also means that
// whenever a sale of the order exists, its claims exist too, which is why the
// cancellation flow reads the order's sales before it settles a line.
//
// # The order line is found by POSITION
//
// The order keeps the cart's lines in the order the snapshot sent them (ADR
// 0233), and an order line carries no cart line, so the k-th order line is the
// cart line at position k of [checkoutPlan.lineOrder]. The position is checked
// against the variant and the quantity, and an answer that does not follow the
// cart records no claim at all.
func (s *clearCartStep) claimBackorders(ctx context.Context, sc *workflow.StepContext, orderID string) []string {
	owed := s.backorderedUnits(sc)
	if len(owed) == 0 {
		return nil
	}

	var warnings []string
	warn := func(format string, a ...any) {
		message := fmt.Sprintf(format, a...)
		s.w.log.ErrorContext(ctx, "a backordered line is not owed its units; the order is VALID, manual repair is required",
			"cart_id", s.plan.CartID, "order_id", orderID, "reason", message)
		warnings = append(warnings, message)
	}

	lines, err := s.orderLines(ctx, orderID)
	if err != nil {
		warn("the order's lines could not be read; no backorder was recorded: %v", err)
		return warnings
	}
	order := s.plan.lineOrder()
	byCartLine := make(map[string]orderLineAnswer, len(order))
	if len(lines) < len(order) {
		warn("the order's lines do not follow the cart's; no backorder was recorded")
		return warnings
	}
	for k, i := range order {
		if lines[k].VariantID != s.plan.Lines[i].VariantID || lines[k].Bought != s.plan.Lines[i].Quantity {
			warn("the order's lines do not follow the cart's; no backorder was recorded")
			return warnings
		}
		byCartLine[s.plan.Lines[i].LineItemID] = lines[k]
	}

	locations, err := s.backorderLocations(ctx)
	if err != nil {
		warn("the warehouses a backordered line may be filled from could not be ranked; "+
			"its claim names none and nothing will fill it: %v", err)
		locations = []string{}
	}

	var claimed []string
	for _, unit := range owed {
		line := byCartLine[unit.cartLine]
		if _, err := s.w.inventory.ClaimBackorder(ctx, unit.itemID, orderID, line.LineItemID,
			unit.quantity, locations); err != nil {
			warn("the backorder of line %s could not be recorded: %v", line.LineItemID, err)
			continue
		}
		if !slices.Contains(claimed, line.LineItemID) {
			claimed = append(claimed, line.LineItemID)
		}
	}
	if len(claimed) == 0 {
		return warnings
	}

	// The write-offs are read AFTER the claims: one made before the claim
	// existed published an event the cancellation flow could not settle.
	after, err := s.orderLines(ctx, orderID)
	if err != nil {
		warn("the order's write-offs could not be read after the backorders were recorded: %v", err)
		return warnings
	}
	// So are the parcels: the order exists from the pivot, an operator may open
	// one before this step runs, and a recovery may run it much later.
	committed, err := s.committedQuantities(ctx, orderID)
	if err != nil {
		warn("the order's parcels could not be read after the backorders were recorded: %v", err)
		return warnings
	}
	for _, line := range after {
		if !slices.Contains(claimed, line.LineItemID) {
			continue
		}
		window := unitsThatWillNotLeave(line.Bought, line.Canceled, committed[line.LineItemID])
		if _, _, err := s.w.inventory.SettleBackorder(ctx, line.LineItemID, line.Bought, window); err != nil {
			warn("the backorder of line %s could not be settled: %v", line.LineItemID, err)
		}
	}

	return warnings
}

// unitsThatWillNotLeave is the cancellation flow's target (ADR 0142): of the
// units no live parcel holds, the ones written off. Units a live parcel holds
// leave when they arrive, so their claim keeps them.
func unitsThatWillNotLeave(bought, canceled, committed int64) int64 {
	return max(0, min(canceled, bought-committed))
}

// committedQuantities answers, per order line, the units the order's live
// parcels hold. The parcels are found through the "order_fulfillment" link, as
// the cancellation flow finds them.
func (s *clearCartStep) committedQuantities(ctx context.Context, orderID string) (map[string]int64, error) {
	byOrder, err := s.w.links.ListMany(ctx, LinkOrderFulfillment, []string{orderID})
	if err != nil {
		return nil, err
	}
	parcels := byOrder[orderID]
	if len(parcels) == 0 {
		return map[string]int64{}, nil
	}

	return s.w.fulfillment.CommittedQuantities(ctx, parcels)
}

// backorderedUnits collects, per cart line and item, the counted units the
// stock step let through without stock. A line the merchant does not count and
// one linked to no item are not owed anything a warehouse can hold; a bundle
// that holds one item through two parts owes the sum.
func (s *clearCartStep) backorderedUnits(sc *workflow.StepContext) []owedUnits {
	raw, ok := sc.Shared[sharedUnreserved]
	if !ok {
		return nil
	}
	unreserved, ok := raw.(unreservedRef)
	if !ok {
		return nil
	}

	var out []owedUnits
	add := func(cartLine, itemID string, quantity int64) {
		for i := range out {
			if out[i].cartLine == cartLine && out[i].itemID == itemID {
				out[i].quantity += quantity
				return
			}
		}
		out = append(out, owedUnits{cartLine: cartLine, itemID: itemID, quantity: quantity})
	}

	units := s.plan.reservationUnits()
	for i := range units {
		unit := &units[i]
		line := &unit.line
		if line.Unmanaged || line.InventoryItemID == "" {
			continue
		}
		skipped := false
		if unit.component == "" {
			skipped = slices.Contains(unreserved.Lines, line.LineItemID)
		} else {
			skipped = slices.Contains(unreserved.Components,
				componentRef{LineItemID: line.LineItemID, VariantID: unit.component})
		}
		if skipped {
			add(line.LineItemID, line.InventoryItemID, line.Quantity)
		}
	}

	return out
}

// orderLines reads the order's lines.
func (s *clearCartStep) orderLines(ctx context.Context, orderID string) ([]orderLineAnswer, error) {
	raw, err := s.w.orders.DispatchableLinesJSON(ctx, orderID)
	if err != nil {
		return nil, err
	}
	var lines []orderLineAnswer
	if err := json.Unmarshal(raw, &lines); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeSharedStateInvalid,
			"the lines of order %s could not be decoded", orderID)
	}

	return lines, nil
}

// backorderLocations ranks the warehouses a backordered line may be filled from:
// the declared one, or every open warehouse the order's channels are served by,
// in the order fulfillment ships this order's region from.
//
// The channel's warehouses are resolved again rather than carried from the
// stock step: a recovery that runs this step again has no stock step in memory.
// A failure here cannot fail the order, so it names no warehouse instead.
func (s *clearCartStep) backorderLocations(ctx context.Context) ([]string, error) {
	if s.plan.LocationID != "" {
		return []string{s.plan.LocationID}, nil
	}

	served, err := s.w.locationsServingChannels(ctx, s.plan.SalesChannelIDs)
	if err != nil {
		return nil, err
	}
	open, err := s.w.inventory.OpenLocations(ctx)
	if err != nil {
		return nil, err
	}
	candidates := withinChannel(served, open)
	if len(candidates) == 0 {
		return nil, errors.Conflict(CodeChannelHasNoStock,
			"no open warehouse serves this order's sales channel")
	}

	ranked, err := s.w.fulfillment.RankLocations(ctx, s.plan.RegionID, candidates)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ranked))
	for _, id := range ranked {
		if slices.Contains(candidates, id) && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return nil, errors.Internal(CodeReservationFailed,
			"the fulfillment module ranked none of the %d open warehouses", len(candidates))
	}

	return out, nil
}
