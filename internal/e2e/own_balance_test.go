//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
)

// TestACustomerReadsTheirOwnBalanceOnTheStorefront is ADR 0253 through the
// production wiring: the payment module's storefront reads resolve the bound
// customer identity lazily, serve the proven customer their store credit and
// their points, and refuse a request that names somebody else or proves
// nobody, without reading the ledger.
func TestACustomerReadsTheirOwnBalanceOnTheStorefront(t *testing.T) {
	ctx := t.Context()

	customerID, _ := newCustomer(ctx, t)
	strangerID, _ := newCustomer(ctx, t)
	_, err := paymentSvc.IssueCredit(ctx, paymentsvc.IssueCreditInput{
		CustomerID: customerID, CurrencyCode: taxedCurrency, Amount: creditIssued,
		Reason: creditIssuedReason,
	})
	require.NoError(t, err)

	credit := func(id string) string {
		return "/store/v1/customers/" + id + "/store-credit/balance?currency_code=" + strings.ToLower(taxedCurrency)
	}
	points := func(id string) string {
		return "/store/v1/customers/" + id + "/loyalty-points/balance?currency_code=" + taxedCurrency
	}

	var balance struct {
		Data struct {
			CustomerID   string `json:"customer_id"`
			CurrencyCode string `json:"currency_code"`
			Balance      int64  `json:"balance"`
			Points       int64  `json:"points"`
		} `json:"data"`
	}

	rec := identifiedStorefrontRequest(t, customerID, http.MethodGet, credit(customerID), "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &balance))
	assert.Equal(t, customerID, balance.Data.CustomerID)
	assert.Equal(t, taxedCurrency, balance.Data.CurrencyCode)
	assert.Equal(t, creditIssued, balance.Data.Balance, "the proven customer reads the credit issued")

	rec = identifiedStorefrontRequest(t, customerID, http.MethodGet, points(customerID), "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &balance))
	assert.Equal(t, int64(0), balance.Data.Points, "a customer who earned nothing holds no points")

	for _, path := range []string{credit(customerID), points(customerID)} {
		rec = identifiedStorefrontRequest(t, strangerID, http.MethodGet, path, "")
		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), corehttp.CodeIdentityMismatch,
			"a customer cannot read somebody else's balance")

		rec = identifiedStorefrontRequest(t, "", http.MethodGet, path, "")
		assert.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
		assert.NotContains(t, rec.Body.String(), `"balance"`)
	}
}
