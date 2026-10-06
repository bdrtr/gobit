//go:build integration

package e2e

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ordermodels "github.com/bdrtr/gobit/internal/modules/order/models"
)

// TestAnOrderReadAtAMomentSaysWhereItWasGoing reads an order between its
// placement, its address correction (ADR 0195) and its delivery change
// (ADR 0199): at each moment the reading names the shipping address row and
// the delivery in force then (ADR 0411). Read now, it is the live order's
// current address row and the deliveries the order is on, which only this
// lane can hold it to with the real rows and their database stamps.
func TestAnOrderReadAtAMomentSaysWhereItWasGoing(t *testing.T) {
	ctx := t.Context()
	sold := spyOptionPriced(t, soldDeliveryFee, false)
	orderID, methodID := deliveryOrder(t, sold)
	pickup := spyOptionPriced(t, 1_000, true)

	placed, err := orderSvc.GetOrder(ctx, orderID)
	require.NoError(t, err)
	require.NotNil(t, placed.ShippingAddress)

	status, _, code := correctAddress(t, orderID, correctedBody())
	require.Equal(t, http.StatusOK, status, "code: %s", code)
	rec := changeDelivery(t, orderID, methodID, pickup)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	live, err := orderSvc.GetOrder(ctx, orderID)
	require.NoError(t, err)
	require.NotNil(t, live.ShippingAddress)
	require.NotEqual(t, placed.ShippingAddress.ID, live.ShippingAddress.ID, "the correction wrote a new row")
	require.Len(t, live.DeliveryChanges, 1)
	change := live.DeliveryChanges[0]

	// Now: the live order.
	now, err := orderSvc.OrderAsOf(ctx, orderID, time.Now())
	require.NoError(t, err)
	require.NotNil(t, now.ShippingAddress)
	assert.Equal(t, live.ShippingAddress.ID, now.ShippingAddress.ID, "the current row")
	require.NotNil(t, now.ShippingAddress.Address)
	assert.Equal(t, "10 Near Road", now.ShippingAddress.Address.Address1)
	current := ordermodels.CurrentDeliveries(live.ShippingMethods, live.DeliveryChanges)
	require.Len(t, now.Deliveries, len(current))
	for i := range current {
		assert.Equal(t, current[i].ShippingOptionID, now.Deliveries[i].ShippingOptionID)
		assert.Equal(t, current[i].Name, now.Deliveries[i].Name)
		assert.Equal(t, current[i].Amount, now.Deliveries[i].Amount)
	}
	assert.Equal(t, change.ID, now.Deliveries[0].ChangeID)
	assert.NotEmpty(t, now.Deliveries[0].CreditLineID, "the cheaper service wrote a credit")
	assert.Equal(t, change.CreditLineID, now.Deliveries[0].CreditLineID)

	// Before the correction: the row the order was placed with, as sold.
	beforeCorrection := live.ShippingAddress.CreatedAt.Add(-time.Microsecond)
	require.False(t, beforeCorrection.Before(live.PlacedAt), "the correction came after the placement")
	then, err := orderSvc.OrderAsOf(ctx, orderID, beforeCorrection)
	require.NoError(t, err)
	require.NotNil(t, then.ShippingAddress)
	assert.Equal(t, placed.ShippingAddress.ID, then.ShippingAddress.ID)
	require.NotNil(t, then.ShippingAddress.Address)
	assert.Equal(t, "12 Main St", then.ShippingAddress.Address.Address1)
	require.Len(t, then.Deliveries, 1)
	assert.Equal(t, sold, then.Deliveries[0].ShippingOptionID)
	assert.Empty(t, then.Deliveries[0].ChangeID)
	assert.True(t, then.Deliveries[0].Since.Equal(live.PlacedAt), "as sold since the placement")

	// Between the correction and the change, as the API publishes it.
	between := change.CreatedAt.Add(-time.Microsecond)
	require.False(t, between.Before(live.ShippingAddress.CreatedAt), "the change came after the correction")
	read := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+orderID+"/as-of?at="+
		url.QueryEscape(between.UTC().Format(time.RFC3339Nano)), "")
	require.Equal(t, http.StatusOK, read.Code, "body: %s", read.Body.String())
	data := storefrontData(t, read)

	shipping, ok := data["shipping_address"].(map[string]any)
	require.True(t, ok, "the reading names the shipping address: %s", read.Body.String())
	assert.Equal(t, live.ShippingAddress.ID, shipping["id"])
	address, ok := shipping["address"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "10 Near Road", address["address_1"], "the correction was in force")
	deliveries, ok := data["deliveries"].([]any)
	require.True(t, ok, "the reading lists the deliveries: %s", read.Body.String())
	require.Len(t, deliveries, 1)
	delivery, ok := deliveries[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, sold, delivery["shipping_option_id"], "the change came later")
	assert.NotContains(t, delivery, "change_id")
}
