package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bdrtr/gobit/core/errors"
)

// This file is customer's CROSS-MODULE surface (ADR 0001).
//
// The signatures here use ONLY primitive and stdlib types. The reason is Go's
// structural conformance: the consuming module (cart, order, or a workflow)
// CANNOT import customer, and therefore cannot name a type such as
// models.Customer in its own interface — the moment it names one it becomes a
// different type in its own package and the concrete service does not satisfy
// it. Signatures written with primitive types, on the other hand, can be
// repeated verbatim in the consumer's own package:
//
//	// in the cart module, WITHOUT importing customer:
//	type CustomerReader interface {
//	    CustomerGroupIDs(ctx context.Context, customerID string) ([]string, error)
//	}
//	customers, err := container.Resolve[CustomerReader](c, "customer.service")
//
// The surface is deliberately kept NARROW: every method added here is a
// contract customer can never change again (the mismatch is caught not at
// compile time but at the moment of resolution from the container). If the
// whole of a field set is needed, the right path is not a new primitive method
// but the Query layer: the "customer" provider gives all of a record's fields
// and its group ids in a single call (ADR 0004).

// CustomerEmail returns the customer's e-mail address; errors.NotFound if the
// customer does not exist.
//
// Cart and order flows need a contact address even for a guest customer; this
// surface, which gives the e-mail on its own, makes it unnecessary for the
// consumer to bind to the whole of the customer model.
func (s *Service) CustomerEmail(ctx context.Context, customerID string) (string, error) {
	customer, err := s.GetCustomer(ctx, customerID)
	if err != nil {
		return "", err
	}
	return customer.Email, nil
}

// CustomerGroupIDs returns the ids of the groups the customer is a member of;
// errors.NotFound if the customer does not exist.
//
// The real consumer of this surface is the PRICE COMPUTATION: pricing's rule
// context looks at the "customer_group_id" attribute, and the customer's
// segments are placed into that context while a cart total is computed. The
// groups' names, their metadata or their creation time are not inputs to the
// computation; only the ids are carried.
//
// # The ORDER is part of the contract (ADR 0049)
//
// The slice comes back by RANK, then by id, so its HEAD is the group the
// merchant chose to speak for this customer. That matters because a customer may
// belong to several groups and a price ruled on each would otherwise be
// separated by the pricing ladder's last usable rung, which compares AMOUNT —
// the cheapest, against the merchant's intent.
//
// The order was not arbitrary before this and this comment does not pretend it
// was: the query ordered by created_at. What was missing is a PROMISE, and this
// is it. A caller may rely on the head; nothing may rely on the rest.
//
// For a customer with no groups an empty (non-nil) slice is returned.
func (s *Service) CustomerGroupIDs(ctx context.Context, customerID string) ([]string, error) {
	groups, err := s.ListGroupsOf(ctx, customerID)
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(groups))
	for i := range groups {
		ids = append(ids, groups[i].ID)
	}
	return ids, nil
}

// RegisterGuestCustomer opens a guest customer record and returns its ID.
//
// It exists so that a cart coming from the storefront can be bound to a
// customer without an account. It does the same work as
// [Service.RegisterGuest]; the difference is that its signature is primitive
// enough to be used across modules.
//
// A guest record already existing with the same e-mail is NOT an obstacle; for
// the rationale see internal/modules/customer/models, Customer.
func (s *Service) RegisterGuestCustomer(ctx context.Context, email, firstName, lastName, phone string) (string, error) {
	customer, err := s.RegisterGuest(ctx, CustomerInput{
		Email:     email,
		FirstName: firstName,
		LastName:  lastName,
		Phone:     phone,
	})
	if err != nil {
		return "", err
	}
	return customer.ID, nil
}

// interopWishlistAlert is one marked wishlist item as the alert flow reads it
// (ADR 0215, ADR 0216). The stock fields mean something when stock_alert is
// true, the price fields when price_alert is.
//
//	{"customer_id": "cus_...", "variant_id": "variant_...",
//	 "stock_alert": true, "sales_channel_ids": ["sc_..."] or null, "armed_at": "RFC 3339" or null,
//	 "price_alert": true, "price_marked_at": "RFC 3339" or null, "price_region_id": "reg_...",
//	 "price_sales_channel_ids": ["sc_..."] or null, "price_currency_code": "TRY",
//	 "price_amount": 1000 or null}
type interopWishlistAlert struct {
	CustomerID      string     `json:"customer_id"`
	VariantID       string     `json:"variant_id"`
	StockAlert      bool       `json:"stock_alert"`
	SalesChannelIDs []string   `json:"sales_channel_ids"`
	ArmedAt         *time.Time `json:"armed_at"`

	PriceAlert           bool       `json:"price_alert"`
	PriceMarkedAt        *time.Time `json:"price_marked_at"`
	PriceRegionID        string     `json:"price_region_id,omitempty"`
	PriceSalesChannelIDs []string   `json:"price_sales_channel_ids"`
	PriceCurrencyCode    string     `json:"price_currency_code,omitempty"`
	PriceAmount          *int64     `json:"price_amount"`
}

// maxAlertPage is the most marked items one read returns.
const maxAlertPage = 500

// WishlistAlertsJSON pages the wishlist items of live customers marked for
// their stock or their price, after the given key, in key order, as a JSON
// array of [interopWishlistAlert].
func (s *Service) WishlistAlertsJSON(
	ctx context.Context, afterCustomerID, afterVariantID string, limit int,
) (json.RawMessage, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > maxAlertPage {
		return nil, errors.Invalid(CodeInvalidInput,
			"a page of wishlist alerts holds between 1 and %d items, %d asked", maxAlertPage, limit)
	}
	items, err := s.repo.ListAlerts(ctx, afterCustomerID, afterVariantID, int32(limit))
	if err != nil {
		return nil, err
	}
	out := make([]interopWishlistAlert, 0, len(items))
	for i := range items {
		item := &items[i]
		out = append(out, interopWishlistAlert{
			CustomerID: item.CustomerID, VariantID: item.VariantID,
			StockAlert: item.StockAlert, SalesChannelIDs: item.StockAlertChannels, ArmedAt: item.StockAlertArmedAt,
			PriceAlert: item.PriceAlert, PriceMarkedAt: item.PriceAlertMarkedAt,
			PriceRegionID: item.PriceAlertRegionID, PriceSalesChannelIDs: item.PriceAlertChannels,
			PriceCurrencyCode: item.PriceAlertCurrency, PriceAmount: item.PriceAlertAmount,
		})
	}

	return json.Marshal(out)
}

// RecordPriceBaseline records the price at the mark named by markedAt, once,
// and says whether this call did (ADR 0216).
func (s *Service) RecordPriceBaseline(
	ctx context.Context, customerID, variantID string, markedAt time.Time, currency string, amount int64,
) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	return s.repo.RecordPriceBaseline(ctx, customerID, variantID, markedAt, currency, amount)
}

// ClearPriceAlert takes the price mark off once its mail went, only while it
// is the mark named by markedAt, and says whether this call did.
func (s *Service) ClearPriceAlert(ctx context.Context, customerID, variantID string, markedAt time.Time) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	return s.repo.ClearPriceAlert(ctx, customerID, variantID, markedAt)
}

// ArmStockAlert records that a marked variant was seen out of stock, and says
// whether this call did.
func (s *Service) ArmStockAlert(ctx context.Context, customerID, variantID string) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	return s.repo.ArmStockAlert(ctx, customerID, variantID)
}

// ClearStockAlert takes the mark off once its mail went, only while it is still
// armed at the given moment, and says whether this call did: a customer who
// marked the variant again meanwhile keeps the new mark.
func (s *Service) ClearStockAlert(ctx context.Context, customerID, variantID string, armedAt time.Time) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	return s.repo.ClearStockAlert(ctx, customerID, variantID, armedAt)
}
