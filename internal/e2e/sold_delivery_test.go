//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file proves ADR 0198 on the production wiring: the delivery a shopper
// chose and paid for survives the checkout into the order, and a parcel opened
// without naming an option goes on it.

// soldDeliveryFee is what the spy option charges, and is not taxed.
const soldDeliveryFee int64 = 3_000

// TestAnOrderRemembersTheDeliveryItWasSold checks out a cart with a shipping
// method on a storefront-visible spy option, reads it back on the order, and
// opens a parcel naming no option.
func TestAnOrderRemembersTheDeliveryItWasSold(t *testing.T) {
	ctx := t.Context()
	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Delivered", map[string]int64{
		taxedCurrency: additionUnitPrice,
	}, additionStock)
	optionID := spyOptionPriced(t, soldDeliveryFee, false)

	f := additionFixture{customerID: customerID, email: email, variantID: variantID}
	cartID := f.openCart(t, "")
	address := fmt.Sprintf(`{"first_name":"Ada","address_1":"12 Main St","city":"Springfield",`+
		`"postal_code":"62701","country_code":%q}`, taxedCountry)
	rec := storefrontRequest(t, http.MethodPut, "/store/v1/carts/"+cartID+"/shipping-address", address)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	rec = storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/shipping-methods",
		fmt.Sprintf(`{"shipping_option_id":%q}`, optionID))
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	done := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		storefrontCompletionBody(t, additionTotal+soldDeliveryFee))
	require.Equal(t, http.StatusOK, done.Code, "body: %s", done.Body.String())
	orderID, _ := storefrontData(t, done)["order_id"].(string)
	require.NotEmpty(t, orderID)

	read := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+orderID, "")
	require.Equal(t, http.StatusOK, read.Code, "body: %s", read.Body.String())
	order := storefrontData(t, read)
	methods, ok := order["shipping_methods"].([]any)
	require.True(t, ok, "body: %s", read.Body.String())
	require.Len(t, methods, 1)
	method, ok := methods[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, optionID, method["shipping_option_id"])
	assert.NotEmpty(t, method["name"])
	assert.InDelta(t, float64(soldDeliveryFee), method["amount"], 0)
	assert.InDelta(t, float64(soldDeliveryFee), order["shipping_total"], 0,
		"the methods add up to the shipping total")

	key := fmt.Sprintf("sold-delivery-%d", fixtureCounter.Add(1))
	opened, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+orderID+"/fulfillments",
		map[string]any{"idempotency_key": key})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, opened.Code, "body: %s", opened.Body.String())

	handed, ok := carrierSpy.shipmentFor(key)
	require.True(t, ok, "the parcel never reached the carrier")
	assert.Equal(t, optionID, handed.OptionID,
		"a parcel opened without an option goes on the one the shopper paid for")
}
