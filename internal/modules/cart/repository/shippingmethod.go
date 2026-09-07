package repository

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/repository/cartdb"
)

// This file is the access to the cart_shipping_methods table. A cart carries more
// than one shipping method, each one is deleted on its own or all of them at once
// with the cart, and that is a set of queries separate from the cart's. The cart
// itself is in repository.go.

// --- shipping methods --------------------------------------------------------

// CreateShippingMethod adds a shipping method to the cart.
func (r *Repository) CreateShippingMethod(ctx context.Context, method models.ShippingMethod) (models.ShippingMethod, error) {
	data, err := fromJSONMap(method.Data)
	if err != nil {
		return models.ShippingMethod{}, err
	}

	row, err := r.queries(ctx).CreateShippingMethod(ctx, cartdb.CreateShippingMethodParams{
		ID:               method.ID,
		CartID:           method.CartID,
		Name:             method.Name,
		ShippingOptionID: nullString(method.ShippingOptionID),
		Amount:           method.Amount,
		Data:             data,
	})
	if err != nil {
		return models.ShippingMethod{}, classify(err, codeQueryFailed, "the shipping method could not be added")
	}
	return toShippingMethod(row)
}

// GetShippingMethod returns the shipping method by its ID; NotFound if there is
// none.
func (r *Repository) GetShippingMethod(ctx context.Context, cartID, methodID string) (models.ShippingMethod, error) {
	row, err := r.queries(ctx).GetShippingMethod(ctx, cartdb.GetShippingMethodParams{
		ID:     methodID,
		CartID: cartID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ShippingMethod{}, shippingMethodNotFound(cartID, methodID)
		}
		return models.ShippingMethod{}, classify(err, codeQueryFailed, "the shipping method could not be read")
	}
	return toShippingMethod(row)
}

// ListShippingMethods returns the cart's shipping methods.
func (r *Repository) ListShippingMethods(ctx context.Context, cartID string) ([]models.ShippingMethod, error) {
	rows, err := r.queries(ctx).ListShippingMethods(ctx, cartID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the shipping methods could not be listed")
	}

	out := make([]models.ShippingMethod, 0, len(rows))
	for i := range rows {
		method, convErr := toShippingMethod(rows[i])
		if convErr != nil {
			return nil, convErr
		}
		out = append(out, method)
	}
	return out, nil
}

// SoftDeleteShippingMethod soft-deletes the shipping method; it returns NotFound
// if there is none.
func (r *Repository) SoftDeleteShippingMethod(ctx context.Context, cartID, methodID string) error {
	affected, err := r.queries(ctx).SoftDeleteShippingMethod(ctx, cartdb.SoftDeleteShippingMethodParams{
		ID:     methodID,
		CartID: cartID,
	})
	if err != nil {
		return classify(err, codeQueryFailed, "the shipping method could not be removed")
	}
	if affected == 0 {
		return shippingMethodNotFound(cartID, methodID)
	}
	return nil
}

// SoftDeleteShippingMethodsByCart soft-deletes all of the cart's shipping
// methods.
func (r *Repository) SoftDeleteShippingMethodsByCart(ctx context.Context, cartID string) error {
	if err := r.queries(ctx).SoftDeleteShippingMethodsByCart(ctx, cartID); err != nil {
		return classify(err, codeQueryFailed, "the shipping methods could not be deleted")
	}
	return nil
}

// shippingMethodNotFound builds the shared error for a missing shipping method.
func shippingMethodNotFound(cartID, methodID string) error {
	return errors.NotFound(codeShippingNotFound,
		"shipping method not found (cart: %s, method: %s)", cartID, methodID)
}
