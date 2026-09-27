package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// SaveToWishlist puts a variant on the customer's wishlist (ADR 0190).
//
// Saving a variant already on the list returns it unchanged, so the call can be
// repeated. A list at [models.MaxWishlistItems] refuses a new variant with
// errors.Conflict, and a customer that does not exist is errors.NotFound.
//
// The variant id is checked for its form only. The product module owns it and
// this module cannot read it (ADR 0001); a variant that was deleted, or never
// existed, is left out when the catalog shows the list.
func (s *Service) SaveToWishlist(ctx context.Context, customerID, variantID string) (models.WishlistItem, error) {
	if err := s.ready(); err != nil {
		return models.WishlistItem{}, err
	}
	if err := requireWishlistIDs(customerID, variantID); err != nil {
		return models.WishlistItem{}, err
	}
	return s.repo.SaveToWishlist(ctx, customerID, variantID, models.MaxWishlistItems, s.clock())
}

// ListWishlist returns the customer's wishlist, most recently saved first.
//
// The customer is read first, as [Service.ListAddresses] reads it: an empty
// list for a customer that does not exist would answer "nothing saved" where
// the answer is 404.
func (s *Service) ListWishlist(ctx context.Context, customerID string) ([]models.WishlistItem, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(customerID, models.CustomerIDPrefix, "customer id"); err != nil {
		return nil, err
	}
	if _, err := s.repo.GetCustomer(ctx, customerID); err != nil {
		return nil, err
	}
	return s.repo.ListWishlist(ctx, customerID)
}

// RemoveFromWishlist takes a variant off the customer's wishlist.
//
// A variant that is not on the list is not an error, so the call can be
// repeated; a customer that does not exist is errors.NotFound.
func (s *Service) RemoveFromWishlist(ctx context.Context, customerID, variantID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireWishlistIDs(customerID, variantID); err != nil {
		return err
	}
	return s.repo.RemoveFromWishlist(ctx, customerID, variantID)
}

// MarkStockAlert asks for the customer to be told once when the variant is back
// in stock (ADR 0215).
//
// The variant is put on the wishlist if it is not there, under the list's cap,
// and the mark records the sales channels the storefront request carried:
// whether the variant is in stock is answered over the warehouses those
// channels serve. Marking again starts the wait again.
func (s *Service) MarkStockAlert(
	ctx context.Context, customerID, variantID string, channels []string,
) (models.WishlistItem, error) {
	if err := s.ready(); err != nil {
		return models.WishlistItem{}, err
	}
	if err := requireWishlistIDs(customerID, variantID); err != nil {
		return models.WishlistItem{}, err
	}
	return s.repo.MarkStockAlert(ctx, customerID, variantID, channels, models.MaxWishlistItems, s.clock())
}

// UnmarkStockAlert takes the mark off and leaves the variant on the list; an
// item that is not marked is not an error.
func (s *Service) UnmarkStockAlert(ctx context.Context, customerID, variantID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireWishlistIDs(customerID, variantID); err != nil {
		return err
	}
	return s.repo.UnmarkStockAlert(ctx, customerID, variantID)
}

// MarkPriceAlert asks for the customer to be told once when the variant's price
// drops below the price at the mark (ADR 0216).
//
// The variant is put on the wishlist if it is not there, under the list's cap.
// The mark names the region the price is asked in — its currency is the
// region's — and the request's sales channels; the price at the mark is
// recorded by the alert job's first pass after it. Marking again is a new mark.
func (s *Service) MarkPriceAlert(
	ctx context.Context, customerID, variantID, regionID string, channels []string,
) (models.WishlistItem, error) {
	if err := s.ready(); err != nil {
		return models.WishlistItem{}, err
	}
	if err := requireWishlistIDs(customerID, variantID); err != nil {
		return models.WishlistItem{}, err
	}
	if err := requireID(regionID, "", "region id"); err != nil {
		return models.WishlistItem{}, err
	}
	return s.repo.MarkPriceAlert(ctx, customerID, variantID, regionID, channels, models.MaxWishlistItems, s.clock())
}

// UnmarkPriceAlert takes the price mark off and leaves the variant on the list;
// an item that is not marked is not an error.
func (s *Service) UnmarkPriceAlert(ctx context.Context, customerID, variantID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireWishlistIDs(customerID, variantID); err != nil {
		return err
	}
	return s.repo.UnmarkPriceAlert(ctx, customerID, variantID)
}

// requireWishlistIDs checks the customer id and the form of the variant id.
//
// The variant id carries no prefix check: the prefix is the product module's
// to choose, as the cart module leaves it (cart/service, requireID).
func requireWishlistIDs(customerID, variantID string) error {
	if err := requireID(customerID, models.CustomerIDPrefix, "customer id"); err != nil {
		return err
	}
	return requireID(variantID, "", "variant id")
}
