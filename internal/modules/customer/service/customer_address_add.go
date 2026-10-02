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
