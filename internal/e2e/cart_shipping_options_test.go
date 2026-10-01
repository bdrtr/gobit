//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fulfillmentsvc "github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// TestACartListsTheOptionItsSubtotalOpens is ADR 0292 on the production
// wiring. An option is free when the subtotal reaches a threshold the cart
// meets, and another when it reaches one the cart does not. The fulfillment
// module's storefront listing takes the subtotal from the client and so leaves
// out both, whatever the client claims; the cart's own listing reads the
// subtotal from the cart, offers the first and not the second, and the
// shipping method write accepts what it offered.
func TestACartListsTheOptionItsSubtotalOpens(t *testing.T) {
	ctx := t.Context()
	variantID, _ := newStockedVariant(ctx, t, "E2E Cart Shipping Options",
		map[string]int64{taxedCurrency: adminCartUnitPrice}, adminCartStock)
	cartID, _ := giftCart(t, variantID)
	profileID := newShippingProfile(ctx, t, "Cart options profile")

	ruled := func(name string, threshold int64) string {
		t.Helper()

		id := newShippingOption(ctx, t, profileID, name, 0, false)
		_, err := shippingSvc.CreateShippingOptionRule(ctx, id, fulfillmentsvc.CreateRuleInput{
			Attribute: fulfillmentsvc.AttrSubtotal, Operator: "gte",
			Values: []string{strconv.FormatInt(threshold, 10)},
		})
		require.NoError(t, err)
		return id
	}
	met := ruled("Free over the cart's subtotal", adminCartUnitPrice/2)
	unmet := ruled("Free over a fortune", 100*adminCartUnitPrice)

	listed := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+cartID+"/shipping-options", "")
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	var body struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &body))
	ids := map[string]bool{}
	for _, option := range body.Data {
		id, _ := option["id"].(string)
		ids[id] = true
	}
	assert.True(t, ids[met], "the cart meets the rule, so the option is listed")
	assert.False(t, ids[unmet], "the cart does not meet the rule")

	params := url.Values{}
	params.Set("region_id", taxedRegionID)
	params.Set("currency_code", taxedCurrency)
	params.Set("country_code", taxedCountry)
	params.Set("subtotal", strconv.FormatInt(1_000_000_000, 10))
	for _, option := range storeShippingOptions(t, params) {
		assert.NotEqual(t, met, option["id"], "the client-fact listing leaves a ruled option out")
	}

	added := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/shipping-methods",
		fmt.Sprintf(`{"shipping_option_id":%q}`, met))
	require.Equal(t, http.StatusCreated, added.Code, "what the cart listed, the write accepts: %s",
		added.Body.String())
}

// TestAnOperatorChoosesAnAdminOnlyOption is ADR 0295 on the production
// wiring: an admin-only option is listed and accepted through the operator's
// doors, and neither listed nor accepted through the storefront's, on the
// same cart.
func TestAnOperatorChoosesAnAdminOnlyOption(t *testing.T) {
	ctx := t.Context()
	variantID, _ := newStockedVariant(ctx, t, "E2E Admin Only Shipping",
		map[string]int64{taxedCurrency: adminCartUnitPrice}, adminCartStock)
	cartID := openAdminCartID(t)
	added := addAdminLine(t, cartID, testChannelID, variantID, 1)
	require.Equal(t, http.StatusCreated, added.Code, added.Body.String())
	desk := newShippingOption(ctx, t, newShippingProfile(ctx, t, "Desk profile"), "Collect at the desk", 0, true)

	adminList := adminCartRequest(t, http.MethodGet, "/admin/v1/carts/"+cartID+"/shipping-options", "")
	require.Equal(t, http.StatusOK, adminList.Code, adminList.Body.String())
	assert.Contains(t, adminList.Body.String(), desk, "the operator sees the admin-only option")
	storeList := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+cartID+"/shipping-options", "")
	require.Equal(t, http.StatusOK, storeList.Code, storeList.Body.String())
	assert.NotContains(t, storeList.Body.String(), desk, "the shopper does not")

	body := fmt.Sprintf(`{"shipping_option_id":%q}`, desk)
	byShopper := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/shipping-methods", body)
	assert.Equal(t, http.StatusUnprocessableEntity, byShopper.Code, byShopper.Body.String())
	byOperator := adminCartRequest(t, http.MethodPost, "/admin/v1/carts/"+cartID+"/shipping-methods", body)
	assert.Equal(t, http.StatusCreated, byOperator.Code, byOperator.Body.String())
}
