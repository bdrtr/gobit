package service_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// shippedTo places an order shipping to the given address and billed to a
// company, and returns it.
func shippedTo(t *testing.T, e env, shipping models.OrderAddress) models.Order {
	t.Helper()

	shipping.Type = models.AddressShipping
	in := validInput()
	in.Addresses = []models.OrderAddress{
		shipping,
		{Type: models.AddressBilling, Company: "Engines Ltd", CountryCode: "TR"},
	}
	order, err := e.svc.CreateOrder(context.Background(), in)
	require.NoError(t, err)

	return order
}

// placedAddress is the address the orders below were placed with.
func placedAddress() models.OrderAddress {
	return models.OrderAddress{
		FirstName: "Ada", LastName: "Lovelace", Address1: "12 Wrong St",
		City: "Springfield", Province: "North", PostalCode: "62701", CountryCode: "TR",
		SourceAddressID: "caddr_1",
	}
}

// correctedAddress is the address the operator was told on the phone.
func correctedAddress() models.OrderAddress {
	return models.OrderAddress{
		FirstName: "Ada", LastName: "Lovelace", Address1: "12 Right St",
		City: "Springfield", Province: "North", PostalCode: "62701", CountryCode: "tr",
		Metadata: map[string]any{"gate_code": "4411"},
	}
}

// TestACorrectionClosesTheOldAddressAndWritesTheNew is the decision: the row the
// order was placed with is kept and closed, the corrected one is current, and
// the order reads the corrected one (ADR 0195).
func TestACorrectionClosesTheOldAddressAndWritesTheNew(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order := shippedTo(t, e, placedAddress())

	current, err := e.svc.CorrectShippingAddress(ctx, order.ID, correctedAddress())
	require.NoError(t, err)
	assert.Equal(t, "12 Right St", current.Address1)
	assert.Equal(t, "TR", current.CountryCode, "the country is stored in its upper-case form")
	assert.Empty(t, current.SourceAddressID, "a correction was typed, not copied from the address book")
	assert.Contains(t, e.store.lockedOrders, order.ID, "the correction runs under the order's lock")

	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	require.NotNil(t, detail.ShippingAddress)
	assert.Equal(t, "12 Right St", detail.ShippingAddress.Address1)
	assert.Equal(t, "Engines Ltd", detail.BillingAddress.Company, "the billing address is untouched")

	all, err := e.store.OrderAddressesByOrderIDs(ctx, []string{order.ID})
	require.NoError(t, err)
	var superseded []models.OrderAddress
	for _, address := range all[order.ID] {
		if !address.Current() {
			superseded = append(superseded, address)
		}
	}
	require.Len(t, superseded, 1, "the address the order was placed with is kept")
	assert.Equal(t, "12 Wrong St", superseded[0].Address1)
}

// TestASecondCorrectionClosesTheFirst keeps one current address however many
// corrections there are.
func TestASecondCorrectionClosesTheFirst(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order := shippedTo(t, e, placedAddress())

	_, err := e.svc.CorrectShippingAddress(ctx, order.ID, correctedAddress())
	require.NoError(t, err)
	again := correctedAddress()
	again.Address2 = "Flat 3"
	_, err = e.svc.CorrectShippingAddress(ctx, order.ID, again)
	require.NoError(t, err)

	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, "Flat 3", detail.ShippingAddress.Address2)

	all, err := e.store.OrderAddressesByOrderIDs(ctx, []string{order.ID})
	require.NoError(t, err)
	current := 0
	for _, address := range all[order.ID] {
		if address.Type == models.AddressShipping && address.Current() {
			current++
		}
	}
	assert.Equal(t, 1, current)
}

// TestTheSameAddressIsNotASecondCorrection writes nothing for a repeated
// request, and an empty country means the current one.
func TestTheSameAddressIsNotASecondCorrection(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order := shippedTo(t, e, placedAddress())

	same := placedAddress()
	same.CountryCode = ""
	same.SourceAddressID = ""
	current, err := e.svc.CorrectShippingAddress(ctx, order.ID, same)
	require.NoError(t, err)
	assert.Equal(t, "12 Wrong St", current.Address1)

	all, err := e.store.OrderAddressesByOrderIDs(ctx, []string{order.ID})
	require.NoError(t, err)
	for _, address := range all[order.ID] {
		assert.True(t, address.Current(), "an identical correction closed %s", address.ID)
	}
}

// TestACorrectionIsRefusedAndWritesNothing runs each refusal against an order
// whose only fault is the one the case names.
func TestACorrectionIsRefusedAndWritesNothing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(t *testing.T, e env) string
		address models.OrderAddress
		kind    errors.Kind
		code    string
	}{
		{
			name: "another country",
			prepare: func(t *testing.T, e env) string {
				return shippedTo(t, e, placedAddress()).ID
			},
			address: func() models.OrderAddress { a := correctedAddress(); a.CountryCode = "DE"; return a }(),
			kind:    errors.KindConflict, code: service.CodeAddressCountryChanged,
		},
		{
			name: "a canceled order",
			prepare: func(t *testing.T, e env) string {
				order := shippedTo(t, e, placedAddress())
				require.NoError(t, e.svc.CancelOrder(context.Background(), order.ID, "test"))
				return order.ID
			},
			address: correctedAddress(), kind: errors.KindConflict, code: service.CodeAddressNotCorrectable,
		},
		{
			name: "a completed order",
			prepare: func(t *testing.T, e env) string {
				order := shippedTo(t, e, placedAddress())
				_, err := e.svc.CompleteOrder(context.Background(), order.ID)
				require.NoError(t, err)
				return order.ID
			},
			address: correctedAddress(), kind: errors.KindConflict, code: service.CodeAddressNotCorrectable,
		},
		{
			name: "an erased order",
			prepare: func(t *testing.T, e env) string {
				order := shippedTo(t, e, placedAddress())
				erased := time.Now().UTC()
				e.store.mu.Lock()
				stored := e.store.orders[order.ID]
				stored.PersonalDataErasedAt = &erased
				e.store.orders[order.ID] = stored
				e.store.mu.Unlock()
				return order.ID
			},
			address: correctedAddress(), kind: errors.KindConflict, code: service.CodeAddressNotCorrectable,
		},
		{
			name: "an order that recorded no shipping address",
			prepare: func(t *testing.T, e env) string {
				order, err := e.svc.CreateOrder(context.Background(), validInput())
				require.NoError(t, err)
				return order.ID
			},
			address: correctedAddress(), kind: errors.KindConflict, code: service.CodeAddressMissing,
		},
		{
			name:    "an order that does not exist",
			prepare: func(*testing.T, env) string { return "order_MISSING" },
			address: correctedAddress(), kind: errors.KindNotFound, code: service.CodeOrderNotFound,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			e := newEnv(t)
			orderID := tc.prepare(t, e)
			before, err := e.store.OrderAddressesByOrderIDs(ctx, []string{orderID})
			require.NoError(t, err)

			_, err = e.svc.CorrectShippingAddress(ctx, orderID, tc.address)

			require.Error(t, err)
			assert.Equal(t, tc.kind, errors.KindOf(err))
			assert.Equal(t, tc.code, errors.CodeOf(err))
			after, err := e.store.OrderAddressesByOrderIDs(ctx, []string{orderID})
			require.NoError(t, err)
			assert.Equal(t, before, after, "a refused correction writes nothing")
		})
	}
}

// TestTheInteropReadsTheCorrectionByItsWireNames holds the body's schema, and
// refuses a field it does not name rather than saving the address without it.
func TestTheInteropReadsTheCorrectionByItsWireNames(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	interop := service.NewInterop(e.svc)
	order := shippedTo(t, e, placedAddress())

	raw, err := interop.CorrectShippingAddressJSON(ctx, order.ID, json.RawMessage(
		`{"first_name":"Ada","address_1":"12 Right St","address_2":"Flat 3","city":"Springfield",`+
			`"province":"North","postal_code":"62701","country_code":"TR","phone":"+90",`+
			`"metadata":{"gate_code":"4411"}}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"first_name":"Ada","address_1":"12 Right St","address_2":"Flat 3",`+
		`"city":"Springfield","province":"North","postal_code":"62701","country_code":"TR",`+
		`"phone":"+90","metadata":{"gate_code":"4411"}}`, string(raw))

	_, err = interop.CorrectShippingAddressJSON(ctx, order.ID,
		json.RawMessage(`{"address_1":"12 Right St","district":"North"}`))
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "%v", err)
}
