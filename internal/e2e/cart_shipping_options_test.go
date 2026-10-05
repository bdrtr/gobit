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

	fulfillmentmanual "github.com/bdrtr/gobit/internal/modules/fulfillment/manual"
	fulfillmentsvc "github.com/bdrtr/gobit/internal/modules/fulfillment/service"
	fulfillingwf "github.com/bdrtr/gobit/internal/workflows/fulfilling"
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

// TestAGiftCardIsNoItemToTheQuoteOrTheDeliveryChange is gap D246 on the
// production wiring (ADR 0404). A cart of one ring and two gift cards is quoted
// on one item: an option charging 700 an item lists at 700, and an option
// ruled to exactly one item is listed. The order placed keeps the card line's
// flag, and its delivery change is quoted on the same one item.
func TestAGiftCardIsNoItemToTheQuoteOrTheDeliveryChange(t *testing.T) {
	ctx := t.Context()
	customerID, email := newCustomer(ctx, t)
	ring, _ := newStockedVariant(ctx, t, "E2E Card Quote Ring", map[string]int64{
		taxedCurrency: additionUnitPrice,
	}, additionStock)
	card := newGiftCardVariant(ctx, t, 5_000)
	cartID := additionFixture{customerID: customerID, email: email, variantID: ring}.openCart(t, "")
	added := addAdminLine(t, cartID, testChannelID, card, 2)
	require.Equal(t, http.StatusCreated, added.Code, "body: %s", added.Body.String())
	address := fmt.Sprintf(`{"first_name":"Ada","address_1":"12 Main St","city":"Springfield",`+
		`"postal_code":"62701","country_code":%q}`, taxedCountry)
	rec := storefrontRequest(t, http.MethodPut, "/store/v1/carts/"+cartID+"/shipping-address", address)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	profileID := newShippingProfile(ctx, t, "Card quote profile")
	perItem, err := shippingSvc.CreateShippingOption(ctx, fulfillmentsvc.CreateOptionInput{
		Name:              fmt.Sprintf("Per item %d", fixtureCounter.Add(1)),
		ProviderID:        fulfillmentmanual.ID,
		ShippingProfileID: profileID,
		PriceType:         "calculated",
		CurrencyCode:      taxedCurrency,
		RegionID:          taxedRegionID,
		Data:              map[string]any{fulfillmentmanual.DataKeyPerItemAmount: 700},
	})
	require.NoError(t, err)
	oneItem := newShippingOption(ctx, t, profileID, "One item only", 300, false)
	for _, operator := range []string{"gte", "lte"} {
		_, err := shippingSvc.CreateShippingOptionRule(ctx, oneItem, fulfillmentsvc.CreateRuleInput{
			Attribute: fulfillmentsvc.AttrItemCount, Operator: operator, Values: []string{"1"},
		})
		require.NoError(t, err)
	}

	listed := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+cartID+"/shipping-options", "")
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	var body struct {
		Data []struct {
			ID     string `json:"id"`
			Amount int64  `json:"amount"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &body))
	listedAt := map[string]int64{}
	for _, option := range body.Data {
		listedAt[option.ID] = option.Amount
	}
	require.Contains(t, listedAt, perItem.ID, listed.Body.String())
	assert.Equal(t, int64(700), listedAt[perItem.ID], "the ring is the only item")
	assert.Contains(t, listedAt, oneItem, "the cart holds one item to the rule")

	chosen := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/shipping-methods",
		fmt.Sprintf(`{"shipping_option_id":%q}`, perItem.ID))
	require.Equal(t, http.StatusCreated, chosen.Code, chosen.Body.String())
	read := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+cartID, "")
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	total, ok := storefrontData(t, read)["total"].(float64)
	require.True(t, ok, read.Body.String())
	done := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		storefrontCompletionBody(t, int64(total)))
	require.Equal(t, http.StatusOK, done.Code, "body: %s", done.Body.String())
	orderID, _ := storefrontData(t, done)["order_id"].(string)
	require.NotEmpty(t, orderID)

	order, err := orderSvc.GetOrder(ctx, orderID)
	require.NoError(t, err)
	assert.Equal(t, int64(700), order.ShippingTotal)
	cards := 0
	for i := range order.Items {
		if order.Items[i].VariantID == card {
			cards++
			assert.True(t, order.Items[i].IsGiftcard, "the card line keeps its flag")
		}
	}
	require.Equal(t, 1, cards)

	flow, err := fulfillingwf.FromContainer(ctr)
	require.NoError(t, err)
	quotes, err := flow.QuoteDelivery(ctx, orderID)
	require.NoError(t, err)
	quotedAt := map[string]int64{}
	for _, quote := range quotes {
		quotedAt[quote.ID] = quote.Amount
	}
	require.Contains(t, quotedAt, perItem.ID)
	assert.Equal(t, int64(700), quotedAt[perItem.ID], "a delivery change is quoted on the ring alone")
	assert.Contains(t, quotedAt, oneItem, "the order holds one item to the rule")
}
