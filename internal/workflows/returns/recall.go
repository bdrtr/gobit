package returns

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
)

// topicFulfillmentCanceled is the event the fulfillment module publishes when a
// parcel is canceled. The name is repeated as a literal for the reason the
// cancellation flow repeats it: the two sides do not know each other's types.
const topicFulfillmentCanceled = "fulfillment.canceled"

// fieldFulfillmentID is the one key this flow reads off that event.
const fieldFulfillmentID = "fulfillment_id"

// RecallResult reports what recalling a replacement did.
type RecallResult struct {
	// ReplacementID is the replacement the parcel carried; empty when it
	// carried none, or no longer carries one.
	ReplacementID string
	// RecalledPromises is how many promises had their units put back by this
	// call; a promise already back is not counted.
	RecalledPromises int
}

// HandleFulfillmentCanceled recalls the replacement a canceled parcel carried
// (ADR 0239).
//
// Every canceled parcel is heard, a sale's too; one that carried no replacement
// is left to the cancellation flow, which puts a sale's written-off units back.
// The error a recall returns is logged by the bus and not retried, as every
// handler's is; the log line here names the parcel and the replacement.
func (w *Workflows) HandleFulfillmentCanceled(ctx context.Context, e eventbus.Event) error {
	fulfillmentID, _ := e.Data[fieldFulfillmentID].(string)
	if fulfillmentID == "" {
		return errors.Invalid(CodeInvalidInput, "the %q event carries no %s", e.Name, fieldFulfillmentID)
	}

	out, err := w.RecallReplacement(ctx, fulfillmentID)
	if err != nil {
		w.log.ErrorContext(ctx,
			"a replacement's parcel was canceled and the replacement could not be recalled; its units "+
				"may be off the shelf and the record still says they left",
			"fulfillment_id", fulfillmentID, "replacement_id", out.ReplacementID, "error", err)

		return err
	}
	if out.ReplacementID != "" {
		w.log.InfoContext(ctx, "a canceled parcel recalled its replacement",
			"fulfillment_id", fulfillmentID, "replacement_id", out.ReplacementID,
			"recalled_promises", out.RecalledPromises)
	}

	return nil
}

// RecallReplacement puts back the units of the replacement a canceled parcel
// carried and sends the record back to 'requested' (ADR 0239).
//
// # The units go back BEFORE the record is written
//
// The order of ADR 0237's withdrawal, for its reason: the step that touches
// stock is the one that can fail on something outside the record, and a record
// written first would say the goods are waiting while their units are still
// counted as gone. Both steps repeat safely: a promise already recalled writes
// nothing, and the record is recalled only while it is dispatched in THIS
// parcel, so a late or repeated event for a parcel the replacement has since
// left does nothing.
func (w *Workflows) RecallReplacement(ctx context.Context, fulfillmentID string) (RecallResult, error) {
	replacementID, err := w.orders.ReplacementOfParcel(ctx, fulfillmentID)
	if err != nil {
		return RecallResult{}, errors.Wrap(err, errors.KindOf(err), CodeReplacementUnreadable,
			"the replacement parcel %s carried could not be found", fulfillmentID)
	}
	if replacementID == "" {
		return RecallResult{}, nil
	}

	detail, err := w.readReplacement(ctx, replacementID)
	if err != nil {
		return RecallResult{ReplacementID: replacementID}, err
	}
	if detail.Status != statusReplacementDispatched || detail.FulfillmentID != fulfillmentID {
		return RecallResult{}, nil
	}

	out := RecallResult{ReplacementID: replacementID}
	for i := range detail.Lines {
		line := detail.Lines[i]
		for _, promise := range line.promises() {
			if promise.reservationID == "" {
				continue
			}
			alreadyBack, err := w.inventory.RecallReplacement(ctx, promise.reservationID)
			if err != nil {
				return out, errors.Wrap(err, errors.KindOf(err), CodeStockNotReleased,
					"the units of %s on line %s could not be put back; the replacement is still "+
						"recorded as sent", promise.variantID, line.ReplacementItemID)
			}
			if !alreadyBack {
				out.RecalledPromises++
			}
		}
	}

	if err := w.orders.RecallReplacement(ctx, replacementID, fulfillmentID); err != nil {
		return out, err
	}

	return out, nil
}
