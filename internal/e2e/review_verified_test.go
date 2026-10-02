//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// TestABuyersReviewCarriesTheBadge is ADR 0372 on the production wiring: a
// customer the storefront request proves, who bought the product, writes a
// review the storefront publishes as a verified purchase; a proven customer who
// did not buy it, and a writer nobody proved, write reviews with no badge.
func TestABuyersReviewCarriesTheBadge(t *testing.T) {
	ctx := t.Context()

	buyer, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Reviewed Product", map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, happyInitialStock)
	cartID, _ := prepareCart(ctx, t, buyer, variantID, happyQuantity)
	_, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: paymentmanual.ID,
		PaymentData:       paymentBehavior(t, paymentmanual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     happyTotal,
	})
	require.NoError(t, err)
	variant, err := productSvc.GetVariant(ctx, variantID)
	require.NoError(t, err)
	browser, _ := newCustomer(ctx, t)

	write := func(as, words string) string {
		t.Helper()
		rec := identifiedStorefrontRequest(t, as, http.MethodPost, "/store/v1/products/"+variant.ProductID+"/reviews",
			fmt.Sprintf(`{"rating":5,"body":%q,"author_name":"A customer"}`, words))
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		id, ok := storefrontData(t, rec)["id"].(string)
		require.True(t, ok, rec.Body.String())
		require.Equal(t, http.StatusOK, moderateReview(t, id, "approved", "").Code)

		return id
	}
	bought := write(buyer, "the buyer's words")
	browsed := write(browser, "a browser's words")
	anonymous := write("", "a stranger's words")

	rec := storefrontRequest(t, http.MethodGet, "/store/v1/products/"+variant.ProductID+"/reviews", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var listing struct {
		Data []struct {
			ID               string `json:"id"`
			VerifiedPurchase bool   `json:"verified_purchase"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listing))
	badges := map[string]bool{}
	for _, review := range listing.Data {
		badges[review.ID] = review.VerifiedPurchase
	}
	assert.Equal(t, map[string]bool{bought: true, browsed: false, anonymous: false}, badges)
	assert.NotContains(t, rec.Body.String(), buyer, "the badge names nobody")
}
