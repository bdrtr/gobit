//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/auth/models"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
	promotionmodels "github.com/bdrtr/gobit/internal/modules/promotion/models"
	promotionsvc "github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// orderChannelPrice is the unit price of the order channel scenario; the
// promotion tried on it takes 10 percent, trialRateBps.
const orderChannelPrice int64 = 5_000

// TestAnOrderRecordsTheChannelItsCartWasOpenedIn is ADR 0410 on the production
// wiring. A cart opened through a key bound to one channel becomes an order
// that records it; a cart opened through a key bound to two names none, and
// neither does its order, though the completing request carried both channels.
// The admin record and the admin list's filter publish the channel, and a
// promotion ruled on it is tried on the order placed in it and not on the other.
func TestAnOrderRecordsTheChannelItsCartWasOpenedIn(t *testing.T) {
	ctx := t.Context()
	from := time.Now().Add(-time.Second)

	channel, err := authSvc.CreateSalesChannel(ctx, authsvc.SalesChannelInput{
		Name: "E2E Order Channel " + t.Name(), Description: "a storefront whose orders are told apart",
	})
	require.NoError(t, err)
	other, err := authSvc.CreateSalesChannel(ctx, authsvc.SalesChannelInput{
		Name: "E2E Order Channel Other " + t.Name(), Description: "a second storefront",
	})
	require.NoError(t, err)
	_, oneKey, err := authSvc.CreateAPIKey(ctx, authsvc.CreateAPIKeyInput{
		Type: models.APIKeyPublishable, Title: "e2e order channel key", CreatedBy: adminID,
		SalesChannelIDs: []string{channel.ID},
	})
	require.NoError(t, err)
	_, twoKey, err := authSvc.CreateAPIKey(ctx, authsvc.CreateAPIKeyInput{
		Type: models.APIKeyPublishable, Title: "e2e order two channel key", CreatedBy: adminID,
		SalesChannelIDs: []string{channel.ID, other.ID},
	})
	require.NoError(t, err)
	variantID, _ := newStockedVariant(ctx, t, "E2E Order Channel Product",
		map[string]int64{taxedCurrency: orderChannelPrice}, 10)

	place := func(key string) string {
		t.Helper()
		cartID := openCartWithKey(t, key)
		require.Equal(t, http.StatusCreated, tryAddLineItem(t, key, cartID, variantID).Code)
		read := keyedStorefrontRequest(t, key, http.MethodGet, "/store/v1/carts/"+cartID, "")
		require.Equal(t, http.StatusOK, read.Code, read.Body.String())
		total, ok := storefrontData(t, read)["total"].(float64)
		require.True(t, ok, read.Body.String())
		done := keyedStorefrontRequest(t, key, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
			storefrontCompletionBody(t, int64(total)))
		require.Equal(t, http.StatusOK, done.Code, done.Body.String())
		orderID, _ := storefrontData(t, done)["order_id"].(string)
		require.NotEmpty(t, orderID)
		return orderID
	}
	inChannel := place(oneKey)
	inNone := place(twoKey)

	type record struct {
		Data struct {
			ID             string `json:"id"`
			SalesChannelID string `json:"sales_channel_id"`
		} `json:"data"`
	}
	for id, want := range map[string]string{inChannel: channel.ID, inNone: ""} {
		rec := adminRequest(t, http.MethodGet, "/admin/v1/orders/"+id, "Bearer "+secretKey)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var read record
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &read), rec.Body.String())
		assert.Equal(t, want, read.Data.SalesChannelID, "order %s; body: %s", id, rec.Body.String())
	}

	rec := adminRequest(t, http.MethodGet,
		"/admin/v1/orders?"+url.Values{"sales_channel_id": {channel.ID}}.Encode(), "Bearer "+secretKey)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var listed struct {
		Data []struct {
			ID             string `json:"id"`
			SalesChannelID string `json:"sales_channel_id"`
		} `json:"data"`
		Count int64 `json:"count"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listed), rec.Body.String())
	require.Len(t, listed.Data, 1, "the channel was made for this test: its one order; body: %s", rec.Body.String())
	assert.Equal(t, inChannel, listed.Data[0].ID)
	assert.Equal(t, channel.ID, listed.Data[0].SalesChannelID, "the list row carries the channel too")
	assert.Equal(t, int64(1), listed.Count)

	// The promotion is written after the sale, so neither order carries it:
	// what the trial reports is what it would have added, in the channel each
	// order recorded.
	promotion, err := promotionSvc.CreatePromotion(ctx, promotionsvc.PromotionInput{
		Code: fmt.Sprintf("ORDERCHANNEL%d", fixtureCounter.Add(1)), Status: promotionmodels.PromotionDraft,
	})
	require.NoError(t, err)
	_, err = promotionSvc.SetApplicationMethod(ctx, promotion.ID, promotionsvc.ApplicationMethodInput{
		Type: promotionmodels.MethodPercentage, TargetType: promotionmodels.TargetItems,
		Allocation: promotionmodels.AllocationEach, Value: trialRateBps,
	})
	require.NoError(t, err)
	for _, rule := range []promotionsvc.RuleInput{
		{RuleType: promotionmodels.RuleTarget, Attribute: attrVariantID, Operator: promotionmodels.OpIn,
			Values: []string{variantID}},
		{RuleType: promotionmodels.RuleContext, Attribute: "sales_channel_id", Operator: promotionmodels.OpEq,
			Values: []string{channel.ID}},
	} {
		_, err = promotionSvc.AddPromotionRule(ctx, promotion.ID, rule)
		require.NoError(t, err)
	}

	trial := adminRequest(t, http.MethodGet, trialPath(promotion.ID, from, time.Now()),
		"Bearer "+scopedAdminToken(t, "promotion:read", "order:read"))
	require.Equal(t, http.StatusOK, trial.Code, trial.Body.String())
	var envelope struct {
		Data struct {
			Assumptions []string `json:"assumptions"`
			Orders      []struct {
				OrderID       string `json:"order_id"`
				TrialDiscount int64  `json:"trial_discount"`
			} `json:"orders"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(trial.Body.Bytes(), &envelope), trial.Body.String())
	discounted := map[string]int64{}
	for _, order := range envelope.Data.Orders {
		discounted[order.OrderID] = order.TrialDiscount
	}
	assert.Equal(t, map[string]int64{inChannel: orderChannelPrice * trialRateBps / 10_000}, discounted,
		"the order placed in the channel would have been discounted, and only it")
	assert.Contains(t, envelope.Data.Assumptions, "recorded_sales_channel")
	assert.NotContains(t, envelope.Data.Assumptions, "no_sales_channel")
}
