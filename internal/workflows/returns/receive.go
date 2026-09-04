package returns

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// returnDetail is the schema of the order module's return read.
//
// It is repeated here rather than imported: this package cannot import that
// module (ADR 0006), so the two ends agree on a document instead of a type. The
// producing side documents it on order/service.Service.ReturnDetailJSON.
type returnDetail struct {
	ReturnID           string       `json:"return_id"`
	OrderID            string       `json:"order_id"`
	Status             string       `json:"status"`
	ReceivedLocationID string       `json:"received_location_id"`
	Lines              []returnLine `json:"lines"`
}

// returnLine is one line coming back.
type returnLine struct {
	OrderLineItemID string `json:"order_line_item_id"`
	VariantID       string `json:"variant_id"`
	Quantity        int64  `json:"quantity"`
}

// ReceiveResult reports what receiving the return did.
type ReceiveResult struct {
	// ReturnID and OrderID locate the return.
	ReturnID string
	OrderID  string
	// LocationID is where the goods arrived and where the stock went.
	LocationID string
	// RestockedLines is how many lines had their stock put back.
	RestockedLines int
	// RestockedUnits is how many units were put back in total.
	RestockedUnits int64
	// Warnings are the faults that did not stop the receipt.
	//
	// A non-empty list means the record is right and the WAREHOUSE COUNT is
	// not; every entry needs a human.
	Warnings []string
}

// ReceiveReturn records that the returned goods arrived and puts their stock
// back.
//
// # The order of the two halves, and why it is this one
//
// The record is stamped FIRST, then the stock is put back.
//
// The reason is that neither half can undo the other, so the question is only
// which failure is recoverable. A stamped return whose stock was not restored
// leaves a written record naming exactly what is missing and where it should
// have gone — an operator can finish it by hand, and this flow refuses to
// receive the same return twice, so nothing double-counts. The other order
// leaves stock added with nothing saying why: the count is wrong, and the only
// evidence of the goods' arrival was never written.
//
// Between "a record that overstates what was done" and "a warehouse count
// nobody can explain", the first is the one a person can fix.
//
// # Restock failures do NOT fail the call
//
// They are reported as warnings, for the reason the checkout saga reports its
// post-pivot faults: the goods are already in the building. Returning an error
// would tell the operator the receipt did not happen while they are holding the
// parcel, and the record they need to work from would not exist.
//
// # Receiving twice is impossible, and that is what makes restocking safe
//
// [Inventory.Restock] is deliberately not idempotent: two calls mean two
// physical arrivals. This flow is what guarantees one call per receipt — the
// order module's transition table turns a second receive into a NO-OP, so this
// flow checks the status first and stops.
func (w *Workflows) ReceiveReturn(
	ctx context.Context, returnID, locationID string,
) (ReceiveResult, error) {
	if returnID == "" {
		return ReceiveResult{}, errors.Invalid(CodeInvalidInput, "the return id is required")
	}
	if locationID == "" {
		return ReceiveResult{}, errors.Invalid(CodeInvalidInput,
			"the stock location is required; returned goods arrive somewhere and the stock "+
				"has to go there")
	}

	detail, err := w.readReturn(ctx, returnID)
	if err != nil {
		return ReceiveResult{}, err
	}

	// A return that already arrived is NOT received again. The record would
	// treat the second call as a no-op, but the warehouse would not: restocking
	// twice adds goods that came once.
	if detail.Status == statusReceived {
		return ReceiveResult{}, errors.Conflict(CodeInvalidInput,
			"return %s has already been received; restocking it again would add goods that "+
				"arrived once", returnID)
	}

	if err := w.orders.ReceiveReturn(ctx, returnID, locationID); err != nil {
		return ReceiveResult{}, err
	}

	result := ReceiveResult{
		ReturnID:   detail.ReturnID,
		OrderID:    detail.OrderID,
		LocationID: locationID,
	}
	w.restock(ctx, detail, locationID, &result)

	return result, nil
}

// statusReceived is the order module's name for a return whose goods arrived.
const statusReceived = "received"

// readReturn reads the return and its lines.
func (w *Workflows) readReturn(ctx context.Context, returnID string) (returnDetail, error) {
	raw, err := w.orders.ReturnDetailJSON(ctx, returnID)
	if err != nil {
		return returnDetail{}, errors.Wrap(err, errors.KindOf(err), CodeReturnUnreadable,
			"return %s could not be read", returnID)
	}

	var detail returnDetail
	if err := json.Unmarshal(raw, &detail); err != nil {
		return returnDetail{}, errors.Wrap(err, errors.KindInternal, CodeReturnUnreadable,
			"the answer for return %s could not be parsed", returnID)
	}

	return detail, nil
}

// restock puts every line's stock back, collecting faults rather than stopping.
//
// A line that cannot be restocked does not stop the others: they are separate
// products in separate bins, and refusing to put the second one back because
// the first failed would make one fault into two.
func (w *Workflows) restock(
	ctx context.Context, detail returnDetail, locationID string, result *ReceiveResult,
) {
	items, err := w.inventoryItems(ctx, detail.Lines)
	if err != nil {
		w.log.ErrorContext(ctx,
			"the returned lines could not be matched to inventory items; the goods ARRIVED and "+
				"NOTHING was restocked, the warehouse count needs a human",
			"return_id", detail.ReturnID, "order_id", detail.OrderID, "error", err)
		result.Warnings = append(result.Warnings,
			"the returned lines could not be matched to inventory items: "+err.Error())

		return
	}

	for i := range detail.Lines {
		line := detail.Lines[i]

		itemID, known := items[line.VariantID]
		if !known {
			w.log.ErrorContext(ctx,
				"a returned variant has no inventory item; its stock was NOT put back",
				"return_id", detail.ReturnID, "variant_id", line.VariantID,
				"quantity", line.Quantity)
			result.Warnings = append(result.Warnings,
				"variant "+line.VariantID+" has no inventory item; its stock was not put back")

			continue
		}

		if err := w.inventory.Restock(ctx, itemID, locationID, line.Quantity); err != nil {
			w.log.ErrorContext(ctx,
				"the stock of a returned line could not be put back; the goods ARE HERE and "+
					"the count is short",
				"return_id", detail.ReturnID, "variant_id", line.VariantID,
				"inventory_item_id", itemID, "location_id", locationID,
				"quantity", line.Quantity, "error", err)
			result.Warnings = append(result.Warnings,
				"the stock of variant "+line.VariantID+" could not be put back: "+err.Error())

			continue
		}

		result.RestockedLines++
		result.RestockedUnits += line.Quantity
	}
}

// inventoryItems resolves every returned variant to its inventory item in ONE
// query.
//
// A variant with no item is left OUT of the map rather than reported as an
// error: an item can legitimately be missing for a variant that does not track
// stock, and failing the whole receipt for one such line would strand the
// others. The caller warns per line instead.
func (w *Workflows) inventoryItems(
	ctx context.Context, lines []returnLine,
) (map[string]string, error) {
	variantIDs := make([]string, 0, len(lines))
	for i := range lines {
		variantIDs = append(variantIDs, lines[i].VariantID)
	}
	if len(variantIDs) == 0 {
		return map[string]string{}, nil
	}

	linked, err := w.links.ListMany(ctx, LinkVariantInventory, variantIDs)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeNoInventoryItem,
			"the inventory items of %d returned variants could not be read", len(variantIDs))
	}

	out := make(map[string]string, len(linked))
	for variantID, itemIDs := range linked {
		if len(itemIDs) == 0 {
			continue
		}
		// The definition is singular, so more than one item is a data fault
		// rather than a choice to make. Taking the first keeps a broken link
		// from stopping a receipt whose goods are already in the building; the
		// count being wrong is louder than a silent skip.
		out[variantID] = itemIDs[0]
	}

	return out, nil
}
