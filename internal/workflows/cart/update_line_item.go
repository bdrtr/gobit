package cart

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
)

// UpdateLineItemInput is the input of a line quantity update.
type UpdateLineItemInput struct {
	// CartID is the cart the line belongs to; it is REQUIRED.
	CartID string
	// LineItemID is the line to be updated; it is REQUIRED.
	LineItemID string
	// Quantity is the line's NEW quantity (an absolute value, not one to add).
	//
	// If zero is given the line is REMOVED; a negative value is rejected. The
	// rationale is in the [Workflows.UpdateLineItem] godoc.
	Quantity int64
}

// UpdateLineItemResult is the result of the update and of the recalculated
// totals.
type UpdateLineItemResult struct {
	// LineItemID is the line that was updated (or removed).
	LineItemID string
	// Quantity is the line's new quantity; it is zero if the line was removed.
	Quantity int64
	// Removed reports whether the line was removed.
	Removed bool
	// Totals are the cart totals after the update.
	Totals Totals
}

// UpdateLineItem writes the line's quantity (or removes the line) and
// recalculates the totals.
//
// # A quantity of zero REMOVES the line
//
// The decision is deliberate and DOES NOT CONTRADICT the cart module's
// decision; it completes it. The module's UpdateLineItemQuantity method rejects
// zero, because that place is a SETTER writing an absolute value and it is
// unacceptable for a programming error that accidentally sends zero into the
// quantity field to silently delete data. This flow, on the other hand, is the
// storefront's intent layer: in every cart interface, dropping the quantity
// picker to zero means "remove this".
//
// That is why the intent is translated EXPLICITLY here — when zero is seen a
// separate removal call is made, the module's rule is not relaxed, and the
// result is REPORTED to the caller with [UpdateLineItemResult.Removed]. The
// alternative was every storefront writing this branch itself; each one would
// have forgotten the "recalculate the totals after removing" part in a
// different way.
//
// A negative quantity is rejected (errors.Invalid): a negative quantity has no
// intent whatsoever, and rounding it to zero would make a request carrying a
// sign error delete a line.
//
// # The sales channel scope is asked again when the quantity RISES
//
// The scope check is at the entry gate ([Workflows.AddLineItem]): a variant that
// does not appear in the identity's channels can NEVER enter the cart. A raise
// asks for more units of a line, and that is an entry of more units, so it asks
// the gate's question again (ADR 0281): a line whose product was moved out of
// the request's channels after it entered the cart is refused a higher
// quantity with the same 404 an add gets, and nothing is written.
//
// Lowering the quantity, removing the line and completing the cart as it is ask
// nothing: the cart is a SNAPSHOT, and an administrator's catalog edit must not
// make a customer's full cart impossible to pay for. What it can stop is the
// cart asking for more of what the shop no longer sells there.
//
// # If the totals calculation blows up
//
// The quantity HAS BEEN WRITTEN and is not taken back; the error is wrapped with
// the [CodeTotalsAfterChange] code. The rationale is the same as
// [Workflows.AddLineItem]'s.
func (w *Workflows) UpdateLineItem(ctx context.Context, in UpdateLineItemInput) (UpdateLineItemResult, error) {
	if err := requireID("cart_id", in.CartID); err != nil {
		return UpdateLineItemResult{}, err
	}
	if err := requireID("line_item_id", in.LineItemID); err != nil {
		return UpdateLineItemResult{}, err
	}
	if in.Quantity < 0 {
		return UpdateLineItemResult{}, errors.Invalid(CodeInvalidInput,
			"the quantity cannot be negative: %d (give 0 to remove the line)", in.Quantity)
	}
	if in.Quantity > MaxQuantity {
		return UpdateLineItemResult{}, errors.Invalid(CodeInvalidInput,
			"the quantity can be at most %d: %d", MaxQuantity, in.Quantity)
	}

	removed := in.Quantity == 0
	if !removed {
		if err := w.mayRaise(ctx, in); err != nil {
			return UpdateLineItemResult{}, err
		}
	}
	var err error
	if removed {
		err = w.carts.RemoveLineItem(ctx, in.CartID, in.LineItemID)
	} else {
		err = w.carts.SetCartLineItemQuantity(ctx, in.CartID, in.LineItemID, in.Quantity)
	}
	if err != nil {
		return UpdateLineItemResult{}, err
	}

	what := "line quantity updated"
	if removed {
		what = "line removed"
	}

	totals, err := w.CalculateTotals(ctx, in.CartID)
	if err != nil {
		return UpdateLineItemResult{}, totalsAfterChange(err, in.CartID, what)
	}

	return UpdateLineItemResult{
		LineItemID: in.LineItemID,
		Quantity:   in.Quantity,
		Removed:    removed,
		Totals:     totals,
	}, nil
}

// mayRaise asks the entry gate's question again when the quantity rises
// (ADR 0281); a quantity that stays or falls asks nothing. A line the cart does
// not hold is left to the write, which refuses it as not found.
func (w *Workflows) mayRaise(ctx context.Context, in UpdateLineItemInput) error {
	snap, err := w.snapshot(ctx, in.CartID)
	if err != nil {
		return err
	}
	for i := range snap.Items {
		item := &snap.Items[i]
		if item.ID != in.LineItemID {
			continue
		}
		if in.Quantity <= item.Quantity {
			return nil
		}
		_, err := w.variantTitle(ctx, item.VariantID)

		return err
	}

	return nil
}
