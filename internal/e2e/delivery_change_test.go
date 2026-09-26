//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file proves ADR 0199 on the production wiring: a delivery is put on
// another service at the fulfillment module's price for the order, a cheaper
// one is written off and booked against shipping, and a parcel opened
// afterwards goes on the new service.

// deliveryOrder checks out an order on the given storefront option and returns
// it with its shipping method's id.
func deliveryOrder(t *testing.T, optionID string) (orderID, methodID string) {
	t.Helper()

	ctx := t.Context()
	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Rerouted", map[string]int64{
		taxedCurrency: additionUnitPrice,
	}, additionStock)
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
	orderID, _ = storefrontData(t, done)["order_id"].(string)
	require.NotEmpty(t, orderID)

	method := onlyShippingMethod(t, orderID)
	methodID, _ = method["id"].(string)
	require.NotEmpty(t, methodID)

	return orderID, methodID
}

// onlyShippingMethod reads the order's one shipping method off the admin
// record.
func onlyShippingMethod(t *testing.T, orderID string) map[string]any {
	t.Helper()

	read := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+orderID, "")
	require.Equal(t, http.StatusOK, read.Code, "body: %s", read.Body.String())
	methods, ok := storefrontData(t, read)["shipping_methods"].([]any)
	require.True(t, ok, "body: %s", read.Body.String())
	require.Len(t, methods, 1)
	method, ok := methods[0].(map[string]any)
	require.True(t, ok)

	return method
}

// changeDelivery puts the order's method on the option.
func changeDelivery(t *testing.T, orderID, methodID, optionID string) *httptest.ResponseRecorder {
	t.Helper()

	return adminCartRequest(t, http.MethodPut,
		"/admin/v1/orders/"+orderID+"/shipping-methods/"+methodID,
		fmt.Sprintf(`{"shipping_option_id":%q}`, optionID))
}

// TestADeliveryIsPutOnACheaperService is the decision end to end.
func TestADeliveryIsPutOnACheaperService(t *testing.T) {
	from := time.Now().UTC().Add(-time.Second)
	sold := spyOptionPriced(t, soldDeliveryFee, false)
	orderID, methodID := deliveryOrder(t, sold)
	pickup := spyOptionPriced(t, 1_000, true)
	sameDay := spyOptionPriced(t, soldDeliveryFee+1, false)

	rec := changeDelivery(t, orderID, methodID, sameDay)
	require.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), "order_delivery_costs_more")

	rec = changeDelivery(t, orderID, methodID, "sopt_nowhere")
	require.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), "fulfilling_option_unavailable")

	rec = changeDelivery(t, orderID, methodID, pickup)
	require.Equal(t, http.StatusOK, rec.Code, "an admin-only option is the operator's to choose; body: %s",
		rec.Body.String())
	rec = changeDelivery(t, orderID, methodID, pickup)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	method := onlyShippingMethod(t, orderID)
	assert.Equal(t, sold, method["shipping_option_id"], "the method keeps what the order was sold")
	changes, ok := method["changes"].([]any)
	require.True(t, ok)
	require.Len(t, changes, 1, "the same option twice is one change")
	change, ok := changes[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, pickup, change["shipping_option_id"])
	assert.InDelta(t, 1_000, change["amount"], 0, "the price is the fulfillment module's")
	assert.InDelta(t, -2_000, change["difference"], 0)
	changeID, _ := change["id"].(string)
	require.NotEmpty(t, changeID)

	credits := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+orderID+"/credit-lines", "")
	require.Equal(t, http.StatusOK, credits.Code, "body: %s", credits.Body.String())
	assert.Contains(t, credits.Body.String(), `"delivery_change"`)
	assert.Contains(t, credits.Body.String(), changeID, "the credit names the change")

	for surface, timeline := range map[string]*httptest.ResponseRecorder{
		"admin":      adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+orderID+"/timeline", ""),
		"storefront": storefrontRequest(t, http.MethodGet, "/store/v1/orders/"+orderID+"/timeline", ""),
	} {
		require.Equal(t, http.StatusOK, timeline.Code, "%s: %s", surface, timeline.Body.String())
		assert.Contains(t, timeline.Body.String(), `"order.delivery_changed"`, surface)
	}

	query := url.Values{
		"from":          {from.Format(time.RFC3339)},
		"to":            {time.Now().UTC().Add(time.Minute).Format(time.RFC3339)},
		"currency_code": {taxedCurrency},
	}
	books := adminCartRequest(t, http.MethodGet, "/admin/v1/order-journal?"+query.Encode(), "")
	require.Equal(t, http.StatusOK, books.Code, "body: %s", books.Body.String())
	var journal struct {
		Data struct {
			Entries []struct {
				ID    string `json:"id"`
				Kind  string `json:"kind"`
				Lines []struct {
					Account string `json:"account"`
					Debit   int64  `json:"debit"`
					Credit  int64  `json:"credit"`
				} `json:"lines"`
			} `json:"entries"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(books.Body.Bytes(), &journal))
	found := false
	for _, entry := range journal.Data.Entries {
		if entry.ID != changeID {
			continue
		}
		found = true
		assert.Equal(t, "delivery_changed", entry.Kind)
		require.Len(t, entry.Lines, 2)
		assert.Equal(t, "shipping", entry.Lines[0].Account)
		assert.Equal(t, int64(2_000), entry.Lines[0].Debit)
	}
	assert.True(t, found, "the change is on the books; body: %s", books.Body.String())

	key := fmt.Sprintf("delivery-change-%d", fixtureCounter.Add(1))
	opened, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+orderID+"/fulfillments",
		map[string]any{"idempotency_key": key})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, opened.Code, "body: %s", opened.Body.String())
	handed, ok := carrierSpy.shipmentFor(key)
	require.True(t, ok, "the parcel never reached the carrier")
	assert.Equal(t, pickup, handed.OptionID, "a parcel opened now goes on the new service")

	rec = changeDelivery(t, orderID, methodID, sold)
	require.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), "fulfilling_parcel_underway")
}
