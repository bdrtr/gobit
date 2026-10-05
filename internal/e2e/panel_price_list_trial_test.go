//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/adminui"
	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	pricingmodels "github.com/bdrtr/gobit/internal/modules/pricing/models"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// TestAnOperatorTriesAPriceListInThePanel is ADR 0395 on the production
// wiring: the Price lists screen offers a draft list its trial, and the trial
// screen, through the registered `pricing.admin` surface and the cart flows,
// draws the price list trial's report — each order the list moves, by its
// number, priced without it and with it — and leaves the list a draft.
func TestAnOperatorTriesAPriceListInThePanel(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Panel List Trial Product", nil, 10)
	set, err := pricingSvc.CreatePriceSet(ctx, []pricingsvc.PriceInput{
		{CurrencyCode: taxedCurrency, Amount: listTrialBase, MinQuantity: 1},
	})
	require.NoError(t, err)
	require.NoError(t, productSvc.SetVariantPriceSet(ctx, variantID, set.ID))
	place := func(quantity int64) string {
		cartID, totals := prepareCart(ctx, t, customerID, variantID, quantity)
		placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
			CartID: cartID, LocationID: stockLocationID, PaymentProviderID: paymentmanual.ID,
			PaymentData: paymentBehavior(t, paymentmanual.OutcomeAuthorize), Email: email,
			ExpectedTotal: totals.Total,
		})
		require.NoError(t, err)

		return placed.OrderID
	}
	two, one := place(2), place(1)

	list, err := pricingSvc.CreatePriceList(ctx, pricingsvc.PriceListInput{
		Title: "E2E Panel List Trial Sale", Type: pricingmodels.PriceListSale, Status: pricingmodels.PriceListDraft,
	})
	require.NoError(t, err)
	_, err = pricingSvc.SetPrices(ctx, set.ID, []pricingsvc.PriceInput{
		{CurrencyCode: taxedCurrency, Amount: listTrialBase, MinQuantity: 1},
		{CurrencyCode: taxedCurrency, Amount: listTrialSale, MinQuantity: 1, PriceListID: &list.ID},
		{CurrencyCode: taxedCurrency, Amount: listTrialVolume, MinQuantity: 2, PriceListID: &list.ID},
	})
	require.NoError(t, err)

	get := trialPanel(t, "pricing:read", "order:read")
	offered := false
	for page := 1; page <= 50 && !offered; page++ {
		body := get(fmt.Sprintf("%s?page=%d", adminui.PriceListsPath, page)).Body.String()
		offered = strings.Contains(body, fmt.Sprintf(`href="%s/%s/trial"`, adminui.PriceListsPath, list.ID))
		if !strings.Contains(body, ">Next</a>") {
			break
		}
	}
	assert.True(t, offered, "the Price lists screen offers the draft list its trial")

	rec := get(adminui.PriceListsPath + "/" + list.ID + "/trial" + panelTrialPeriod(nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, trialRowOf(ctx, t, body, two), "<td>200.00 TRY</td><td>140.00 TRY</td><td>-60.00 TRY</td>",
		"two units at the volume price")
	assert.Contains(t, trialRowOf(ctx, t, body, one), "<td>100.00 TRY</td><td>80.00 TRY</td><td>-20.00 TRY</td>",
		"one unit at the sale price")

	stored, err := pricingSvc.GetPriceList(ctx, list.ID)
	require.NoError(t, err)
	assert.Equal(t, pricingmodels.PriceListDraft, stored.Status, "a trial leaves the list a draft")
}
