package repository

import (
	"context"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/repository/cartdb"
)

// AddPromotionCode writes a coupon code onto the cart.
//
// A code the cart already holds is absorbed by the query's ON CONFLICT: typing
// the same code twice is a double press and not a second coupon, so it is not
// an error the storefront has to explain.
func (r *Repository) AddPromotionCode(ctx context.Context, cartID, code string) error {
	if err := r.queries(ctx).AddCartPromotionCode(ctx, cartdb.AddCartPromotionCodeParams{
		CartID: cartID, Code: code,
	}); err != nil {
		return classify(err, codeQueryFailed, "the coupon code could not be written")
	}

	return nil
}

// ListPromotionCodes returns the cart's coupon codes in the order they were
// typed.
func (r *Repository) ListPromotionCodes(ctx context.Context, cartID string) ([]string, error) {
	codes, err := r.queries(ctx).ListCartPromotionCodes(ctx, cartID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the coupon codes could not be listed")
	}
	if codes == nil {
		return []string{}, nil
	}

	return codes, nil
}

// PromotionCodesByCartIDs returns the coupon codes of several carts in a SINGLE
// query (no N+1).
func (r *Repository) PromotionCodesByCartIDs(
	ctx context.Context, cartIDs []string,
) (map[string][]string, error) {
	if len(cartIDs) == 0 {
		return map[string][]string{}, nil
	}

	rows, err := r.queries(ctx).ListCartPromotionCodesByCarts(ctx, cartIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the coupon codes could not be listed")
	}

	out := make(map[string][]string, len(cartIDs))
	for i := range rows {
		out[rows[i].CartID] = append(out[rows[i].CartID], rows[i].Code)
	}

	return out, nil
}

// RemovePromotionCode takes a coupon code off the cart.
//
// A row count of zero is NOT FOUND rather than a silent success: "the coupon was
// removed" and "the cart was not holding that coupon" are different answers, and
// a storefront that got the first for the second would leave a code on the
// screen that nothing will take off.
func (r *Repository) RemovePromotionCode(ctx context.Context, cartID, code string) error {
	n, err := r.queries(ctx).DeleteCartPromotionCode(ctx, cartdb.DeleteCartPromotionCodeParams{
		CartID: cartID, Code: code,
	})
	if err != nil {
		return classify(err, codeQueryFailed, "the coupon code could not be removed")
	}
	if n == 0 {
		return coreerrors.NotFound("cart_promotion_code_not_found",
			"the cart is not holding that coupon code: %s", code)
	}

	return nil
}

// DeletePromotionCodesByCart takes every coupon code off the cart.
//
// It is the counterpart of the soft deletes the other children get, and it is a
// HARD delete for the reason the table's own migration gives: the row is a
// binding, and a cart that is gone holds no coupons.
func (r *Repository) DeletePromotionCodesByCart(ctx context.Context, cartID string) error {
	if err := r.queries(ctx).DeleteCartPromotionCodesByCart(ctx, cartID); err != nil {
		return classify(err, codeQueryFailed, "the coupon codes could not be removed")
	}

	return nil
}
