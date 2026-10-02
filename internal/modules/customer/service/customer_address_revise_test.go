package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// ReviseAddress mirrors the query: the printed fields are written only while
// they are the ones the caller read, on a live address of the customer.
func (m *memRepo) ReviseAddress(
	_ context.Context, customerID, addressID string, read, next models.AddressTerms, now time.Time,
) (models.CustomerAddress, bool, error) {
	m.record("ReviseAddress")
	a, ok := m.liveAddress(customerID, addressID)
	if !ok || a.Terms() != read {
		return models.CustomerAddress{}, false, nil
	}
	a.FirstName, a.LastName, a.Company, a.Address1, a.Address2 = next.FirstName, next.LastName, next.Company, next.Address1, next.Address2
	a.City, a.Province, a.CountryCode = next.City, next.Province, next.CountryCode
	a.PostalCode, a.Phone, a.UpdatedAt = next.PostalCode, next.Phone, now
	m.addresses[addressID] = a

	return a, true, nil
}

// TestAnAddressIsCorrectedFromWhatWasRead is ADR 0342: the printed fields are
// written from the ones read, the country code in capitals and the default
// flag kept; an address corrected since is refused; an address with no first
// line, a country that is not a code and another customer's address are
// refused, the first two before the store is asked.
func TestAnAddressIsCorrectedFromWhatWasRead(t *testing.T) {
	ctx := context.Background()
	repo := newMemRepo()
	svc := New(repo, Options{})
	customer, err := svc.CreateCustomer(ctx, CustomerInput{Email: "ada@example.com"})
	require.NoError(t, err)
	other, err := svc.CreateCustomer(ctx, CustomerInput{Email: "bob@example.com"})
	require.NoError(t, err)
	address, err := svc.CreateAddress(ctx, customer.ID, AddressInput{
		FirstName: "Ada", Address1: "12 Main St", City: "Springfield", CountryCode: "TR", IsDefaultShipping: true,
	})
	require.NoError(t, err)
	read := address.Terms()

	next := read
	next.Address1, next.PostalCode, next.CountryCode = "14 Main St", "34000", "tr"
	corrected, err := svc.ReviseAddress(ctx, customer.ID, address.ID, read, next)
	require.NoError(t, err)
	assert.Equal(t, "14 Main St|34000|TR", corrected.Address1+"|"+corrected.PostalCode+"|"+corrected.CountryCode)
	assert.True(t, corrected.IsDefaultShipping, "the default flag is kept")

	_, err = svc.ReviseAddress(ctx, customer.ID, address.ID, read, next)
	require.Error(t, err)
	assert.Equal(t, CodeAddressRevised, errors.CodeOf(err), "read before the correction: %v", err)

	calls := repo.calls["ReviseAddress"]
	blank := corrected.Terms()
	blank.Address1 = "  "
	_, err = svc.ReviseAddress(ctx, customer.ID, address.ID, corrected.Terms(), blank)
	assert.True(t, errors.IsInvalid(err), "no first line: %v", err)
	assert.Contains(t, err.Error(), "the address's first line")
	country := corrected.Terms()
	country.CountryCode = "Turkey"
	_, err = svc.ReviseAddress(ctx, customer.ID, address.ID, corrected.Terms(), country)
	assert.True(t, errors.IsInvalid(err), "a country that is not a code: %v", err)
	assert.Equal(t, calls, repo.calls["ReviseAddress"], "a refused address never reaches the store")

	_, err = svc.ReviseAddress(ctx, customer.ID, customer.ID, corrected.Terms(), corrected.Terms())
	assert.True(t, errors.IsInvalid(err), "a customer's id is not an address's: %v", err)
	assert.Equal(t, calls, repo.calls["ReviseAddress"], "nor does an id that names no address")

	_, err = svc.ReviseAddress(ctx, other.ID, address.ID, corrected.Terms(), corrected.Terms())
	assert.True(t, errors.IsNotFound(err), "another customer's address: %v", err)
}

// TestThePanelCorrectsAnAddress is ADR 0342 through the surface: the read and
// the written address cross as JSON with the provider's address keys.
func TestThePanelCorrectsAnAddress(t *testing.T) {
	ctx := context.Background()
	svc := New(newMemRepo(), Options{})
	surface := NewAdminSurface(svc)
	customer, err := svc.CreateCustomer(ctx, CustomerInput{Email: "ada@example.com"})
	require.NoError(t, err)
	address, err := svc.CreateAddress(ctx, customer.ID, AddressInput{
		Address1: "12 Main St", City: "Springfield", Province: "Illinois", CountryCode: "TR",
	})
	require.NoError(t, err)

	read := `{"first_name":"","last_name":"","company":"","address_1":"12 Main St","address_2":"","city":"Springfield",` +
		`"province":"Illinois","country_code":"TR","postal_code":"","phone":""}`
	next := strings.NewReplacer("12 Main St", "14 Main St", "Illinois", "Ohio").Replace(read)
	require.NoError(t, surface.ReviseCustomerAddress(ctx, customer.ID, address.ID, json.RawMessage(read), json.RawMessage(next)))
	stored, err := svc.GetAddress(ctx, customer.ID, address.ID)
	require.NoError(t, err)
	assert.Equal(t, "14 Main St", stored.Address1)
	assert.Equal(t, "Ohio", stored.Province, "the province is read and written by its key")

	err = surface.ReviseCustomerAddress(ctx, customer.ID, address.ID, json.RawMessage(read), json.RawMessage(next))
	assert.Equal(t, CodeAddressRevised, errors.CodeOf(err))
	err = surface.ReviseCustomerAddress(ctx, customer.ID, address.ID, json.RawMessage(`[`), json.RawMessage(next))
	assert.True(t, errors.IsInvalid(err))
}
