//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestACounterSaleNeedsNoAddressOrDelivery is the counter sale ADR 0418
// describes, on the production wiring: an operator opens a cart in the store's
// channel, puts a line in it and completes it with an offline method, writing
// neither a shipping address nor a delivery. The order records the channel
// (ADR 0410), owes its whole total until the money is recorded (ADR 0284), has
// its stock deducted and carries no shipping method.
func TestACounterSaleNeedsNoAddressOrDelivery(t *testing.T) {
	ctx := t.Context()

	variantID, stockItemID := newStockedVariant(ctx, t, "E2E Counter Sale", map[string]int64{
		taxedCurrency: adminCartUnitPrice,
	}, adminCartStock)

	opened := adminCartRequest(t, http.MethodPost, "/admin/v1/carts",
		fmt.Sprintf(`{"country_code":%q,"sales_channel_id":%q}`, taxedCountry, testChannelID))
	require.Equal(t, http.StatusCreated, opened.Code, "body: %s", opened.Body.String())
	cartID, ok := storefrontData(t, opened)["id"].(string)
	require.True(t, ok, opened.Body.String())
	added := addAdminLine(t, cartID, testChannelID, variantID, adminCartQuantity)
	require.Equal(t, http.StatusCreated, added.Code, "body: %s", added.Body.String())

	read := adminCartRequest(t, http.MethodGet, "/admin/v1/carts/"+cartID, "")
	require.Equal(t, http.StatusOK, read.Code, "body: %s", read.Body.String())
	total, ok := storefrontData(t, read)["total"].(float64)
	require.True(t, ok, read.Body.String())
	require.Equal(t, adminCartTotal, int64(total), "the goods and their tax, no delivery")

	done := adminCartRequest(t, http.MethodPost, "/admin/v1/carts/"+cartID+"/complete",
		fmt.Sprintf(`{"sales_channel_id":%q,"payment_provider_id":%q,"expected_total":%d}`,
			testChannelID, offlineMethod, int64(total)))
	require.Equal(t, http.StatusOK, done.Code, "a counter sale completes with no address or delivery; body: %s",
		done.Body.String())
	result := storefrontData(t, done)
	assert.InDelta(t, total, result["outstanding"], 0, "the order owes its whole total until the money is recorded")
	orderID, _ := result["order_id"].(string)
	require.NotEmpty(t, orderID)

	order, err := orderSvc.GetOrder(ctx, orderID)
	require.NoError(t, err)
	assert.Equal(t, testChannelID, order.SalesChannelID, "the order records the store's channel")
	assert.Empty(t, order.ShippingMethods, "a counter sale carries no delivery")
	assert.NotEmpty(t, order.PlacedBy, "the order names the operator")
	assert.Equal(t, adminCartStock-adminCartQuantity, sellableQuantity(ctx, t, stockItemID),
		"the sale's stock is deducted")
}
