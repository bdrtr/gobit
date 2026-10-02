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
		"company":"","address_1":"12 Main St","address_2":"Flat 3","city":"Springfield","country_code":"tr",
		"postal_code":"34000","phone":"555"}`), true, false)
	require.NoError(t, err)
	stored, err := svc.GetAddress(ctx, customer.ID, id)
	require.NoError(t, err)
	assert.Equal(t, "Ada|Byron|12 Main St|Flat 3|Springfield|TR|34000|555", stored.FirstName+"|"+stored.LastName+"|"+
		stored.Address1+"|"+stored.Address2+"|"+stored.City+"|"+stored.CountryCode+"|"+stored.PostalCode+"|"+stored.Phone)
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
