package cart

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
)

// CodeCouponNotUsable is the answer to a coupon code the discount round cannot
// use.
//
// It does NOT say why. The code may not exist, its promotion may be a draft or
// paused, its campaign may be over or its budget spent — and the promotion
// module deliberately gives one answer to all of them, because "this code exists
// but its campaign has not started" would hand a code guesser a campaign
// calendar. This flow keeps that decision rather than unpacking it.
const CodeCouponNotUsable = "cart_coupon_not_usable"

// ApplyPromotionCode writes a coupon code onto the cart and reprices it.
//
// # The order: ASK, then write, then reprice
//
// The code is checked against the promotion module BEFORE it is written. The
// reverse order — write, reprice, then look at what the round could not match —
// would leave a cart holding an unusable code for as long as the round takes,
// and would have to unwrite it afterwards; the failure of that unwrite would
// leave the shopper with a coupon nothing will ever honor.
//
// # What "usable" means here, and what it does not
//
// It means the code names a promotion a customer may use. It does NOT mean the
// coupon discounts THIS cart: a valid coupon whose target matches no line is not
// an invalid code, only one that did nothing today, and it may start working
// when the shopper adds another item. That is the promotion module's own rule
// (see its unmatchedCodes) and this flow does not add a second one.
//
// # Why the totals are recomputed here
//
// Because the coupon changes what the cart costs, and the cart's written total
// is what the completion saga charges. Leaving the repricing to the client would
// let a cart be completed at the amount it had before the coupon.
func (w *Workflows) ApplyPromotionCode(ctx context.Context, cartID, code string) error {
	if w.discounts == nil {
		// The installation did not register the promotion module. Writing the
		// code anyway would put a coupon on the cart that nothing can ever
		// honor, and answering "applied" would be a lie the shopper only finds
		// out about at the till.
		return errors.Internal(CodeCouponNotUsable,
			"the promotion module is not installed; a coupon code cannot be applied")
	}
	if err := w.discounts.CouponApplies(ctx, code); err != nil {
		if errors.IsNotFound(err) {
			return errors.Wrap(err, errors.KindInvalid, CodeCouponNotUsable,
				"the coupon code cannot be applied: %s", code)
		}

		return err
	}

	if err := w.carts.AddCartPromotionCode(ctx, cartID, code); err != nil {
		return err
	}
	_, err := w.CalculateTotals(ctx, cartID)

	return err
}

// RemovePromotionCode takes a coupon code off the cart and reprices it.
//
// It asks the promotion module NOTHING. A code that has stopped being usable
// still has to be removable — otherwise a shopper whose coupon expired while the
// page was open could not get it off the cart.
func (w *Workflows) RemovePromotionCode(ctx context.Context, cartID, code string) error {
	if err := w.carts.RemoveCartPromotionCode(ctx, cartID, code); err != nil {
		return err
	}
	_, err := w.CalculateTotals(ctx, cartID)

	return err
}
