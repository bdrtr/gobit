package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// AddLineItemInput holds the fields of the line to be added to the cart.
type AddLineItemInput struct {
	// VariantID is the product variant being added; it is REQUIRED. It belongs
	// to the product module, its existence is not validated here (ADR 0001) and
	// no foreign key is given.
	VariantID string
	// Title is the line's display name; it is REQUIRED. It is COPIED from the
	// variant: even if the catalog changes later, the name seen in the cart does
	// not change.
	Title string
	// Quantity is the quantity to be added; it must be POSITIVE.
	Quantity int64
	// UnitPrice is the unit price (minor unit).
	//
	// THE CLIENT DOES NOT and cannot give its value: there is no price field in
	// the storefront body (see addLineItemRequest in the api package) and the
	// only way to open a line is the add_line_item flow, which takes the price
	// from the pricing module. Its looking optional here is not a shortcoming of
	// the service but its boundary — the module cannot know whether the price is
	// CORRECT, it only checks its range; that is why the authority over the price
	// is guarded by WHO the caller is, and the only caller is the flow.
	//
	// The final value is written by the flow as well: the calculation round that
	// runs right after the line is added re-prices all of the lines with the
	// current quantity and writes the result with [Service.SetTotals].
	UnitPrice int64
	// Metadata is the caller's free-form extra data.
	Metadata map[string]any
}

// AddLineItem adds a line to the cart.
//
// # What happens if the same variant is added a second time
//
// A NEW LINE IS NOT OPENED; the QUANTITY of the existing line GOES UP. The
// decision rests on three grounds:
//
//  1. Price tiers. The pricing module picks the price by quantity range
//     (min_quantity/max_quantity). If the same variant is split into two lines
//     as 3 + 2, both lines are priced from the "1-4" tier and the customer DOES
//     NOT GET the "5+" price they earned. When the quantity is summed into a
//     single line, the tier is picked correctly.
//  2. Stock reservation. complete_cart in Phase 6 makes a reservation per line;
//     two lines of the same variant mean two separate reservations for the same
//     stock, and the compensation gets complicated on a partial success.
//  3. Customer expectation. The same product appearing twice in the cart gives
//     the impression that the products are different.
//
// The decision is enforced at the database level too: the
// cart_line_items_cart_variant_uniq partial unique index prevents even a write
// path that somehow gets around the cart lock from opening the second line.
//
// In the merge only the QUANTITY is carried over; the existing line's title,
// unit price and metadata are PRESERVED. Per-line customization (for example a
// different gift note on the same variant) is not supported in this phase; if it
// were, the merge criterion would have to be "variant + customization" rather
// than the variant.
func (s *Service) AddLineItem(ctx context.Context, cartID string, in AddLineItemInput) (models.LineItem, error) {
	if err := requireID("variant_id", in.VariantID); err != nil {
		return models.LineItem{}, err
	}
	title := strings.TrimSpace(in.Title)
	if err := requireText("title", title); err != nil {
		return models.LineItem{}, err
	}
	if err := checkQuantity(in.Quantity); err != nil {
		return models.LineItem{}, err
	}
	if err := checkAmount("unit_price", in.UnitPrice, models.MaxAmount); err != nil {
		return models.LineItem{}, err
	}

	var item models.LineItem
	_, err := s.mutate(ctx, cartID, func(ctx context.Context, cart models.Cart) error {
		existing, err := s.store.GetLineItemByVariant(ctx, cart.ID, in.VariantID)
		switch {
		case err == nil:
			// The sum is checked without overflow: even if the sum of the two
			// quantities fits into an int64, it cannot go over the model's
			// quantity ceiling.
			if existing.Quantity > models.MaxQuantity-in.Quantity {
				return errors.Invalid(CodeInvalidInput,
					"the line quantity exceeds the limit once merged: %d + %d > %d",
					existing.Quantity, in.Quantity, models.MaxQuantity)
			}
			item, err = s.store.SetLineItemQuantity(ctx, cart.ID, existing.ID, existing.Quantity+in.Quantity)
			return err
		case errors.IsNotFound(err):
			item, err = s.store.CreateLineItem(ctx, models.LineItem{
				ID:        models.NewLineItemID(),
				CartID:    cart.ID,
				VariantID: in.VariantID,
				Title:     title,
				Quantity:  in.Quantity,
				UnitPrice: in.UnitPrice,
				Metadata:  in.Metadata,
			})
			return err
		default:
			return err
		}
	})
	if err != nil {
		return models.LineItem{}, err
	}
	return item, nil
}

// UpdateLineItemQuantity writes the line's quantity as an ABSOLUTE value.
//
// If quantity is zero or negative, errors.Invalid is returned; the line IS NOT
// DELETED. "Set the quantity to zero" and "remove the line" are separate
// intents and they have separate methods ([Service.RemoveLineItem]); turning one
// into the other would mean that a bug sending zero into the quantity field
// silently deletes data.
func (s *Service) UpdateLineItemQuantity(ctx context.Context, cartID, lineID string, quantity int64) (models.LineItem, error) {
	if err := requireID("line_item_id", lineID); err != nil {
		return models.LineItem{}, err
	}
	if err := checkQuantity(quantity); err != nil {
		return models.LineItem{}, err
	}

	var item models.LineItem
	_, err := s.mutate(ctx, cartID, func(ctx context.Context, cart models.Cart) error {
		var err error
		item, err = s.store.SetLineItemQuantity(ctx, cart.ID, lineID, quantity)
		return err
	})
	if err != nil {
		return models.LineItem{}, err
	}
	return item, nil
}

// RemoveLineItem removes the line from the cart (soft delete).
// If the line is not in the cart, errors.NotFound is returned.
func (s *Service) RemoveLineItem(ctx context.Context, cartID, lineID string) error {
	if err := requireID("line_item_id", lineID); err != nil {
		return err
	}
	_, err := s.mutate(ctx, cartID, func(ctx context.Context, cart models.Cart) error {
		return s.store.SoftDeleteLineItem(ctx, cart.ID, lineID)
	})
	return err
}
