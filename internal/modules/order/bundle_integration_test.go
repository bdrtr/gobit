//go:build integration

package order_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestABundleLineKeepsWhatItWasMadeOf is ADR 0235's column on the real schema:
// a line that sold a bundle keeps its components in the order given, the
// interop answer the put-back acts read carries them, and a line of its own
// reads none.
func TestABundleLineKeepsWhatItWasMadeOf(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	in := validInput()
	in.CartID = "cart_BUNDLE"
	box := in.Items[0]
	box.VariantID = "variant_box"
	box.Components = []service.CreateOrderLineComponentInput{
		{VariantID: "variant_towel", Quantity: 1},
		{VariantID: "variant_soap", Quantity: 2},
	}
	plain := in.Items[0]
	in.Items = []service.CreateOrderItemInput{box, plain}
	in.Subtotal += plain.Subtotal
	in.TaxTotal += plain.TaxTotal
	in.Total += plain.Total

	ord, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)
	detail, err := svc.GetOrder(ctx, ord.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 2)
	assert.Equal(t, []models.OrderLineComponent{
		{VariantID: "variant_towel", Quantity: 1}, {VariantID: "variant_soap", Quantity: 2},
	}, detail.Items[0].Components)
	assert.Nil(t, detail.Items[1].Components, "a line of its own is made of nothing")

	raw, err := service.NewInterop(svc).DispatchableLinesJSON(ctx, ord.ID)
	require.NoError(t, err)
	var lines []struct {
		LineItemID string          `json:"line_item_id"`
		Components json.RawMessage `json:"components"`
	}
	require.NoError(t, json.Unmarshal(raw, &lines))
	require.Len(t, lines, 2)
	assert.JSONEq(t, `[{"variant_id":"variant_towel","quantity":1},{"variant_id":"variant_soap","quantity":2}]`,
		string(lines[0].Components))
	assert.Empty(t, lines[1].Components, "the key is absent on a line of its own")

	var stored string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT components::text FROM order_line_items WHERE id = $1`, detail.Items[1].ID).Scan(&stored))
	assert.Equal(t, "[]", stored, "none is an empty array, never NULL")
}

// TestAnOrderLinesComponentsAreAnArray is the column's CHECK, witnessed in raw
// SQL: the service writes an array, and the database refuses anything else.
func TestAnOrderLinesComponentsAreAnArray(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	in := validInput()
	in.CartID = "cart_BUNDLE_CHECK"
	ord, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)
	detail, err := svc.GetOrder(ctx, ord.ID)
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`UPDATE order_line_items SET components = '{"variant_id":"v","quantity":1}'::jsonb WHERE id = $1`,
		detail.Items[0].ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `check constraint "order_line_items_components_is_array"`)
}
