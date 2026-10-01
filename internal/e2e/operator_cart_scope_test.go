//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheAdminWritesLeaveAShoppersCartAlone is D200 end to end: a cart the
// storefront opened is the shopper's, and the admin cart API changes none of
// it — not its lines, its addresses, its shipping, nor completes it for the
// total the shopper is looking at. The cart still holds what the shopper put
// in it (ADR 0299).
func TestTheAdminWritesLeaveAShoppersCartAlone(t *testing.T) {
	ctx := t.Context()
	variantID, _ := newStockedVariant(ctx, t, "E2E Shopper's Own Cart",
		map[string]int64{taxedCurrency: adminCartUnitPrice}, adminCartStock)

	opened := storefrontRequest(t, http.MethodPost, "/store/v1/carts",
		fmt.Sprintf(`{"country_code":%q,"email":"shopper@example.com"}`, taxedCountry))
	require.Equal(t, http.StatusCreated, opened.Code, "body: %s", opened.Body.String())
	cartID, _ := storefrontData(t, opened)["id"].(string)
	require.NotEmpty(t, cartID)
	added := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/line-items",
		fmt.Sprintf(`{"variant_id":%q,"quantity":1}`, variantID))
	require.Equal(t, http.StatusCreated, added.Code, "body: %s", added.Body.String())
	read := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+cartID, "")
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	total, _ := storefrontData(t, read)["total"].(float64)
	require.Positive(t, total)

	address := fmt.Sprintf(`{"first_name":"Not","last_name":"Theirs","address_1":"Elsewhere 1",`+
		`"city":"City","postal_code":"00000","country_code":%q}`, taxedCountry)
	for name, rec := range map[string]func() int{
		"line": func() int {
			return addAdminLine(t, cartID, testChannelID, variantID, 1).Code
		},
		"shipping address": func() int {
			return adminCartRequest(t, http.MethodPut, "/admin/v1/carts/"+cartID+"/shipping-address", address).Code
		},
		"billing address": func() int {
			return adminCartRequest(t, http.MethodPut, "/admin/v1/carts/"+cartID+"/billing-address", address).Code
		},
		"line removal": func() int {
			lines, _ := storefrontData(t, read)["items"].([]any)
			require.Len(t, lines, 1)
			line, _ := lines[0].(map[string]any)
			lineID, _ := line["id"].(string)
			return adminCartRequest(t, http.MethodDelete, "/admin/v1/carts/"+cartID+"/line-items/"+lineID, "").Code
		},
		"discard": func() int {
			return adminCartRequest(t, http.MethodDelete, "/admin/v1/carts/"+cartID, "").Code
		},
		"completion": func() int {
			return adminCartRequest(t, http.MethodPost, "/admin/v1/carts/"+cartID+"/complete",
				fmt.Sprintf(`{"sales_channel_id":%q,"payment_provider_id":%q,"expected_total":%d}`,
					testChannelID, offlineMethod, int64(total))).Code
		},
	} {
		assert.Equal(t, http.StatusConflict, rec(), "the admin %s write reached the shopper's cart", name)
	}

	fetched := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+cartID, "")
	require.Equal(t, http.StatusOK, fetched.Code, fetched.Body.String())
	cart := storefrontData(t, fetched)
	items, _ := cart["items"].([]any)
	require.Len(t, items, 1, "the shopper's line alone")
	line, _ := items[0].(map[string]any)
	assert.InDelta(t, 1, line["quantity"], 0, "no unit was added behind the shopper's back")
	assert.Nil(t, cart["shipping_address"], "no address was written")
	assert.Nil(t, cart["billing_address"])
	assert.Nil(t, cart["completed_at"], "the cart was not completed")
}
