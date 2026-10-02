//go:build integration

package customer_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// TestAnAddressIsCorrectedOnlyAsItWasRead is ADR 0342 against a real
// PostgreSQL: the printed fields are written from the ones read and the
// default flag is kept; a stale line, city, country or phone writes nothing
// and is refused; another customer's address is not found; a deleted
// address is not found.
func TestAnAddressIsCorrectedOnlyAsItWasRead(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	customer, err := svc.CreateCustomer(ctx, service.CustomerInput{Email: newEmail(t)})
	require.NoError(t, err)
	other, err := svc.CreateCustomer(ctx, service.CustomerInput{Email: newEmail(t)})
	require.NoError(t, err)
	address, err := svc.CreateAddress(ctx, customer.ID, service.AddressInput{
		FirstName: "Ada", Address1: "12 Main St", City: "Springfield", CountryCode: "TR", Phone: "555",
		IsDefaultBilling: true,
	})
	require.NoError(t, err)
	read := address.Terms()

	next := models.AddressTerms{
		FirstName: "Augusta", LastName: "King", Company: "Analytical", Address1: "14 Main St", Address2: "Flat 3",
		City: "Shelbyville", CountryCode: "DE", PostalCode: "34000", Phone: "556",
	}
	corrected, err := svc.ReviseAddress(ctx, customer.ID, address.ID, read, next)
	require.NoError(t, err)
	stored, err := svc.GetAddress(ctx, customer.ID, address.ID)
	require.NoError(t, err)
	assert.Equal(t, next, stored.Terms(), "every printed field written")
	assert.Equal(t, corrected.Terms(), stored.Terms())
	assert.True(t, stored.UpdatedAt.After(address.UpdatedAt), "the moment it was written moves")
	assert.True(t, stored.IsDefaultBilling, "the default flag is kept")

	for label, change := range map[string]func(*models.AddressTerms){
		"a first name read before":  func(t *models.AddressTerms) { t.FirstName = read.FirstName },
		"a last name read before":   func(t *models.AddressTerms) { t.LastName = read.LastName },
		"a company read before":     func(t *models.AddressTerms) { t.Company = read.Company },
		"a first line read before":  func(t *models.AddressTerms) { t.Address1 = read.Address1 },
		"a second line read before": func(t *models.AddressTerms) { t.Address2 = read.Address2 },
		"a city read before":        func(t *models.AddressTerms) { t.City = read.City },
		"a country read before":     func(t *models.AddressTerms) { t.CountryCode = read.CountryCode },
		"a postal code read before": func(t *models.AddressTerms) { t.PostalCode = read.PostalCode },
		"a phone read before":       func(t *models.AddressTerms) { t.Phone = read.Phone },
	} {
		stale := stored.Terms()
		change(&stale)
		_, err = svc.ReviseAddress(ctx, customer.ID, address.ID, stale, read)
		require.Error(t, err, label)
		assert.Equal(t, service.CodeAddressRevised, errors.CodeOf(err), "%s: %v", label, err)
	}

	_, err = svc.ReviseAddress(ctx, other.ID, address.ID, stored.Terms(), next)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "another customer's address: %v", err)
	require.NoError(t, svc.DeleteAddress(ctx, customer.ID, address.ID))
	_, err = svc.ReviseAddress(ctx, customer.ID, address.ID, stored.Terms(), next)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "a deleted address: %v", err)
}
