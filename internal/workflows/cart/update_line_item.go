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
// # The sales channel scope is NOT asked again here
//
// The scope check is at the entry gate ([Workflows.AddLineItem]): a variant that
// does not appear in the identity's channels can NEVER enter the cart. This
// method cannot slip a new variant in, it only writes the quantity of a line
// ALREADY sitting in the cart.
//
// That has a measured consequence and it is not being hidden: if a product is
// moved to another channel from the admin end AFTER it entered the cart, the
// customer can keep increasing that line's quantity and completing the cart.
// ADDING the same variant again, however, is rejected (404) — the entry gate is
// closed.
//
// This is not a hole, it is the consequence of the cart being a SNAPSHOT, and it
// is deliberate: the alternative is an administrator's catalog edit making
// customers' full carts impossible to pay for. There is also nothing an attacker
// gains from it — for the line to be able to enter the cart it MUST HAVE been in
// scope at that moment, and the party doing the move is not an attacker but the
// operator. An installation that wants the scope enforced continuously
// throughout the line's lifetime may choose to put the scope check on the
// completion step as well; the price paid then is the sentence above.
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
