package service

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
)

// AddCustomerAddress adds an address to the customer from its printed
// fields, as JSON under the provider's address keys, making it the default
// shipping or billing address when asked, and returns its id (ADR 0359).
func (a *AdminSurface) AddCustomerAddress(
	ctx context.Context, customerID string, address json.RawMessage, defaultShipping, defaultBilling bool,
) (string, error) {
	var fields adminAddress
	if err := json.Unmarshal(address, &fields); err != nil {
		return "", errors.Invalid(CodeInvalidInput, "the address could not be read: %v", err)
	}
	added, err := a.service().CreateAddress(ctx, customerID, AddressInput{
		FirstName: fields.FirstName, LastName: fields.LastName, Company: fields.Company, Address1: fields.Address1,
		Address2: fields.Address2, City: fields.City, CountryCode: fields.CountryCode, PostalCode: fields.PostalCode,
		Phone: fields.Phone, IsDefaultShipping: defaultShipping, IsDefaultBilling: defaultBilling,
	})
	if err != nil {
		return "", err
	}

	return added.ID, nil
}

// The default kinds the panel moves (ADR 0360).
const (
	// DefaultShippingKind makes an address the default shipping address.
	DefaultShippingKind = "shipping"
	// DefaultBillingKind makes an address the default billing address.
	DefaultBillingKind = "billing"
)

// MakeAddressDefault makes the customer's address their default shipping or
// billing address, as kind names, taking the flag from the address that
// held it in the same write (ADR 0360).
func (a *AdminSurface) MakeAddressDefault(ctx context.Context, customerID, addressID, kind string) error {
	var err error
	switch kind {
	case DefaultShippingKind:
		_, err = a.service().SetDefaultShippingAddress(ctx, customerID, addressID)
	case DefaultBillingKind:
		_, err = a.service().SetDefaultBillingAddress(ctx, customerID, addressID)
	default:
		err = errors.Invalid(CodeInvalidInput, "a default is %q or %q, not %q", DefaultShippingKind, DefaultBillingKind, kind)
	}

	return err
}

// RemoveCustomerAddress removes the customer's address (ADR 0360).
func (a *AdminSurface) RemoveCustomerAddress(ctx context.Context, customerID, addressID string) error {
	return a.service().DeleteAddress(ctx, customerID, addressID)
}
