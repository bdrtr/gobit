package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// TestThePanelAddsAnAddress is ADR 0359: the surface adds the address its
// JSON names, the country upper-cased, as the default shipping or billing
// address when asked, and refuses one without a first line or of no
// customer.
func TestThePanelAddsAnAddress(t *testing.T) {
	ctx := context.Background()
	svc := New(newMemRepo(), Options{})
	surface := NewAdminSurface(svc)
	customer, err := svc.CreateCustomer(ctx, CustomerInput{Email: "ada@example.com"})
	require.NoError(t, err)

	id, err := surface.AddCustomerAddress(ctx, customer.ID, json.RawMessage(`{"first_name":"Ada","last_name":"Byron",
		"company":"","address_1":"12 Main St","address_2":"Flat 3","city":"Springfield","province":"Illinois","country_code":"tr",
		"postal_code":"34000","phone":"555"}`), true, false)
	require.NoError(t, err)
	stored, err := svc.GetAddress(ctx, customer.ID, id)
	require.NoError(t, err)
	assert.Equal(t, "Ada|Byron|12 Main St|Flat 3|Springfield|Illinois|TR|34000|555", stored.FirstName+"|"+
		stored.LastName+"|"+stored.Address1+"|"+stored.Address2+"|"+stored.City+"|"+stored.Province+"|"+
		stored.CountryCode+"|"+stored.PostalCode+"|"+stored.Phone)
	assert.True(t, stored.IsDefaultShipping, "the default shipping address, as asked")
	assert.False(t, stored.IsDefaultBilling)

	id, err = surface.AddCustomerAddress(ctx, customer.ID,
		json.RawMessage(`{"address_1":"9 Side St","city":"Shelbyville","country_code":"DE"}`), false, true)
	require.NoError(t, err)
	stored, err = svc.GetAddress(ctx, customer.ID, id)
	require.NoError(t, err)
	assert.True(t, stored.IsDefaultBilling, "the default billing address, as asked")
	assert.False(t, stored.IsDefaultShipping)

	_, err = surface.AddCustomerAddress(ctx, customer.ID, json.RawMessage(`{"city":"Springfield","country_code":"TR"}`),
		false, false)
	assert.True(t, errors.IsInvalid(err), "no first line: %v", err)
	_, err = surface.AddCustomerAddress(ctx, customer.ID, json.RawMessage(`[`), false, false)
	assert.True(t, errors.IsInvalid(err), "no JSON: %v", err)
	_, err = surface.AddCustomerAddress(ctx, "order_1", json.RawMessage(`{"address_1":"x","city":"y","country_code":"TR"}`),
		false, false)
	assert.True(t, errors.IsInvalid(err), "an id that is not a customer's: %v", err)
}

// TestThePanelMovesADefaultAndRemovesAnAddress is ADR 0360: the surface
// makes an address the default shipping or billing address, taking the flag
// from the one that held it, refuses a default there is not, and removes an
// address, which is found no more.
func TestThePanelMovesADefaultAndRemovesAnAddress(t *testing.T) {
	ctx := context.Background()
	svc := New(newMemRepo(), Options{})
	surface := NewAdminSurface(svc)
	customer, err := svc.CreateCustomer(ctx, CustomerInput{Email: "ada@example.com"})
	require.NoError(t, err)
	first, err := svc.CreateAddress(ctx, customer.ID, AddressInput{
		Address1: "1 First St", City: "Izmir", CountryCode: "TR", IsDefaultShipping: true, IsDefaultBilling: true,
	})
	require.NoError(t, err)
	second, err := svc.CreateAddress(ctx, customer.ID, AddressInput{Address1: "2 Second St", City: "Ankara", CountryCode: "TR"})
	require.NoError(t, err)

	require.NoError(t, surface.MakeAddressDefault(ctx, customer.ID, second.ID, DefaultShippingKind))
	moved, err := svc.GetAddress(ctx, customer.ID, second.ID)
	require.NoError(t, err)
	assert.True(t, moved.IsDefaultShipping)
	assert.False(t, moved.IsDefaultBilling, "the billing default is another flag")
	kept, err := svc.GetAddress(ctx, customer.ID, first.ID)
	require.NoError(t, err)
	assert.False(t, kept.IsDefaultShipping, "the flag left the first")
	assert.True(t, kept.IsDefaultBilling)
	require.NoError(t, surface.MakeAddressDefault(ctx, customer.ID, second.ID, DefaultBillingKind))
	moved, err = svc.GetAddress(ctx, customer.ID, second.ID)
	require.NoError(t, err)
	assert.True(t, moved.IsDefaultBilling)
	err = surface.MakeAddressDefault(ctx, customer.ID, second.ID, "gift")
	assert.True(t, errors.IsInvalid(err), "%v", err)

	require.NoError(t, surface.RemoveCustomerAddress(ctx, customer.ID, first.ID))
	_, err = svc.GetAddress(ctx, customer.ID, first.ID)
	assert.True(t, errors.IsNotFound(err), "the removed address is found no more: %v", err)
	err = surface.RemoveCustomerAddress(ctx, customer.ID, first.ID)
	assert.True(t, errors.IsNotFound(err), "nor removed twice: %v", err)
}
