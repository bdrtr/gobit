package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// AddressInput holds the fields of the address to be written to the cart.
//
// All of the fields are optional: the delivery information is collected piece
// by piece over the course of the checkout flow, and saving an incomplete
// address is a valid intermediate state. Deciding whether the address is
// SUFFICIENT for delivery is fulfillment's job (Phase 7); the cart only
// validates the shape here.
type AddressInput struct {
	// SourceAddressID is the identifier of the customer address the address was
	// copied from; it only documents the ORIGIN, it is not used for reading.
	SourceAddressID string
	FirstName       string
	LastName        string
	Company         string
	Address1        string
	Address2        string
	City            string
	Province        string
	// PostalCode is the postal code.
	PostalCode string
	// CountryCode is the ISO 3166-1 alpha-2 country code; if it is given it
	// must be two letters.
	CountryCode string
	Phone       string
	// Metadata is the caller's free-form extra data.
	Metadata map[string]any
}

// SetShippingAddress writes the cart's shipping address; it OVERWRITES an
// existing one.
//
// The cart's address is COPIED from the ledger in the customer module (see the
// [models.CartAddress] documentation): the cart keeps its own copy so that a
// later change to the record in the customer ledger does not corrupt a past
// cart. The side that does the copying is the caller; the cart service does not
// call the customer module (ADR 0001).
//
// A change of address affects the tax, so it increments the cart's shape
// counter and the totals are considered stale.
func (s *Service) SetShippingAddress(ctx context.Context, cartID string, in AddressInput) (models.CartAddress, error) {
	return s.setAddress(ctx, cartID, models.AddressShipping, in)
}

// SetBillingAddress writes the cart's billing address; it OVERWRITES an
// existing one.
//
// The rationale for the copy and the staleness effect are the same as in
// [Service.SetShippingAddress].
func (s *Service) SetBillingAddress(ctx context.Context, cartID string, in AddressInput) (models.CartAddress, error) {
	return s.setAddress(ctx, cartID, models.AddressBilling, in)
}

// setAddress writes the address of the given type.
//
// The existing record is UPDATED, a new one is not opened: the identifier
// staying stable means that a reference given to the address (a log record, an
// order copy) is still valid after a correction. The generated identifier is
// used only on the first write.
func (s *Service) setAddress(ctx context.Context, cartID string, kind models.AddressType, in AddressInput) (models.CartAddress, error) {
	if in.SourceAddressID != "" {
		if err := requireID("source_address_id", in.SourceAddressID); err != nil {
			return models.CartAddress{}, err
		}
	}
	country, err := normalizeCountry(in.CountryCode)
	if err != nil {
		return models.CartAddress{}, err
	}
	// The order is deliberately fixed: walking over a map would leave which
	// error is returned to chance when more than one field is too long at once.
	for _, field := range []struct{ label, value string }{
		{"first_name", in.FirstName},
		{"last_name", in.LastName},
		{"company", in.Company},
		{"address_1", in.Address1},
		{"address_2", in.Address2},
		{"city", in.City},
		{"province", in.Province},
		{"postal_code", in.PostalCode},
		{"phone", in.Phone},
	} {
		if err := checkTextLen(field.label, field.value); err != nil {
			return models.CartAddress{}, err
		}
	}

	var addr models.CartAddress
	_, err = s.mutate(ctx, cartID, func(ctx context.Context, cart models.Cart) error {
		var err error
		addr, err = s.store.UpsertCartAddress(ctx, models.CartAddress{
			ID:              models.NewAddressID(),
			CartID:          cart.ID,
			Type:            kind,
			SourceAddressID: in.SourceAddressID,
			FirstName:       strings.TrimSpace(in.FirstName),
			LastName:        strings.TrimSpace(in.LastName),
			Company:         strings.TrimSpace(in.Company),
			Address1:        strings.TrimSpace(in.Address1),
			Address2:        strings.TrimSpace(in.Address2),
			City:            strings.TrimSpace(in.City),
			Province:        strings.TrimSpace(in.Province),
			PostalCode:      strings.TrimSpace(in.PostalCode),
			CountryCode:     country,
			Phone:           strings.TrimSpace(in.Phone),
			Metadata:        in.Metadata,
		})
		return err
	})
	if err != nil {
		return models.CartAddress{}, err
	}
	return addr, nil
}
