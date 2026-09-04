package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// AddShippingMethodInput holds the fields of the shipping method to be added to
// the cart.
type AddShippingMethodInput struct {
	// Name is the method's display name; it is REQUIRED.
	Name string
	// ShippingOptionID is the identifier of the option in the fulfillment
	// module; it is OPTIONAL (the option catalog arrives in Phase 7) and it is
	// not a foreign key.
	ShippingOptionID string
	// Amount is the shipping amount (minor unit); it cannot be negative.
	Amount int64
	// Data is provider-specific free-form data.
	Data map[string]any
}

// AddShippingMethod adds a shipping method to the cart.
//
// A cart may hold more than one method (different shipping profiles mean
// separate shipments), but the SAME shipping option cannot be added a second
// time: a repeat would mean charging the same shipment twice, and the
// cart_shipping_methods_cart_option_uniq index turns that into errors.Conflict.
// Methods without an option (Phase 5) are outside the constraint.
//
// The amount is NOT added into the cart's shipping_total HERE; the summing is
// the calculate_totals workflow's job (see [Service.SetTotals]). The addition
// only increments the cart's shape counter and marks the totals stale.
func (s *Service) AddShippingMethod(ctx context.Context, cartID string, in AddShippingMethodInput) (models.ShippingMethod, error) {
	name := strings.TrimSpace(in.Name)
	if err := requireText("name", name); err != nil {
		return models.ShippingMethod{}, err
	}
	if in.ShippingOptionID != "" {
		if err := requireID("shipping_option_id", in.ShippingOptionID); err != nil {
			return models.ShippingMethod{}, err
		}
	}
	if err := checkAmount("amount", in.Amount, models.MaxAmount); err != nil {
		return models.ShippingMethod{}, err
	}

	var method models.ShippingMethod
	_, err := s.mutate(ctx, cartID, func(ctx context.Context, cart models.Cart) error {
		var err error
		method, err = s.store.CreateShippingMethod(ctx, models.ShippingMethod{
			ID:               models.NewShippingMethodID(),
			CartID:           cart.ID,
			Name:             name,
			ShippingOptionID: in.ShippingOptionID,
			Amount:           in.Amount,
			Data:             in.Data,
		})
		return err
	})
	if err != nil {
		return models.ShippingMethod{}, err
	}
	return method, nil
}

// RemoveShippingMethod removes the shipping method from the cart (soft delete).
// If the method is not in the cart, errors.NotFound is returned.
func (s *Service) RemoveShippingMethod(ctx context.Context, cartID, methodID string) error {
	if err := requireID("shipping_method_id", methodID); err != nil {
		return err
	}
	_, err := s.mutate(ctx, cartID, func(ctx context.Context, cart models.Cart) error {
		return s.store.SoftDeleteShippingMethod(ctx, cart.ID, methodID)
	})
	return err
}
