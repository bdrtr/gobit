package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// TestAProvinceIsBoundedAsACityIs is ADR 0369's bound: a province as long as
// a city may be is written, and one byte more is refused by name on a new
// address, on a patch and on a correction alike.
func TestAProvinceIsBoundedAsACityIs(t *testing.T) {
	ctx := context.Background()
	svc := New(newMemRepo(), Options{})
	customer, err := svc.CreateCustomer(ctx, CustomerInput{Email: "province@example.test"})
	require.NoError(t, err)
	longest := strings.Repeat("a", models.MaxNameLen)
	tooLong := longest + "a"

	address, err := svc.CreateAddress(ctx, customer.ID, AddressInput{
		Address1: "1 Main St", City: "Konak", Province: longest, CountryCode: "TR",
	})
	require.NoError(t, err, "a province as long as a city may be")
	assert.Equal(t, longest, address.Province)

	refused := func(label string, err error) {
		t.Helper()
		require.Error(t, err, label)
		assert.True(t, errors.IsInvalid(err), "%s: %v", label, err)
		assert.Contains(t, err.Error(), "province", "%s names the field", label)
	}
	_, err = svc.CreateAddress(ctx, customer.ID, AddressInput{
		Address1: "1 Main St", City: "Konak", Province: tooLong, CountryCode: "TR",
	})
	refused("a new address", err)
	_, err = svc.UpdateAddress(ctx, customer.ID, address.ID, UpdateAddressInput{Province: &tooLong})
	refused("a patch", err)
	next := address.Terms()
	next.Province = tooLong
	_, err = svc.ReviseAddress(ctx, customer.ID, address.ID, address.Terms(), next)
	refused("a correction", err)
}
