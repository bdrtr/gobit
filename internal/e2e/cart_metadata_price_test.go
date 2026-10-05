//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	pricingmodels "github.com/bdrtr/gobit/internal/modules/pricing/models"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// The prices of the metadata scenario. The arm's is the CHEAPER one on an
// override list, so a cart charged it shows the bag chose a price.
const (
	metadataBasePrice int64 = 20_000
	metadataArmPrice  int64 = 15_000
)

// TestACartsMetadataChoosesNoPrice is ADR 0403 on the production wiring (D260):
// pricing refuses a price rule under `cart.` and writes nothing; a rule written
// before the refusal stays live — the admin calculator still matches it — and
// prices no cart, even a guest's whose storefront wrote the matching bag; and a
// write that keeps the set's other prices carries that rule through unchanged.
func TestACartsMetadataChoosesNoPrice(t *testing.T) {
	ctx := t.Context()

	variantID, _ := newStockedVariant(ctx, t, "E2E Metadata Price Product", nil, 5)
	set, err := pricingSvc.CreatePriceSet(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, productSvc.SetVariantPriceSet(ctx, variantID, set.ID))
	list := activeOverrideList(ctx, t, "E2E arm price of "+variantID)

	// --- a) a new rule on the cart's bag is refused, and nothing is written ---
	_, err = pricingSvc.SetPrices(ctx, set.ID, []pricingsvc.PriceInput{
		{CurrencyCode: taxedCurrency, Amount: metadataBasePrice, MinQuantity: 1},
		{CurrencyCode: taxedCurrency, Amount: metadataArmPrice, MinQuantity: 1, PriceListID: &list,
			Rules: []pricingsvc.RuleInput{{Attribute: "cart.arm", Operator: pricingmodels.OpEq,
				Values: []string{"B"}}}},
	})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, pricingsvc.CodeRuleAttributeReserved, errors.CodeOf(err))
	written, err := pricingSvc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	assert.Empty(t, written, "a refused write writes no price")

	// --- b) a rule written before the refusal prices no cart ---
	prices, err := pricingSvc.SetPrices(ctx, set.ID, []pricingsvc.PriceInput{
		{CurrencyCode: taxedCurrency, Amount: metadataBasePrice, MinQuantity: 1},
		{CurrencyCode: taxedCurrency, Amount: metadataArmPrice, MinQuantity: 1, PriceListID: &list},
	})
	require.NoError(t, err)
	var armPriceID string
	for i := range prices {
		if prices[i].PriceListID != nil {
			armPriceID = prices[i].ID
		}
	}
	require.NotEmpty(t, armPriceID)
	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO price_rule (id, price_id, attribute, operator, rule_values)
		 VALUES ($1, $2, 'cart.arm', 'eq', ARRAY['B'])`,
		fmt.Sprintf("prule_e2e_arm_%d", fixtureCounter.Add(1)), armPriceID)
	require.NoError(t, err, "the rule an installation wrote before the refusal")

	calculated, err := adminRequestWithBody(http.MethodGet, "/admin/v1/price-sets/"+set.ID+
		"/calculate?currency_code="+taxedCurrency+"&attr_cart.arm=B", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, calculated.Code, calculated.Body.String())
	assert.InDelta(t, metadataArmPrice, storefrontData(t, calculated)["amount"], 0,
		"the rule is live: asked with the attribute, the calculator matches it")

	opened := storefrontRequest(t, http.MethodPost, "/store/v1/carts",
		fmt.Sprintf(`{"country_code":%q,"metadata":{"arm":"B"}}`, taxedCountry))
	require.Equal(t, http.StatusCreated, opened.Code, opened.Body.String())
	assert.Equal(t, map[string]any{"arm": "B"}, storefrontData(t, opened)["metadata"],
		"the storefront wrote the bag the rule names")
	cartID, _ := storefrontData(t, opened)["id"].(string)

	added := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/line-items",
		fmt.Sprintf(`{"variant_id":%q,"quantity":1}`, variantID))
	require.Equal(t, http.StatusCreated, added.Code, added.Body.String())
	assert.InDelta(t, metadataBasePrice, storefrontData(t, added)["unit_price"], 0,
		"the line is charged the base price")
	read := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+cartID, "")
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	assert.Equal(t, metadataBasePrice, cartUnitPrice(t, read), "the totals charge the base price")

	// --- c) a write keeping the set's other prices carries the old rule ---
	changed, err := pricingSvc.SetUnitBasePrices(ctx, set.ID, map[string]int64{taxedCurrency: metadataBasePrice + 1})
	require.NoError(t, err, "a keeping write is not refused for a rule it did not write")
	assert.True(t, changed)
	kept, err := pricingSvc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	var rules []pricingmodels.PriceRule
	for i := range kept {
		if kept[i].PriceListID != nil {
			rules = kept[i].Rules
		}
	}
	require.Len(t, rules, 1, "the arm's price keeps its rule; without it, it would be everybody's")
	assert.Equal(t, "cart.arm", rules[0].Attribute)
	assert.Equal(t, []string{"B"}, rules[0].Values)
}
