//go:build integration

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/auth/models"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
	promotionmodels "github.com/bdrtr/gobit/internal/modules/promotion/models"
	promotionsvc "github.com/bdrtr/gobit/internal/modules/promotion/service"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// TestACartsMetadataMeetsNoPromotion is ADR 0407 on the production wiring
// (D261): promotion refuses a new rule under `cart.` and writes nothing; a rule
// written before the refusal stays live — the admin computation still matches
// it — and holds on no cart, even a guest's whose storefront wrote the matching
// bag; and the attribute the server decides, the sales channel a key opens its
// carts in, carries the case the bag was for, which a bag claiming that channel
// does not.
//
// Each part has its own variant and promotion, so a red in one does not hide
// the others.
func TestACartsMetadataMeetsNoPromotion(t *testing.T) {
	t.Run("a new rule on the bag is refused", func(t *testing.T) {
		ctx := t.Context()
		variantID := newVariant(ctx, t, "E2E Bag Refused", map[string]int64{taxedCurrency: couponUnitPrice})
		promotionID := newTargetedPromotion(ctx, t, variantID)

		rec, err := adminRequestWithBody(http.MethodPost, "/admin/v1/promotions/"+promotionID+"/rules",
			map[string]any{"rule_type": "context", "attribute": "cart.brand", "operator": "eq", "values": []string{"acme"}})
		require.NoError(t, err)

		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		assert.Equal(t, "promotion_rule_attribute_reserved", errorCode(t, rec))
		rules, err := promotionSvc.ListPromotionRules(ctx, promotionID)
		require.NoError(t, err)
		require.Len(t, rules, 1, "only the target rule is written")
		assert.Equal(t, promotionmodels.RuleTarget, rules[0].RuleType)
	})

	t.Run("an old rule meets no cart", func(t *testing.T) {
		ctx := t.Context()
		variantID := newVariant(ctx, t, "E2E Bag Old Rule", map[string]int64{taxedCurrency: couponUnitPrice})
		promotionID := newTargetedPromotion(ctx, t, variantID)
		brand := fmt.Sprintf("brand-%d", fixtureCounter.Add(1))
		_, err := testPool.Pool().Exec(ctx,
			`INSERT INTO promotion_rule (id, promotion_id, rule_type, attribute, operator, rule_values)
			 VALUES ($1, $2, 'context', 'cart.brand', 'eq', ARRAY[$3])`,
			fmt.Sprintf("prule_e2e_bag_%d", fixtureCounter.Add(1)), promotionID, brand)
		require.NoError(t, err, "the rule an installation wrote before the refusal")

		opened := storefrontRequest(t, http.MethodPost, "/store/v1/carts",
			fmt.Sprintf(`{"country_code":%q,"metadata":{"brand":%q}}`, taxedCountry, brand))
		require.Equal(t, http.StatusCreated, opened.Code, opened.Body.String())
		assert.Equal(t, map[string]any{"brand": brand}, storefrontData(t, opened)["metadata"],
			"the storefront wrote the bag the rule names, and reads it back")
		cartID, _ := storefrontData(t, opened)["id"].(string)
		added := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/line-items",
			fmt.Sprintf(`{"variant_id":%q,"quantity":1}`, variantID))
		require.Equal(t, http.StatusCreated, added.Code, added.Body.String())
		read := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+cartID, "")
		require.Equal(t, http.StatusOK, read.Code, read.Body.String())
		assert.InDelta(t, 0, storefrontData(t, read)["discount_total"], 0, "the bag meets no promotion")

		computed, err := adminRequestWithBody(http.MethodPost, "/admin/v1/promotions/compute", map[string]any{
			"currency_code": taxedCurrency,
			"context":       map[string]string{"cart.brand": brand},
			"items": []map[string]any{{
				"id": "li_bag", "amount": couponUnitPrice, "unit_amount": couponUnitPrice, "quantity": 1,
				"attributes": map[string]string{attrVariantID: variantID},
			}},
		})
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, computed.Code, computed.Body.String())
		applied, _ := storefrontData(t, computed)["applied"].([]any)
		var ids []string
		for _, raw := range applied {
			if entry, ok := raw.(map[string]any); ok {
				id, _ := entry["promotion_id"].(string)
				ids = append(ids, id)
			}
		}
		assert.Contains(t, ids, promotionID,
			"the rule is live: asked with the attribute, the computation matches it")
	})

	t.Run("the channel carries the brand", func(t *testing.T) {
		ctx := t.Context()
		variantID := newVariant(ctx, t, "E2E Channel Brand", map[string]int64{taxedCurrency: couponUnitPrice})
		channel, err := authSvc.CreateSalesChannel(ctx, authsvc.SalesChannelInput{
			Name: "E2E Brand Channel " + t.Name(), Description: "one brand's storefront",
		})
		require.NoError(t, err)
		_, channelKey, err := authSvc.CreateAPIKey(ctx, authsvc.CreateAPIKeyInput{
			Type: models.APIKeyPublishable, Title: "e2e brand channel key", CreatedBy: adminID,
			SalesChannelIDs: []string{channel.ID},
		})
		require.NoError(t, err)
		newContextRuledPromotion(ctx, t, couponRateBps, cartwf.AttrSalesChannelID, channel.ID, []string{variantID})

		branded := openCartWithKey(t, channelKey)
		added := tryAddLineItem(t, channelKey, branded, variantID)
		require.Equal(t, http.StatusCreated, added.Code, added.Body.String())
		read := keyedStorefrontRequest(t, channelKey, http.MethodGet, "/store/v1/carts/"+branded, "")
		require.Equal(t, http.StatusOK, read.Code, read.Body.String())
		assert.InDelta(t, couponDiscount, storefrontData(t, read)["discount_total"], 0,
			"a cart its brand's key opened meets the promotion")

		opened := storefrontRequest(t, http.MethodPost, "/store/v1/carts",
			fmt.Sprintf(`{"country_code":%q,"metadata":{%q:%q}}`, taxedCountry, cartwf.AttrSalesChannelID, channel.ID))
		require.Equal(t, http.StatusCreated, opened.Code, opened.Body.String())
		claimed, _ := storefrontData(t, opened)["id"].(string)
		added = storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+claimed+"/line-items",
			fmt.Sprintf(`{"variant_id":%q,"quantity":1}`, variantID))
		require.Equal(t, http.StatusCreated, added.Code, added.Body.String())
		read = storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+claimed, "")
		require.Equal(t, http.StatusOK, read.Code, read.Body.String())
		assert.InDelta(t, 0, storefrontData(t, read)["discount_total"], 0,
			"a bag claiming the channel is not the channel")
	})
}

// newTargetedPromotion writes an automatic promotion taking couponRateBps off
// the given variants, with no context rule.
func newTargetedPromotion(ctx context.Context, t *testing.T, variantID string) string {
	t.Helper()

	promotion, err := promotionSvc.CreatePromotion(ctx, promotionsvc.PromotionInput{
		Code:        fmt.Sprintf("E2E-BAG-%d", fixtureCounter.Add(1)),
		IsAutomatic: true,
		Status:      promotionmodels.PromotionActive,
	})
	require.NoError(t, err)
	_, err = promotionSvc.SetApplicationMethod(ctx, promotion.ID, promotionsvc.ApplicationMethodInput{
		Type:       promotionmodels.MethodPercentage,
		TargetType: promotionmodels.TargetItems,
		Allocation: promotionmodels.AllocationEach,
		Value:      couponRateBps,
	})
	require.NoError(t, err)
	_, err = promotionSvc.AddPromotionRule(ctx, promotion.ID, promotionsvc.RuleInput{
		RuleType:  promotionmodels.RuleTarget,
		Attribute: attrVariantID,
		Operator:  promotionmodels.OpIn,
		Values:    []string{variantID},
	})
	require.NoError(t, err)

	return promotion.ID
}
