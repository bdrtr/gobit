//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fulfillmentmanual "github.com/bdrtr/gobit/internal/modules/fulfillment/manual"
)

// TestAnOptionSaysHowManyDaysItTakes is ADR 0421 on the production wiring: an
// operator writes an option with the business days its delivery takes through
// the admin route, and both storefront listings, the cart's and the client-fact
// one, publish them beside the price, while an option that says none carries
// no days; the update's clear takes them off every listing.
func TestAnOptionSaysHowManyDaysItTakes(t *testing.T) {
	ctx := t.Context()
	variantID, _ := newStockedVariant(ctx, t, "E2E Delivery Days",
		map[string]int64{taxedCurrency: adminCartUnitPrice}, adminCartStock)
	cartID, _ := giftCart(t, variantID)
	profileID := newShippingProfile(ctx, t, "Delivery days profile")

	written, err := adminRequestWithBody(http.MethodPost, "/admin/v1/shipping-options", map[string]any{
		"name":                fmt.Sprintf("Courier %d", fixtureCounter.Add(1)),
		"provider_id":         fulfillmentmanual.ID,
		"shipping_profile_id": profileID,
		"amount":              1_500,
		"currency_code":       taxedCurrency,
		"region_id":           taxedRegionID,
		"delivery_days":       map[string]int{"min": 3, "max": 5},
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, written.Code, written.Body.String())
	var created struct {
		Data struct {
			ID           string          `json:"id"`
			DeliveryDays json.RawMessage `json:"delivery_days"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(written.Body.Bytes(), &created))
	courier := created.Data.ID
	assert.JSONEq(t, `{"min":3,"max":5}`, string(created.Data.DeliveryDays))
	bare := newShippingOption(ctx, t, profileID, "Pick up", 0, false)

	daysOn := func(listing []map[string]any) map[string]any {
		t.Helper()
		found := map[string]any{}
		for _, option := range listing {
			id, _ := option["id"].(string)
			if id == courier || id == bare {
				found[id] = option["delivery_days"]
			}
		}
		require.Len(t, found, 2, "both options are listed: %v", listing)
		return found
	}
	cartListing := func() []map[string]any {
		t.Helper()
		listed := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+cartID+"/shipping-options", "")
		require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
		var body struct {
			Data []map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &body))
		return body.Data
	}
	params := url.Values{}
	params.Set("region_id", taxedRegionID)
	params.Set("currency_code", taxedCurrency)
	params.Set("country_code", taxedCountry)

	want := map[string]any{"min": float64(3), "max": float64(5)}
	for name, listing := range map[string][]map[string]any{
		"the cart's listing":      cartListing(),
		"the client-fact listing": storeShippingOptions(t, params),
	} {
		days := daysOn(listing)
		assert.Equal(t, want, days[courier], "%s carries the courier's days", name)
		assert.Nil(t, days[bare], "%s: an option that says none carries none", name)
	}

	cleared, err := adminRequestWithBody(http.MethodPatch, "/admin/v1/shipping-options/"+courier,
		map[string]any{"clear_delivery_days": true})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, cleared.Code, cleared.Body.String())
	assert.Nil(t, daysOn(cartListing())[courier], "the clear takes the days off the cart's listing")
	assert.Nil(t, daysOn(storeShippingOptions(t, params))[courier], "and off the client-fact listing")
}
