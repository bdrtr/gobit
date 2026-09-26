package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// TestACartKeepsTheOrderItAddsToAndHandsItToTheCheckout follows the field from
// the opening to the snapshot the checkout reads, by its wire name (ADR 0192).
func TestACartKeepsTheOrderItAddsToAndHandsItToTheCheckout(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	cart, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CurrencyCode: currency, CustomerID: "cust_1",
		AddsToOrderID: "order_PARENT",
	})
	require.NoError(t, err)
	assert.Equal(t, "order_PARENT", cart.AddsToOrderID)

	raw, err := service.NewInterop(svc).CartSnapshotJSON(ctx, cart.ID)
	require.NoError(t, err)

	var snapshot map[string]any
	require.NoError(t, json.Unmarshal(raw, &snapshot))
	assert.Equal(t, "order_PARENT", snapshot["adds_to_order_id"])

	plain := newCart(ctx, t, svc)
	raw, err = service.NewInterop(svc).CartSnapshotJSON(ctx, plain.ID)
	require.NoError(t, err)
	snapshot = map[string]any{}
	require.NoError(t, json.Unmarshal(raw, &snapshot))
	assert.NotContains(t, snapshot, "adds_to_order_id", "a cart that adds to nothing says nothing")
}

// TestABlankOrderIsNotAnAddition refuses a name that names nothing rather than
// storing it for the checkout to trip over.
func TestABlankOrderIsNotAnAddition(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.CreateCart(context.Background(), service.CreateCartInput{
		RegionID: regionID, CurrencyCode: currency, AddsToOrderID: "  ",
	})

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
}

// TestCartsThatDoNotAddToTheSameOrderDoNotMerge holds the merge to one order,
// in both directions and between two different orders.
func TestCartsThatDoNotAddToTheSameOrderDoNotMerge(t *testing.T) {
	ctx := context.Background()

	open := func(t *testing.T, svc *service.Service, addsTo string) string {
		t.Helper()

		cart, err := svc.CreateCart(ctx, service.CreateCartInput{
			RegionID: regionID, CurrencyCode: currency, CustomerID: "cust_1",
			AddsToOrderID: addsTo,
		})
		require.NoError(t, err)

		return cart.ID
	}

	for _, tc := range []struct {
		name           string
		source, target string
	}{
		{name: "into an addition", source: "", target: "order_A"},
		{name: "out of an addition", source: "order_A", target: ""},
		{name: "between two orders", source: "order_A", target: "order_B"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newService(t)
			source := open(t, svc, tc.source)
			fill(ctx, t, svc, source, variantA, 1)
			target := open(t, svc, tc.target)

			_, err := svc.MergeCart(ctx, source, target)

			require.Error(t, err)
			assert.Equal(t, errors.KindConflict, errors.KindOf(err))
			assert.Equal(t, service.CodeAdditionMismatch, errors.CodeOf(err))
			assert.Empty(t, quantities(ctx, t, svc, target), "a refused merge moves nothing")
		})
	}

	t.Run("two additions to one order", func(t *testing.T) {
		svc, _ := newService(t)
		source := open(t, svc, "order_A")
		fill(ctx, t, svc, source, variantA, 1)
		target := open(t, svc, "order_A")

		_, err := svc.MergeCart(ctx, source, target)
		require.NoError(t, err)
		assert.Equal(t, map[string]int64{variantA: 1}, quantities(ctx, t, svc, target))
	})
}
