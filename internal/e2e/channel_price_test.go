//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/auth/models"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
	pricingmodels "github.com/bdrtr/gobit/internal/modules/pricing/models"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// The prices of the channel-price scenario. The channel's is the DEARER one,
// so a cart priced at it shows that the more specific rule won and not the
// cheaper price (ADR 0397).
const (
	channelPriceRegion  int64 = 800
	channelPriceChannel int64 = 1_000
)

// cartUnitPrice reads the unit price of the cart's single line off the cart
// the response carries.
func cartUnitPrice(t *testing.T, rec *httptest.ResponseRecorder) int64 {
	t.Helper()

	items, ok := storefrontData(t, rec)["items"].([]any)
	require.True(t, ok, "the cart carries its lines; body: %s", rec.Body.String())
	require.Len(t, items, 1, "body: %s", rec.Body.String())
	line, ok := items[0].(map[string]any)
	require.True(t, ok)
	price, ok := line["unit_price"].(float64)
	require.True(t, ok, "body: %s", rec.Body.String())

	return int64(price)
}

// TestACartIsPricedInTheChannelItWasOpenedIn is ADR 0397 on the production
// wiring: the publishable key's channel is recorded on the cart and reaches
// pricing's rule context; a key bound to two channels and an operator who names
// none price the cart at the region's price; an operator who names the channel
// prices it at the channel's, and an address write carrying no channel keeps
// it, so the completion meets the total that was read.
func TestACartIsPricedInTheChannelItWasOpenedIn(t *testing.T) {
	ctx := t.Context()

	channelA, err := authSvc.CreateSalesChannel(ctx, authsvc.SalesChannelInput{
		Name: "E2E Channel Price A " + t.Name(), Description: "a storefront with prices of its own",
	})
	require.NoError(t, err)
	channelB, err := authSvc.CreateSalesChannel(ctx, authsvc.SalesChannelInput{
		Name: "E2E Channel Price B " + t.Name(), Description: "a second storefront",
	})
	require.NoError(t, err)
	_, oneKey, err := authSvc.CreateAPIKey(ctx, authsvc.CreateAPIKeyInput{
		Type: models.APIKeyPublishable, Title: "e2e channel price key", CreatedBy: adminID,
		SalesChannelIDs: []string{channelA.ID},
	})
	require.NoError(t, err)
	_, twoKey, err := authSvc.CreateAPIKey(ctx, authsvc.CreateAPIKeyInput{
		Type: models.APIKeyPublishable, Title: "e2e two channel key", CreatedBy: adminID,
		SalesChannelIDs: []string{channelA.ID, channelB.ID},
	})
	require.NoError(t, err)

	// The product is assigned to no channel, so every storefront sells it and
	// the only thing that tells the two prices apart is the cart's channel.
	variantID, _ := newStockedVariant(ctx, t, "E2E Channel Price Product", nil, 10)
	inRegion := pricingsvc.RuleInput{
		Attribute: "region_id", Operator: pricingmodels.OpEq, Values: []string{taxedRegionID},
	}
	set, err := pricingSvc.CreatePriceSet(ctx, []pricingsvc.PriceInput{
		{CurrencyCode: taxedCurrency, Amount: channelPriceRegion, MinQuantity: 1,
			Rules: []pricingsvc.RuleInput{inRegion}},
		{CurrencyCode: taxedCurrency, Amount: channelPriceChannel, MinQuantity: 1,
			// The rule is written the way an operator writes it, by the
			// published name rather than the constant.
			Rules: []pricingsvc.RuleInput{inRegion, {
				Attribute: "sales_channel_id", Operator: pricingmodels.OpEq,
				Values: []string{channelA.ID},
			}}},
	})
	require.NoError(t, err)
	require.NoError(t, productSvc.SetVariantPriceSet(ctx, variantID, set.ID))

	// --- a) a key bound to one channel: the cart names it and is priced in it ---
	oneCart := keyedStorefrontRequest(t, oneKey, http.MethodPost, "/store/v1/carts",
		fmt.Sprintf(`{"country_code":%q}`, taxedCountry))
	require.Equal(t, http.StatusCreated, oneCart.Code, oneCart.Body.String())
	assert.Equal(t, channelA.ID, storefrontData(t, oneCart)["sales_channel_id"])
	oneID, _ := storefrontData(t, oneCart)["id"].(string)
	require.Equal(t, http.StatusCreated, tryAddLineItem(t, oneKey, oneID, variantID).Code)
	assert.Equal(t, channelPriceChannel, cartUnitPrice(t,
		keyedStorefrontRequest(t, oneKey, http.MethodGet, "/store/v1/carts/"+oneID, "")),
		"the channel's price, though dearer: it names one more rule")

	// --- b) a key bound to two channels: no channel, the region's price ---
	twoCart := keyedStorefrontRequest(t, twoKey, http.MethodPost, "/store/v1/carts",
		fmt.Sprintf(`{"country_code":%q}`, taxedCountry))
	require.Equal(t, http.StatusCreated, twoCart.Code, twoCart.Body.String())
	assert.NotContains(t, storefrontData(t, twoCart), "sales_channel_id")
	twoID, _ := storefrontData(t, twoCart)["id"].(string)
	require.Equal(t, http.StatusCreated, tryAddLineItem(t, twoKey, twoID, variantID).Code)
	assert.Equal(t, channelPriceRegion, cartUnitPrice(t,
		keyedStorefrontRequest(t, twoKey, http.MethodGet, "/store/v1/carts/"+twoID, "")))

	// --- c) an operator who names the channel: its price, kept by every write ---
	named := adminCartRequest(t, http.MethodPost, "/admin/v1/carts",
		fmt.Sprintf(`{"country_code":%q,"sales_channel_id":%q}`, taxedCountry, channelA.ID))
	require.Equal(t, http.StatusCreated, named.Code, named.Body.String())
	assert.Equal(t, channelA.ID, storefrontData(t, named)["sales_channel_id"])
	namedID, _ := storefrontData(t, named)["id"].(string)
	require.Equal(t, http.StatusCreated, addAdminLine(t, namedID, channelA.ID, variantID, 1).Code)
	addressed := adminCartRequest(t, http.MethodPut, "/admin/v1/carts/"+namedID+"/shipping-address",
		fmt.Sprintf(`{"first_name":"Tele","last_name":"Phone","address_1":"Street 1",`+
			`"city":"City","postal_code":"00000","country_code":%q}`, taxedCountry))
	require.Equal(t, http.StatusOK, addressed.Code, addressed.Body.String())
	optionID := newShippingOption(ctx, t, newShippingProfile(ctx, t, "Channel price profile"),
		"Channel price delivery", 4_900, false)
	shipped := adminCartRequest(t, http.MethodPost, "/admin/v1/carts/"+namedID+"/shipping-methods",
		fmt.Sprintf(`{"shipping_option_id":%q}`, optionID))
	require.Equal(t, http.StatusCreated, shipped.Code, shipped.Body.String())
	read := adminCartRequest(t, http.MethodGet, "/admin/v1/carts/"+namedID, "")
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	assert.Equal(t, channelPriceChannel, cartUnitPrice(t, read),
		"an address write carrying no channel reprices the cart in the cart's channel")
	total, ok := storefrontData(t, read)["total"].(float64)
	require.True(t, ok, read.Body.String())
	done := adminCartRequest(t, http.MethodPost, "/admin/v1/carts/"+namedID+"/complete",
		fmt.Sprintf(`{"sales_channel_id":%q,"payment_provider_id":%q,"expected_total":%d}`,
			channelA.ID, offlineMethod, int64(total)))
	require.Equal(t, http.StatusOK, done.Code,
		"the completion meets the total the operator read; body: %s", done.Body.String())

	// --- d) an operator who names none: the region's price ---
	unnamed := openAdminCartID(t)
	require.Equal(t, http.StatusCreated, addAdminLine(t, unnamed, channelA.ID, variantID, 1).Code)
	assert.Equal(t, channelPriceRegion, cartUnitPrice(t,
		adminCartRequest(t, http.MethodGet, "/admin/v1/carts/"+unnamed, "")),
		"the line's channel scopes the catalog and does not price the cart")
}
