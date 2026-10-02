//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// TestAShopperListsTheirOwnOrdersOnTheStorefront is ADR 0367 on the
// production wiring: each customer the storefront request proves lists the
// orders they placed and not the other's, through the publishable key, and a
// request naming another customer's list is refused.
func TestAShopperListsTheirOwnOrdersOnTheStorefront(t *testing.T) {
	ctx := t.Context()

	place := func() (customerID, orderID string) {
		t.Helper()

		customerID, email := newCustomer(ctx, t)
		variantID, _ := newStockedVariant(ctx, t, "E2E Own Orders Product", map[string]int64{
			taxedCurrency: happyUnitPrice,
		}, happyInitialStock)
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
		return customerID, order.OrderID
	}
	shopper, shoppersOrder := place()
	stranger, strangersOrder := place()

	list := func(as, of string) (int, []string) {
		t.Helper()

		rec := identifiedStorefrontRequest(t, as, http.MethodGet, "/store/v1/customers/"+of+"/orders", "")
		var body struct {
			Data []struct {
				ID         string `json:"id"`
				CustomerID string `json:"customer_id"`
			} `json:"data"`
		}
		ids := []string{}
		if rec.Code == http.StatusOK {
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			for _, order := range body.Data {
				assert.Equal(t, of, order.CustomerID)
				ids = append(ids, order.ID)
			}
		}
		return rec.Code, ids
	}

	status, ids := list(shopper, shopper)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, []string{shoppersOrder}, ids, "the shopper's own order, and only theirs")

	status, ids = list(stranger, stranger)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, []string{strangersOrder}, ids)

	status, _ = list(stranger, shopper)
	assert.Equal(t, http.StatusForbidden, status, "another customer's list is refused")
	status, _ = list("", shopper)
	assert.Equal(t, http.StatusUnauthorized, status, "a request that proves nobody is refused")
}
