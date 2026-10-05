//go:build integration

package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/adminui"
	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	promotionmodels "github.com/bdrtr/gobit/internal/modules/promotion/models"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// TestAnOperatorTriesAPromotionInThePanel is ADR 0395 on the production
// wiring: a draft coupon's page offers its trial, and the trial screen,
// through the registered `promotion.admin` surface and the cart flows, draws
// the promotion trial's report — each order it would have discounted, by its
// number, with its goods and what the coupon adds — and publishes nothing.
func TestAnOperatorTriesAPromotionInThePanel(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Panel Promotion Trial Product",
		map[string]int64{taxedCurrency: trialUnitPrice}, 10)
	var ordered []string
	for range 2 {
		cartID, totals := prepareCart(ctx, t, customerID, variantID, trialQuantity)
		placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
			CartID: cartID, LocationID: stockLocationID, PaymentProviderID: paymentmanual.ID,
			PaymentData: paymentBehavior(t, paymentmanual.OutcomeAuthorize), Email: email,
			ExpectedTotal: totals.Total,
		})
		require.NoError(t, err)
		ordered = append(ordered, placed.OrderID)
	}
	promotionID := newDraftTrialCoupon(ctx, t, variantID)

	get := trialPanel(t, "promotion:read", "order:read")
	page := get(adminui.PromotionsPath + "/" + promotionID)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Body.String(), adminui.PromotionsPath+"/"+promotionID+"/trial", "the page offers its trial")

	rec := get(adminui.PromotionsPath + "/" + promotionID + "/trial" + panelTrialPeriod(nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	for _, orderID := range ordered {
		assert.Contains(t, trialRowOf(ctx, t, body, orderID), "<td>246.80 TRY</td>",
			"the order's goods, %d × %d", trialQuantity, trialUnitPrice)
		assert.Contains(t, trialRowOf(ctx, t, body, orderID), "<td>24.68 TRY</td>",
			"what the coupon adds, ten percent of them")
	}

	promotion, err := promotionSvc.GetPromotion(ctx, promotionID)
	require.NoError(t, err)
	assert.Equal(t, promotionmodels.PromotionDraft, promotion.Status, "a trial publishes nothing")
}
