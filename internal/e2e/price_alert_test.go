//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreprovider "github.com/bdrtr/gobit/core/provider"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
	"github.com/bdrtr/gobit/internal/workflows/stockalert"
)

// priceDropMails returns the captured price-drop mails about a variant.
func (s *notificationProviderSpy) priceDropMails(variantID string) []coreprovider.Notification {
	s.mu.Lock()
	defer s.mu.Unlock()

	var found []coreprovider.Notification
	for i := range s.captured {
		if s.captured[i].Template == stockalert.TemplatePriceDrop &&
			s.captured[i].Data[stockalert.DataVariantID] == variantID {
			found = append(found, s.captured[i])
		}
	}
	return found
}

// TestAWishlistPriceDropIsMailedOnce is ADR 0216 on the production wiring: a
// proven shopper marks a variant's price in a region, a pass records the price
// the cart would charge, a raise mails nothing, a price under the mark's mails
// the shopper's own address once and clears the mark, and a pass after that
// mails nothing.
func TestAWishlistPriceDropIsMailedOnce(t *testing.T) {
	ctx := t.Context()
	shopper, address := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Price Alert Product", nil, 5)
	set, err := pricingSvc.CreatePriceSet(ctx, []pricingsvc.PriceInput{
		{CurrencyCode: taxedCurrency, Amount: 20_000, MinQuantity: 1},
	})
	require.NoError(t, err)
	require.NoError(t, productSvc.SetVariantPriceSet(ctx, variantID, set.ID))
	reprice := func(amount int64) {
		t.Helper()

		_, err := pricingSvc.SetPrices(ctx, set.ID, []pricingsvc.PriceInput{
			{CurrencyCode: taxedCurrency, Amount: amount, MinQuantity: 1},
		})
		require.NoError(t, err)
	}
	item := func() map[string]any {
		t.Helper()

		list := identifiedStorefrontRequest(t, shopper, http.MethodGet, "/store/v1/customers/"+shopper+"/wishlist", "")
		require.Equal(t, http.StatusOK, list.Code, list.Body.String())
		var envelope struct {
			Data []map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(list.Body.Bytes(), &envelope), list.Body.String())
		for _, row := range envelope.Data {
			if row["variant_id"] == variantID {
				return row
			}
		}
		require.FailNow(t, "the marked item is not on the list")
		return nil
	}

	marked := identifiedStorefrontRequest(t, shopper, http.MethodPut,
		"/store/v1/customers/"+shopper+"/wishlist/"+variantID+"/price-alert",
		`{"region_id":"`+taxedRegionID+`"}`)
	require.Equal(t, http.StatusOK, marked.Code, marked.Body.String())
	assert.Equal(t, true, storefrontData(t, marked)["price_alert"])
	assert.NotContains(t, storefrontData(t, marked), "price_alert_amount", "no price is recorded at the request")

	_, recorded, _, err := stockAlerts.Pass(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, recorded, 1)
	assert.Empty(t, notificationSpy.priceDropMails(variantID), "the first pass records, it compares nothing")
	assert.Equal(t, float64(20_000), item()["price_alert_amount"], "the price the cart charges for one")
	assert.Equal(t, taxedCurrency, item()["price_alert_currency_code"])

	reprice(21_000)
	_, _, _, err = stockAlerts.Pass(ctx)
	require.NoError(t, err)
	assert.Empty(t, notificationSpy.priceDropMails(variantID), "a raise is not a drop")

	reprice(17_500)
	_, _, mailed, err := stockAlerts.Pass(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, mailed, 1)

	mails := notificationSpy.priceDropMails(variantID)
	require.Len(t, mails, 1)
	assert.Equal(t, address, mails[0].To, "the shopper's own address")
	assert.Equal(t, "20000", mails[0].Data[stockalert.DataPreviousAmount])
	assert.Equal(t, "17500", mails[0].Data[stockalert.DataAmount])
	assert.Equal(t, taxedCurrency, mails[0].Data[stockalert.DataCurrencyCode])
	assert.Equal(t, "E2E Price Alert Product", mails[0].Data[stockalert.DataProductTitle])
	assert.Equal(t, false, item()["price_alert"], "the mail cleared the mark")

	reprice(15_000)
	_, _, _, err = stockAlerts.Pass(ctx)
	require.NoError(t, err)
	assert.Len(t, notificationSpy.priceDropMails(variantID), 1, "once")
}

// TestAPriceAlertNeedsARegion: a mark without the region it prices in is
// refused, and another shopper's session cannot mark a list that is not theirs.
func TestAPriceAlertNeedsARegion(t *testing.T) {
	ctx := t.Context()
	shopper, _ := newCustomer(ctx, t)
	stranger, _ := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Price Alert Refusals", map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, 5)
	alert := "/store/v1/customers/" + shopper + "/wishlist/" + variantID + "/price-alert"

	bare := identifiedStorefrontRequest(t, shopper, http.MethodPut, alert, `{}`)
	assert.Equal(t, http.StatusUnprocessableEntity, bare.Code, bare.Body.String())
	assert.Contains(t, bare.Body.String(), "region id")

	refused := identifiedStorefrontRequest(t, stranger, http.MethodPut, alert, `{"region_id":"`+taxedRegionID+`"}`)
	assert.Equal(t, http.StatusForbidden, refused.Code, refused.Body.String())
}
