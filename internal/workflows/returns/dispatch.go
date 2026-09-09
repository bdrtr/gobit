package returns

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
)

// replacementDetail is the schema of the order module's replacement read.
//
// It is repeated here rather than imported, for [returnDetail]'s reason: this
// package cannot import that module (ADR 0006), so the two ends agree on a
// document. The producing side documents it on
// order/service.Service.ReplacementDetailJSON.
type replacementDetail struct {
	ReplacementID    string            `json:"replacement_id"`
	ClaimID          string            `json:"claim_id"`
	ClaimStatus      string            `json:"claim_status"`
	OrderID          string            `json:"order_id"`
	Status           string            `json:"status"`
	ShippingOptionID string            `json:"shipping_option_id"`
	LocationID       string            `json:"location_id"`
	FulfillmentID    string            `json:"fulfillment_id"`
	Lines            []replacementLine `json:"lines"`
}

// replacementLine is one line being sent.
type replacementLine struct {
	ReplacementItemID string `json:"replacement_item_id"`
	OrderLineItemID   string `json:"order_line_item_id"`
	VariantID         string `json:"variant_id"`
	Quantity          int64  `json:"quantity"`
	ReservationID     string `json:"reservation_id"`
}

// The order module's names for the states this flow reads.
const (
	// statusReplacementRequested is a replacement waiting to be sent.
	statusReplacementRequested = "requested"
	// statusReplacementDispatched is one whose goods have left.
	statusReplacementDispatched = "dispatched"
)

// DispatchResult reports what sending the replacement did.
type DispatchResult struct {
	// ReplacementID, ClaimID and OrderID locate the record.
	ReplacementID string
	ClaimID       string
	OrderID       string
	// FulfillmentID is the parcel the goods left in.
	FulfillmentID string
	// SentUnits is how many units left the warehouse.
	SentUnits int64
	// AlreadySent reports that the replacement had already been dispatched and
	// nothing moved this time.
	//
	// It is REPORTED rather than inferred, for the reason the fulfilling flow
	// reports AlreadyOpen: an operator who pressed the button twice has to be
	// told the second press sent nothing.
	AlreadySent bool
}

// DispatchReplacement sends what a claim promised: it sets the units aside,
// opens a parcel, takes the units out of the count and records all three.
//
// # The order of the steps, and why it is this one
//
// Set aside, open the parcel, confirm. Each step leaves a record the NEXT
// attempt reads, so a dispatch that dies anywhere is finished by running it
// again rather than by undoing what it did:
//
//  1. The promise is written on the replacement's line. A second attempt reuses
//     it instead of setting the same units aside twice.
//  2. The parcel is opened with an idempotency key derived from the
//     replacement's id, so the same replacement resolves to the same parcel.
//  3. The confirm is idempotent in the inventory module, so re-confirming a
//     promise whose units already left does nothing.
//
// The confirm comes AFTER the parcel because it is the irreversible half: a
// confirmed reservation cannot be released, while a promise that no parcel ever
// carried is released by hand or expires with the record. Between "units held
// for goods that never shipped" and "units gone with nothing carrying them",
// the first is the one a person can fix.
//
// # The claim is settled last
//
// Completing the claim is what says the matter is closed, and it is true only
// once the goods are on their way. A dispatch that died before it reads the
// claim as still open on the next attempt — which is why the record carries the
// claim's status rather than making this flow guess.
func (w *Workflows) DispatchReplacement(
	ctx context.Context, replacementID string,
) (DispatchResult, error) {
	if replacementID == "" {
		return DispatchResult{}, errors.Invalid(CodeInvalidInput,
			"the replacement id is required")
	}

	detail, err := w.readReplacement(ctx, replacementID)
	if err != nil {
		return DispatchResult{}, err
	}

	result := DispatchResult{
		ReplacementID: detail.ReplacementID,
		ClaimID:       detail.ClaimID,
		OrderID:       detail.OrderID,
		FulfillmentID: detail.FulfillmentID,
	}

	if detail.Status == statusReplacementDispatched {
		// Everything below has already happened; the claim may not have. The
		// settle is repeated rather than skipped, because the attempt that
		// dispatched may be exactly the one that died before it.
		result.AlreadySent = true

		return result, w.settleDispatchedClaim(ctx, detail)
	}
	if detail.Status != statusReplacementRequested {
		return DispatchResult{}, errors.Conflict(CodeReplacementNotOpen,
			"replacement %s is %s; only one that is waiting to be sent can be dispatched",
			replacementID, detail.Status)
	}
	if len(detail.Lines) == 0 {
		return DispatchResult{}, errors.Conflict(CodeInvalidInput,
			"replacement %s names no lines, so there is nothing to send", replacementID)
	}

	held, err := w.holdStock(ctx, detail)
	if err != nil {
		return DispatchResult{}, err
	}

	fulfillmentID, _, err := w.openParcel(ctx, detail)
	if err != nil {
		return DispatchResult{}, err
	}
	result.FulfillmentID = fulfillmentID

	for i := range held {
		if err := w.inventory.ConfirmReservation(ctx, held[i].reservationID); err != nil {
			return DispatchResult{}, errors.Wrap(err, errors.KindOf(err), CodeStockNotTaken,
				"the units of line %s could not be taken out of the count; parcel %s is open "+
					"and the replacement is NOT recorded as sent",
				held[i].orderLineItemID, fulfillmentID)
		}
		result.SentUnits += held[i].quantity
	}

	if err := w.orders.MarkReplacementDispatched(ctx, replacementID, fulfillmentID); err != nil {
		return DispatchResult{}, err
	}
	if err := w.settleDispatchedClaim(ctx, detail); err != nil {
		return DispatchResult{}, err
	}

	return result, nil
}

// heldLine is one line whose units are set aside.
type heldLine struct {
	orderLineItemID string
	reservationID   string
	quantity        int64
}

// holdStock sets aside the units of every line and records the promises.
//
// A line that already names one is left alone: that is the retry reading what
// the earlier attempt wrote. A promise that is made but cannot be RECORDED is
// released again before the error is returned — it is the one failure here that
// would otherwise hold stock nothing could name.
func (w *Workflows) holdStock(
	ctx context.Context, detail replacementDetail,
) ([]heldLine, error) {
	variantIDs := make([]string, 0, len(detail.Lines))
	for i := range detail.Lines {
		variantIDs = append(variantIDs, detail.Lines[i].VariantID)
	}

	items, err := w.inventoryItems(ctx, variantIDs)
	if err != nil {
		return nil, err
	}

	held := make([]heldLine, 0, len(detail.Lines))
	for i := range detail.Lines {
		line := detail.Lines[i]

		if line.ReservationID != "" {
			held = append(held, heldLine{
				orderLineItemID: line.OrderLineItemID,
				reservationID:   line.ReservationID,
				quantity:        line.Quantity,
			})

			continue
		}

		itemID, tracked := items[line.VariantID]
		if !tracked {
			// Receiving warns and carries on here; dispatching cannot. Goods
			// with no inventory item are goods no warehouse can be asked for,
			// and sending them would deduct nothing while the parcel claims
			// they left.
			return nil, errors.Conflict(CodeNoInventoryItem,
				"variant %s has no inventory item, so line %s cannot be sent",
				line.VariantID, line.OrderLineItemID)
		}

		reservationID, err := w.inventory.ReserveForReplacement(
			ctx, itemID, detail.LocationID, line.Quantity, line.OrderLineItemID)
		if err != nil {
			return nil, errors.Wrap(err, errors.KindOf(err), CodeStockNotHeld,
				"the units of line %s could not be set aside at location %s",
				line.OrderLineItemID, detail.LocationID)
		}

		if err := w.orders.RecordReplacementReservation(
			ctx, detail.ReplacementID, line.ReplacementItemID, reservationID); err != nil {
			w.releaseHeldStock(ctx, detail, reservationID)

			return nil, errors.Wrap(err, errors.KindOf(err), CodeStockNotHeld,
				"the units of line %s were set aside and the record of that could not be "+
					"written; the promise was released again",
				line.OrderLineItemID)
		}

		held = append(held, heldLine{
			orderLineItemID: line.OrderLineItemID,
			reservationID:   reservationID,
			quantity:        line.Quantity,
		})
	}

	return held, nil
}

// releaseHeldStock gives back a promise nothing will be able to name.
//
// A release that fails is LOGGED and not returned: the caller is already
// returning the failure that made this necessary, and replacing it with the
// second one would hide the first.
func (w *Workflows) releaseHeldStock(
	ctx context.Context, detail replacementDetail, reservationID string,
) {
	if err := w.inventory.ReleaseReservation(ctx, reservationID); err != nil {
		w.log.ErrorContext(ctx,
			"units were set aside for a replacement, could not be recorded and could not be "+
				"released; the stock is held by a promise nothing names",
			"replacement_id", detail.ReplacementID, "reservation_id", reservationID,
			"location_id", detail.LocationID, "error", err)
	}
}

// openParcel opens the shipment the goods leave in.
//
// The idempotency key is DERIVED from the replacement's id rather than stored
// beside it: the row is the key (migration 000011), so a retry names the same
// parcel without a column that could disagree with the record.
func (w *Workflows) openParcel(
	ctx context.Context, detail replacementDetail,
) (fulfillmentID string, alreadyOpen bool, err error) {
	request, err := json.Marshal(map[string]string{
		"shipping_option_id": detail.ShippingOptionID,
		"idempotency_key":    "replacement-" + detail.ReplacementID,
	})
	if err != nil {
		return "", false, errors.Internal(CodeParcelNotOpened,
			"the shipment request for replacement %s could not be built", detail.ReplacementID)
	}

	fulfillmentID, alreadyOpen, err = w.shipping.OpenForOrder(ctx, detail.OrderID, request)
	if err != nil {
		return "", false, errors.Wrap(err, errors.KindOf(err), CodeParcelNotOpened,
			"no parcel could be opened for replacement %s, so nothing was sent",
			detail.ReplacementID)
	}

	return fulfillmentID, alreadyOpen, nil
}

// settleDispatchedClaim closes the claim the goods answered.
//
// A claim that is no longer open is left alone rather than refused: this runs
// on the retry path too, and the attempt that completed it may be the one that
// died immediately afterwards.
func (w *Workflows) settleDispatchedClaim(ctx context.Context, detail replacementDetail) error {
	if detail.ClaimStatus != statusRequested {
		return nil
	}

	if err := w.orders.CompleteClaim(ctx, detail.ClaimID); err != nil {
		return errors.Wrap(err, errors.KindOf(err), CodeInvalidInput,
			"the goods of replacement %s left and claim %s could not be marked as settled",
			detail.ReplacementID, detail.ClaimID)
	}

	return nil
}

// readReplacement reads the replacement, its lines and its claim's status.
func (w *Workflows) readReplacement(
	ctx context.Context, replacementID string,
) (replacementDetail, error) {
	raw, err := w.orders.ReplacementDetailJSON(ctx, replacementID)
	if err != nil {
		return replacementDetail{}, errors.Wrap(err, errors.KindOf(err),
			CodeReplacementUnreadable, "replacement %s could not be read", replacementID)
	}

	var detail replacementDetail
	if err := json.Unmarshal(raw, &detail); err != nil {
		return replacementDetail{}, errors.Wrap(err, errors.KindInternal,
			CodeReplacementUnreadable,
			"the answer for replacement %s could not be parsed", replacementID)
	}

	return detail, nil
}
