package repository

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/repository/customerdb"
)

// ReviseAddress writes the address's printed fields while they are still the
// ones the caller read, and reports whether it did (ADR 0342); an address
// that moved since, that is not the customer's or that is not there, is left
// as it is.
func (r *Repo) ReviseAddress(
	ctx context.Context, customerID, addressID string, read, next models.AddressTerms, now time.Time,
) (models.CustomerAddress, bool, error) {
	if err := r.ready(); err != nil {
		return models.CustomerAddress{}, false, err
	}

	row, err := r.q.ReviseCustomerAddress(ctx, customerdb.ReviseCustomerAddressParams{
		ID: addressID, CustomerID: customerID, UpdatedAt: fromTime(now),
		FirstName: next.FirstName, LastName: next.LastName, Company: next.Company,
		Address1: next.Address1, Address2: next.Address2, City: next.City, Province: next.Province,
		CountryCode: next.CountryCode, PostalCode: next.PostalCode, Phone: next.Phone,
		ReadFirstName: read.FirstName, ReadLastName: read.LastName, ReadCompany: read.Company,
		ReadAddress1: read.Address1, ReadAddress2: read.Address2, ReadCity: read.City, ReadProvince: read.Province,
		ReadCountryCode: read.CountryCode, ReadPostalCode: read.PostalCode, ReadPhone: read.Phone,
	})
	switch {
	case stderrors.Is(err, pgx.ErrNoRows):
		return models.CustomerAddress{}, false, nil
	case err != nil:
		return models.CustomerAddress{}, false, wrapDB(err, "address %s could not be revised", addressID)
	}

	return toAddress(row), true, nil
}
