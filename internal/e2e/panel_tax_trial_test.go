//go:build integration

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	taxsvc "github.com/bdrtr/gobit/internal/modules/tax/service"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// trialPanel is the production panel, read by an operator holding the scopes
// given.
func trialPanel(t *testing.T, scopes ...string) func(path string) *httptest.ResponseRecorder {
	t.Helper()

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)

	return func(path string) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_trials", Kind: "user", Scopes: scopes,
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		return rec
	}
}

// panelTrialPeriod is a trial screen's period from yesterday to today, which
// the screen ends now, as days in the server's zone.
func panelTrialPeriod(extra url.Values) string {
	query := url.Values{}
	for name, values := range extra {
		query[name] = values
	}
	now := time.Now()
	query.Set("from", now.AddDate(0, 0, -1).Format("2006-01-02"))
	query.Set("to", now.Format("2006-01-02"))

	return "?" + query.Encode()
}

// trialRowOf is the row a trial screen draws for the order: its number linked
// to its page, then its figures.
func trialRowOf(ctx context.Context, t *testing.T, body, orderID string) string {
	t.Helper()

	order, err := orderSvc.GetOrder(ctx, orderID)
	require.NoError(t, err)
	link := fmt.Sprintf(`<a href="%s/%s">#%d</a>`, adminui.OrdersPath, orderID, order.DisplayID)
	_, row, found := strings.Cut(body, link)
	require.True(t, found, "the screen names order #%d; body: %s", order.DisplayID, body)
	row, _, _ = strings.Cut(row, "</tr>")

	return row
}

// TestAnOperatorTriesATaxRateInThePanel is ADR 0395 on the production wiring:
// the Taxes screen offers a country's rate its trial, and the trial screen,
// through the registered `tax.admin` surface and the cart flows, draws the
// tax rate trial's report — each order the change moves, by its number, with
// what it was charged, today's tax and the tried one — and writes nothing.
func TestAnOperatorTriesATaxRateInThePanel(t *testing.T) {
	ctx := t.Context()

	page, err := taxSvc.ListTaxRegions(ctx, taxedCountry, 0, 0)
	require.NoError(t, err)
	var rootID string
	for _, region := range page.Items {
		if region.IsRoot() {
			rootID = region.ID
		}
	}
	require.NotEmpty(t, rootID, "the taxed country's root region is a fixture")
	rate, err := taxSvc.CreateTaxRate(ctx, taxsvc.CreateTaxRateInput{
		TaxRegionID: rootID, Name: "E2E Panel trial, not yet ruled", RateBps: rateTrialRuledBps,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = taxSvc.DeleteTaxRate(ctx, rate.ID) })

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Panel Rate Trial Product",
		map[string]int64{taxedCurrency: rateTrialPrice}, 10)
	variant, err := productSvc.GetVariant(ctx, variantID)
	require.NoError(t, err)
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

	get := trialPanel(t, "tax:read", "order:read")
	offered := false
	for page := 1; page <= 50 && !offered; page++ {
		body := get(fmt.Sprintf("%s?page=%d", adminui.TaxesPath, page)).Body.String()
		offered = strings.Contains(body, fmt.Sprintf(`action="%s/rates/%s/trial"`, adminui.TaxesPath, rate.ID))
		if !strings.Contains(body, ">Next</a>") {
			break
		}
	}
	assert.True(t, offered, "the Taxes screen offers the rate its trial")

	rec := get(adminui.TaxesPath + "/rates/" + rate.ID + "/trial" + panelTrialPeriod(url.Values{
		"rate": {"8"}, "rule_reference": {"product"}, "rule_id": {variant.ProductID},
	}))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, "Tried at 8%, with a rule on product "+variant.ProductID+".")
	assert.Contains(t, trialRowOf(ctx, t, body, two),
		"<td>40.00 TRY</td><td>40.00 TRY</td><td>16.00 TRY</td><td>-24.00 TRY</td>")
	assert.Contains(t, trialRowOf(ctx, t, body, one),
		"<td>20.00 TRY</td><td>20.00 TRY</td><td>8.00 TRY</td><td>-12.00 TRY</td>")
	assert.Contains(t, body, "todays_rates")

	stored, err := taxSvc.GetTaxRate(ctx, rate.ID)
	require.NoError(t, err)
	assert.Equal(t, rateTrialRuledBps, stored.RateBps, "a trial writes no value")
	rules, err := taxSvc.ListRateRules(ctx, rate.ID)
	require.NoError(t, err)
	assert.Empty(t, rules, "a trial writes no rule")

	refused := get(adminui.TaxesPath + "/rates/" + rate.ID + "/trial" + panelTrialPeriod(url.Values{
		"rule_reference": {"product"}, "rule_id": {variant.ProductID}, "rate": {"101"},
	}))
	assert.Equal(t, http.StatusUnprocessableEntity, refused.Code, "a rate past a hundred percent; body: %s", refused.Body.String())
	assert.Contains(t, refused.Body.String(), `role="alert"`)
}
