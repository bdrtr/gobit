//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// TestAnOrderKeepsTheMarginItWasPlacedAt is ADR 0401 on the production wiring:
// an operator writes a variant's costs over the admin API, the checkout copies
// the one in the cart's currency onto the line, and after the cost changes the
// admin order and the admin order list still read the cost and the margin the
// order was placed at. The storefront's order and product carry neither.
func TestAnOrderKeepsTheMarginItWasPlacedAt(t *testing.T) {
	ctx := t.Context()
	// A cost no other figure of the fixture equals, so finding it in a
	// storefront body can only be a leak.
	const unitCost int64 = 31_337

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Copper Kettle", map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, happyInitialStock)
	variant, err := productSvc.GetVariant(ctx, variantID)
	require.NoError(t, err)

	costsPath := "/admin/v1/variants/" + variantID + "/costs"
	written, err := adminRequestWithBody(http.MethodPut, costsPath, map[string]any{"costs": []map[string]any{
		{"currency_code": "try", "amount": unitCost}, {"currency_code": untaxedCurrency, "amount": 1},
	}})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, written.Code, written.Body.String())
	read, err := adminRequestWithBody(http.MethodGet, costsPath, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	assert.JSONEq(t, `{"data":{"variant_id":"`+variantID+`","costs":[
		{"currency_code":"EUR","amount":1},{"currency_code":"TRY","amount":`+strconv.FormatInt(unitCost, 10)+`}]}}`,
		read.Body.String(), "the code is upper-cased and the list is in currency order")

	cartID, _ := prepareCart(ctx, t, customerID, variantID, happyQuantity)
	order, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: paymentmanual.ID,
		PaymentData:       paymentBehavior(t, paymentmanual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     happyTotal,
	})
	require.NoError(t, err)

	changed, err := adminRequestWithBody(http.MethodPut, costsPath, map[string]any{"costs": []map[string]any{
		{"currency_code": taxedCurrency, "amount": 44_000},
	}})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, changed.Code, changed.Body.String())

	placed := map[string]any{
		"currency_code":      taxedCurrency,
		"sales":              float64(happySubtotal),
		"cost":               float64(happyQuantity * unitCost),
		"margin":             float64(happySubtotal - happyQuantity*unitCost),
		"lines_without_cost": float64(0),
	}

	detail, err := adminRequestWithBody(http.MethodGet, "/admin/v1/orders/"+order.OrderID, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, detail.Code, detail.Body.String())
	var record struct {
		Data struct {
			Items []struct {
				UnitCost *int64 `json:"unit_cost"`
			} `json:"items"`
			PlacedMargin map[string]any `json:"placed_margin"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(detail.Body.Bytes(), &record))
	require.Len(t, record.Data.Items, 1)
	require.NotNil(t, record.Data.Items[0].UnitCost, "the line keeps the cost it was sold at")
	assert.Equal(t, unitCost, *record.Data.Items[0].UnitCost, "a cost changed after the sale changes no order")
	assert.Equal(t, placed, record.Data.PlacedMargin)

	list, err := adminRequestWithBody(http.MethodGet, "/admin/v1/orders?customer_id="+customerID, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	var page struct {
		Data []struct {
			ID           string         `json:"id"`
			PlacedMargin map[string]any `json:"placed_margin"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &page))
	require.Len(t, page.Data, 1)
	assert.Equal(t, order.OrderID, page.Data[0].ID)
	assert.Equal(t, placed, page.Data[0].PlacedMargin, "the list reads the margin the order was placed at")

	stored := strconv.FormatInt(unitCost, 10)
	for name, path := range map[string]string{
		"the storefront order":   "/store/v1/orders/" + order.OrderID,
		"the storefront product": catalogPath(testChannelID, "/products/"+variant.ProductID),
	} {
		rec := storefrontRequest(t, http.MethodGet, path, "")
		require.Equal(t, http.StatusOK, rec.Code, "%s: %s", name, rec.Body.String())
		require.Contains(t, rec.Body.String(), variantID, "%s names the variant", name)
		assert.NotContains(t, strings.ToLower(rec.Body.String()), "cost", "%s carries a cost", name)
		assert.NotContains(t, rec.Body.String(), "margin", "%s carries a margin", name)
		assert.NotContains(t, rec.Body.String(), stored, "%s carries the cost's amount", name)
	}
}
