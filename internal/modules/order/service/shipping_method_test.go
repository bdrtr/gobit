package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// soldExpress is validInput sold one delivery that is its whole shipping total.
func soldExpress() service.CreateOrderInput {
	in := validInput()
	in.ShippingMethods = []service.CreateShippingMethodInput{
		{ShippingOptionID: "so_express", Name: "  Next day  ", Amount: in.ShippingTotal},
	}

	return in
}

// TestAnOrderRemembersTheDeliveryItWasSold writes the methods with the order
// and reads them back with it (ADR 0198).
func TestAnOrderRemembersTheDeliveryItWasSold(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, soldExpress())
	require.NoError(t, err)

	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	require.Len(t, detail.ShippingMethods, 1)
	assert.Equal(t, "so_express", detail.ShippingMethods[0].ShippingOptionID)
	assert.Equal(t, "Next day", detail.ShippingMethods[0].Name, "the name is stored trimmed")
	assert.Equal(t, order.ShippingTotal, detail.ShippingMethods[0].Amount)

	plain, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	detail, err = e.svc.GetOrder(ctx, plain.ID)
	require.NoError(t, err)
	assert.Empty(t, detail.ShippingMethods, "an order sold no method records none")
}

// TestTheDeliveriesAddUpToTheShippingTotal refuses methods that do not, and a
// method with no name, and writes no order.
func TestTheDeliveriesAddUpToTheShippingTotal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(in *service.CreateOrderInput)
	}{
		{"a method short of the total", func(in *service.CreateOrderInput) { in.ShippingMethods[0].Amount-- }},
		{"a second method past it", func(in *service.CreateOrderInput) {
			in.ShippingMethods = append(in.ShippingMethods,
				service.CreateShippingMethodInput{Name: "Gift wrap", Amount: 1})
		}},
		{"a method with no name", func(in *service.CreateOrderInput) { in.ShippingMethods[0].Name = "  " }},
		{"a negative amount", func(in *service.CreateOrderInput) {
			in.ShippingMethods = append(in.ShippingMethods,
				service.CreateShippingMethodInput{Name: "Refund", Amount: -1})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			e := newEnv(t)
			in := soldExpress()
			tc.change(&in)

			_, err := e.svc.CreateOrder(ctx, in)

			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "%v", err)
			listed, err := e.svc.ListOrders(ctx, service.ListOrdersInput{})
			require.NoError(t, err)
			assert.Zero(t, listed.Count)
		})
	}
}

// TestTheSnapshotCarriesTheDeliveriesByTheirWireNames holds the checkout's
// field names, and the one-option answer a parcel defaults to.
func TestTheSnapshotCarriesTheDeliveriesByTheirWireNames(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	interop := service.NewInterop(e.svc)

	place := func(key string, methods []map[string]any) string {
		t.Helper()

		var body map[string]any
		require.NoError(t, json.Unmarshal([]byte(snapshotJSON), &body))
		body["idempotency_key"] = key
		body["shipping_methods"] = methods
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		orderID, err := interop.PlaceOrderJSON(ctx, raw)
		require.NoError(t, err)

		return orderID
	}

	one := place("wf_ONE", []map[string]any{
		{"shipping_option_id": "so_express", "name": "Next day", "amount": 2500},
	})
	detail, err := e.svc.GetOrder(ctx, one)
	require.NoError(t, err)
	require.Len(t, detail.ShippingMethods, 1)
	assert.Equal(t, "so_express", detail.ShippingMethods[0].ShippingOptionID)
	sold, err := interop.SoldShippingOptionOf(ctx, one)
	require.NoError(t, err)
	assert.Equal(t, "so_express", sold)

	two := place("wf_TWO", []map[string]any{
		{"shipping_option_id": "so_a", "name": "Part one", "amount": 1000},
		{"shipping_option_id": "so_b", "name": "Part two", "amount": 1500},
	})
	sold, err = interop.SoldShippingOptionOf(ctx, two)
	require.NoError(t, err)
	assert.Empty(t, sold, "two deliveries leave the choice to whoever opens the parcel")

	none := place("wf_NONE", nil)
	sold, err = interop.SoldShippingOptionOf(ctx, none)
	require.NoError(t, err)
	assert.Empty(t, sold)
}
