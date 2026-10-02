package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// yeniMusteri opens a customer for a test.
func yeniMusteri(ctx context.Context, t *testing.T, svc *Service, email string) models.Customer {
	t.Helper()

	c, err := svc.CreateCustomer(ctx, CustomerInput{Email: email})
	require.NoError(t, err)
	return c
}

// TestTheCountryCodeIsNormalized proves that the country code is converted to
// UPPER case and that its format is validated.
func TestTheCountryCodeIsNormalized(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)
	customer := yeniMusteri(ctx, t, svc, "country@example.com")

	address, err := svc.CreateAddress(ctx, customer.ID, gecerliAdres())
	require.NoError(t, err)
	assert.Equal(t, "TR", address.CountryCode, "the country code has to be converted to UPPER case")

	for _, code := range []string{"", "T", "TUR", "T1"} {
		input := gecerliAdres()
		input.CountryCode = code
		_, err := svc.CreateAddress(ctx, customer.ID, input)
		require.Error(t, err, "invalid country code: %q", code)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	}
}

// TestRequiredAddressFields proves that an empty first line and an empty city
// are rejected.
func TestRequiredAddressFields(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)
	customer := yeniMusteri(ctx, t, svc, "required@example.com")

	blankLine := gecerliAdres()
	blankLine.Address1 = "   "
	_, err := svc.CreateAddress(ctx, customer.ID, blankLine)
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	blankCity := gecerliAdres()
	blankCity.City = ""
	_, err = svc.CreateAddress(ctx, customer.ID, blankCity)
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestTheDefaultAddressIsUnique proves that a customer has a single default
// shipping address and a single default billing address.
//
// When a new default is assigned, the flag of the OLD one has to be removed; an
// implementation that assumed it had been removed would leave the customer with
// two default shipping addresses, and the cart could not know which one to
// pick. That the rule is also enforced by a database constraint is proven in
// the integration test.
func TestTheDefaultAddressIsUnique(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)
	customer := yeniMusteri(ctx, t, svc, "default@example.com")

	first, err := svc.CreateAddress(ctx, customer.ID, gecerliAdres())
	require.NoError(t, err)
	second, err := svc.CreateAddress(ctx, customer.ID, gecerliAdres())
	require.NoError(t, err)

	_, err = svc.SetDefaultShippingAddress(ctx, customer.ID, first.ID)
	require.NoError(t, err)

	updated, err := svc.SetDefaultShippingAddress(ctx, customer.ID, second.ID)
	require.NoError(t, err)
	assert.True(t, updated.IsDefaultShipping)

	addresses, err := svc.ListAddresses(ctx, customer.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, countDefaults(addresses, models.DefaultShipping),
		"a customer has to have a single default shipping address")

	previous, err := svc.GetAddress(ctx, customer.ID, first.ID)
	require.NoError(t, err)
	assert.False(t, previous.IsDefaultShipping, "the old default's flag has to be removed")
}

// TestShippingAndBillingDefaultsAreIndependent proves that the two flags do not
// affect each other.
func TestShippingAndBillingDefaultsAreIndependent(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)
	customer := yeniMusteri(ctx, t, svc, "independent@example.com")

	shipping, err := svc.CreateAddress(ctx, customer.ID, gecerliAdres())
	require.NoError(t, err)
	billing, err := svc.CreateAddress(ctx, customer.ID, gecerliAdres())
	require.NoError(t, err)

	_, err = svc.SetDefaultShippingAddress(ctx, customer.ID, shipping.ID)
	require.NoError(t, err)
	_, err = svc.SetDefaultBillingAddress(ctx, customer.ID, billing.ID)
	require.NoError(t, err)

	readShipping, err := svc.GetAddress(ctx, customer.ID, shipping.ID)
	require.NoError(t, err)
	assert.True(t, readShipping.IsDefaultShipping)
	assert.False(t, readShipping.IsDefaultBilling, "the billing flag must not affect the shipping flag")

	readBilling, err := svc.GetAddress(ctx, customer.ID, billing.ID)
	require.NoError(t, err)
	assert.True(t, readBilling.IsDefaultBilling)
	assert.False(t, readBilling.IsDefaultShipping)
}

// TestTheDefaultFlagOnCreateClearsTheOldOne proves that a default flag given
// when the address is created clears the old one.
func TestTheDefaultFlagOnCreateClearsTheOldOne(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)
	customer := yeniMusteri(ctx, t, svc, "create@example.com")

	firstInput := gecerliAdres()
	firstInput.IsDefaultShipping = true
	first, err := svc.CreateAddress(ctx, customer.ID, firstInput)
	require.NoError(t, err)
	assert.True(t, first.IsDefaultShipping)

	secondInput := gecerliAdres()
	secondInput.IsDefaultShipping = true
	_, err = svc.CreateAddress(ctx, customer.ID, secondInput)
	require.NoError(t, err)

	addresses, err := svc.ListAddresses(ctx, customer.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, countDefaults(addresses, models.DefaultShipping),
		"the new default has to clear the old one")
}

// TestWhenTheDefaultAddressIsDeleted proves that a new default can be
// assigned in place of a deleted one.
//
// The partial unique index is defined with the condition deleted_at IS NULL; a
// deleted row leaves the index's scope. Without the condition, a deleted
// default would occupy the place forever.
func TestWhenTheDefaultAddressIsDeleted(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)
	customer := yeniMusteri(ctx, t, svc, "deleted@example.com")

	first, err := svc.CreateAddress(ctx, customer.ID, gecerliAdres())
	require.NoError(t, err)
	_, err = svc.SetDefaultShippingAddress(ctx, customer.ID, first.ID)
	require.NoError(t, err)

	require.NoError(t, svc.DeleteAddress(ctx, customer.ID, first.ID))

	second, err := svc.CreateAddress(ctx, customer.ID, gecerliAdres())
	require.NoError(t, err)
	newDefault, err := svc.SetDefaultShippingAddress(ctx, customer.ID, second.ID)
	require.NoError(t, err)
	assert.True(t, newDefault.IsDefaultShipping)
}

// TestAnotherCustomersAddressCannotBeRead proves that the ownership check is
// in the query.
func TestAnotherCustomersAddressCannotBeRead(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	owner := yeniMusteri(ctx, t, svc, "owner@example.com")
	stranger := yeniMusteri(ctx, t, svc, "stranger@example.com")

	address, err := svc.CreateAddress(ctx, owner.ID, gecerliAdres())
	require.NoError(t, err)

	_, err = svc.GetAddress(ctx, stranger.ID, address.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"another customer's address must not be readable")

	err = svc.DeleteAddress(ctx, stranger.ID, address.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"another customer's address must not be deletable")

	_, err = svc.SetDefaultShippingAddress(ctx, stranger.ID, address.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"another customer's address must not be made the default")
}

// TestAddressForAMissingCustomer proves that NotFound is returned for a missing
// customer.
func TestAddressForAMissingCustomer(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	_, err := svc.CreateAddress(ctx, models.NewCustomerID(sabitSaat), gecerliAdres())
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	_, err = svc.ListAddresses(ctx, models.NewCustomerID(sabitSaat))
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"a customer that does not exist has to get NotFound, not an empty list")
}

// TestTheAddressUpdateIsPartial proves that the fields not given are kept.
func TestTheAddressUpdateIsPartial(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)
	customer := yeniMusteri(ctx, t, svc, "partial@example.com")

	address, err := svc.CreateAddress(ctx, customer.ID, gecerliAdres())
	require.NoError(t, err)

	newCity := "Ankara"
	updated, err := svc.UpdateAddress(ctx, customer.ID, address.ID, UpdateAddressInput{City: &newCity})
	require.NoError(t, err)
	assert.Equal(t, "Ankara", updated.City)
	assert.Equal(t, address.Address1, updated.Address1, "a field not given has to be kept")
	assert.Equal(t, address.CountryCode, updated.CountryCode)

	// A required field cannot be EMPTY if it is given.
	empty := ""
	_, err = svc.UpdateAddress(ctx, customer.ID, address.ID, UpdateAddressInput{City: &empty})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// countDefaults returns how many addresses carry the flag of the given kind.
func countDefaults(addresses []models.CustomerAddress, kind models.DefaultKind) int {
	var n int
	for i := range addresses {
		a := &addresses[i]
		if (kind == models.DefaultShipping && a.IsDefaultShipping) ||
			(kind == models.DefaultBilling && a.IsDefaultBilling) {
			n++
		}
	}
	return n
}
