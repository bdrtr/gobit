package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// CodeAddressRevised refuses a correction of an address when another writer
// changed its printed fields since the caller read them (ADR 0342).
const CodeAddressRevised = "customer_address_revised"

// ReviseAddress corrects the address's printed fields, writing them only
// while they are the ones the caller read, and refuses with
// [CodeAddressRevised] when another writer changed any of them since (ADR
// 0342). The fields are checked as a new address's are, the country code
// normalized; the default flags are kept.
func (s *Service) ReviseAddress(
	ctx context.Context, customerID, addressID string, read, next models.AddressTerms,
) (models.CustomerAddress, error) {
	if err := s.ready(); err != nil {
		return models.CustomerAddress{}, err
	}
	if err := requireAddressIDs(customerID, addressID); err != nil {
		return models.CustomerAddress{}, err
	}
	country, err := normalizeCountryCode(next.CountryCode)
	if err != nil {
		return models.CustomerAddress{}, err
	}
	next.CountryCode = country
	if err := validateAddressText(AddressInput{
		FirstName: next.FirstName, LastName: next.LastName, Company: next.Company, Address1: next.Address1,
		Address2: next.Address2, City: next.City, Province: next.Province, CountryCode: next.CountryCode,
		PostalCode: next.PostalCode, Phone: next.Phone,
	}); err != nil {
		return models.CustomerAddress{}, err
	}

	address, revised, err := s.repo.ReviseAddress(ctx, customerID, addressID, read, next, s.clock())
	if err != nil || revised {
		return address, err
	}

	// Nothing was written: the address is gone or not the customer's, or it
	// was corrected since.
	if _, err := s.repo.GetAddress(ctx, customerID, addressID); err != nil {
		return models.CustomerAddress{}, err
	}

	return models.CustomerAddress{}, errors.Conflict(CodeAddressRevised,
		"address %s was corrected since it was read; draw the page again", addressID)
}
