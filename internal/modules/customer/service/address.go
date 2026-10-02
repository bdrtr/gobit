package service

import (
	"context"
	"log/slog"

	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// AddressInput is the write input of an address.
type AddressInput struct {
	// FirstName is the first name on the address; it may be left empty.
	FirstName string
	// LastName is the last name on the address; it may be left empty.
	LastName string
	// Company is the company name; it may be left empty.
	Company string
	// Address1 is the address's first line; it is required.
	Address1 string
	// Address2 is the address's second line; it may be left empty.
	Address2 string
	// City is the city; it is required.
	City string
	// CountryCode is the ISO 3166-1 alpha-2 country code; it is required, and it
	// is stored normalized to UPPER case.
	CountryCode string
	// PostalCode is the postal code; it may be left empty.
	PostalCode string
	// Phone is the address's contact phone; it may be left empty.
	Phone string
	// IsDefaultShipping makes the address the default shipping address; the
	// customer's previous default, if any, is cleared in the SAME transaction.
	IsDefaultShipping bool
	// IsDefaultBilling makes the address the default billing address.
	IsDefaultBilling bool
}

// CreateAddress adds a new address for the customer; errors.NotFound if the
// customer does not exist.
func (s *Service) CreateAddress(ctx context.Context, customerID string, in AddressInput) (models.CustomerAddress, error) {
	if err := s.ready(); err != nil {
		return models.CustomerAddress{}, err
	}
	if err := requireID(customerID, models.CustomerIDPrefix, "customer id"); err != nil {
		return models.CustomerAddress{}, err
	}

	country, err := normalizeCountryCode(in.CountryCode)
	if err != nil {
		return models.CustomerAddress{}, err
	}
	if err := validateAddressText(in); err != nil {
		return models.CustomerAddress{}, err
	}

	now := s.clock()
	return s.repo.CreateAddress(ctx, models.CustomerAddress{
		ID:                models.NewAddressID(now),
		CustomerID:        customerID,
		FirstName:         in.FirstName,
		LastName:          in.LastName,
		Company:           in.Company,
		Address1:          in.Address1,
		Address2:          in.Address2,
		City:              in.City,
		CountryCode:       country,
		PostalCode:        in.PostalCode,
		Phone:             in.Phone,
		IsDefaultShipping: in.IsDefaultShipping,
		IsDefaultBilling:  in.IsDefaultBilling,
		CreatedAt:         now,
	})
}

// GetAddress returns the customer's address; errors.NotFound if it does not exist.
//
// The ownership check is in the query's WHERE clause: even when the id of
// another customer's address is given, the record is NOT returned.
func (s *Service) GetAddress(ctx context.Context, customerID, addressID string) (models.CustomerAddress, error) {
	if err := s.ready(); err != nil {
		return models.CustomerAddress{}, err
	}
	if err := requireAddressIDs(customerID, addressID); err != nil {
		return models.CustomerAddress{}, err
	}
	return s.repo.GetAddress(ctx, customerID, addressID)
}

// ListAddresses returns the customer's addresses.
//
// The customer's existence is verified FIRST: if an empty list were returned
// for a customer that does not exist, the client would take it for "has no
// addresses at all" instead of a 404.
func (s *Service) ListAddresses(ctx context.Context, customerID string) ([]models.CustomerAddress, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(customerID, models.CustomerIDPrefix, "customer id"); err != nil {
		return nil, err
	}
	if _, err := s.repo.GetCustomer(ctx, customerID); err != nil {
		return nil, err
	}
	return s.repo.ListAddresses(ctx, customerID)
}

// UpdateAddressInput is the partial update input of an address.
//
// A nil field means "leave it alone", a set field means "write this value". The
// default flags are NOT here; [Service.SetDefaultShippingAddress] and
// [Service.SetDefaultBillingAddress] are used for them, because changing a flag
// also concerns the customer's other addresses.
type UpdateAddressInput struct {
	// FirstName is the new first name.
	FirstName *string
	// LastName is the new last name.
	LastName *string
	// Company is the new company name.
	Company *string
	// Address1 is the address's new first line; if given, it cannot be empty.
	Address1 *string
	// Address2 is the address's new second line.
	Address2 *string
	// City is the new city; if given, it cannot be empty.
	City *string
	// CountryCode is the new country code; if given, it is validated and
	// converted to UPPER case.
	CountryCode *string
	// PostalCode is the new postal code.
	PostalCode *string
	// Phone is the new phone.
	Phone *string
}

// UpdateAddress updates the given fields of the address; errors.NotFound if it
// does not exist.
func (s *Service) UpdateAddress(
	ctx context.Context,
	customerID, addressID string,
	in UpdateAddressInput,
) (models.CustomerAddress, error) {
	if err := s.ready(); err != nil {
		return models.CustomerAddress{}, err
	}
	if err := requireAddressIDs(customerID, addressID); err != nil {
		return models.CustomerAddress{}, err
	}

	patch := models.AddressPatch{
		FirstName:  in.FirstName,
		LastName:   in.LastName,
		Company:    in.Company,
		Address1:   in.Address1,
		Address2:   in.Address2,
		City:       in.City,
		PostalCode: in.PostalCode,
		Phone:      in.Phone,
	}
	if in.CountryCode != nil {
		country, err := normalizeCountryCode(*in.CountryCode)
		if err != nil {
			return models.CustomerAddress{}, err
		}
		patch.CountryCode = &country
	}
	if err := validateAddressPatch(patch); err != nil {
		return models.CustomerAddress{}, err
	}

	return s.repo.UpdateAddress(ctx, customerID, addressID, patch, s.clock())
}

// DeleteAddress deletes the address with a soft delete; errors.NotFound if it
// does not exist.
func (s *Service) DeleteAddress(ctx context.Context, customerID, addressID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireAddressIDs(customerID, addressID); err != nil {
		return err
	}
	return s.repo.DeleteAddress(ctx, customerID, addressID, s.clock())
}

// SetDefaultShippingAddress makes the address the customer's default shipping address.
//
// A customer can have AT MOST ONE default shipping address: the old flag is
// removed in the same transaction, and the constraint is enforced by a partial
// unique index in the database (see repository.Repo.SetDefaultAddress).
func (s *Service) SetDefaultShippingAddress(ctx context.Context, customerID, addressID string) (models.CustomerAddress, error) {
	return s.setDefault(ctx, customerID, addressID, models.DefaultShipping)
}

// SetDefaultBillingAddress makes the address the customer's default billing address.
//
// It is INDEPENDENT of the shipping flag: a single address holding both flags
// and the two flags being spread over different addresses are both valid.
func (s *Service) SetDefaultBillingAddress(ctx context.Context, customerID, addressID string) (models.CustomerAddress, error) {
	return s.setDefault(ctx, customerID, addressID, models.DefaultBilling)
}

// setDefault is the shared body of the two default-assignment paths.
func (s *Service) setDefault(
	ctx context.Context,
	customerID, addressID string,
	kind models.DefaultKind,
) (models.CustomerAddress, error) {
	if err := s.ready(); err != nil {
		return models.CustomerAddress{}, err
	}
	if err := requireAddressIDs(customerID, addressID); err != nil {
		return models.CustomerAddress{}, err
	}

	address, err := s.repo.SetDefaultAddress(ctx, customerID, addressID, kind, s.clock())
	if err != nil {
		return models.CustomerAddress{}, err
	}

	s.log.DebugContext(ctx, "customer's default address updated",
		slog.String("customer_id", customerID),
		slog.String("address_id", addressID),
		slog.String("kind", kind.String()),
	)
	return address, nil
}

// requireAddressIDs validates the customer's and the address's ids together.
func requireAddressIDs(customerID, addressID string) error {
	if err := requireID(customerID, models.CustomerIDPrefix, "customer id"); err != nil {
		return err
	}
	return requireID(addressID, models.AddressIDPrefix, "address id")
}

// validateAddressText validates the address's text fields.
func validateAddressText(in AddressInput) error {
	if err := requireText("the address's first line", in.Address1); err != nil {
		return err
	}
	if err := requireText("city", in.City); err != nil {
		return err
	}
	if err := validatePerson(in.FirstName, in.LastName, in.Phone); err != nil {
		return err
	}
	if err := checkLen("company", in.Company, models.MaxNameLen); err != nil {
		return err
	}
	if err := checkLen("the address's first line", in.Address1, models.MaxAddressLen); err != nil {
		return err
	}
	if err := checkLen("the address's second line", in.Address2, models.MaxAddressLen); err != nil {
		return err
	}
	if err := checkLen("city", in.City, models.MaxNameLen); err != nil {
		return err
	}
	return checkLen("postal code", in.PostalCode, models.MaxPostalCodeLen)
}

// validateAddressPatch validates the fields in a partial update.
//
// Required fields (first line, city) CANNOT BE EMPTY if given: a partial update
// may skip a field, but it cannot remove an existing requirement.
func validateAddressPatch(patch models.AddressPatch) error {
	if patch.Address1 != nil {
		if err := requireText("the address's first line", *patch.Address1); err != nil {
			return err
		}
		if err := checkLen("the address's first line", *patch.Address1, models.MaxAddressLen); err != nil {
			return err
		}
	}
	if patch.City != nil {
		if err := requireText("city", *patch.City); err != nil {
			return err
		}
		if err := checkLen("city", *patch.City, models.MaxNameLen); err != nil {
			return err
		}
	}
	if patch.Company != nil {
		if err := checkLen("company", *patch.Company, models.MaxNameLen); err != nil {
			return err
		}
	}
	if patch.Address2 != nil {
		if err := checkLen("the address's second line", *patch.Address2, models.MaxAddressLen); err != nil {
			return err
		}
	}
	if patch.PostalCode != nil {
		if err := checkLen("postal code", *patch.PostalCode, models.MaxPostalCodeLen); err != nil {
			return err
		}
	}
	return validatePatchPerson(models.CustomerPatch{
		FirstName: patch.FirstName,
		LastName:  patch.LastName,
		Phone:     patch.Phone,
	})
}
