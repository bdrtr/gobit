//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	taxsvc "github.com/bdrtr/gobit/internal/modules/tax/service"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// The amounts of the tax rate trial, computed by hand.
//
//	price 10_000; charged and baseline at the default 20%, the trial at 8%
//	order of two:  charged 4_000, baseline 4_000, trial 1_600
//	order of one:  charged 2_000, baseline 2_000, trial   800
//	the change's effect on the period: -3_600
const (
	rateTrialPrice     int64 = 10_000
	rateTrialRuledBps  int32 = 1_000
	rateTrialTriedBps  int32 = 800
	rateTrialTriedTax2 int64 = 1_600
	rateTrialTriedTax1 int64 = 800
)

// TestATaxRateCanBeTriedOnPastOrders is the gate ADR 0387 rests on: a ruled
// rate with no rule, which matches nothing and charges nothing, tried at 8%
// with a rule on a product sold in the period, through the production endpoint
// over the order, product and tax modules' real schemas.
//
// Two orders were placed at the default 20% and a third was canceled. The
// trial has to tax each line twice from the amount the cart sent, report what
// was charged, leave the canceled order out, write nothing, refuse an identity
// that may read the taxes but not the orders, and refuse a rule on the default
// before reading an order.
func TestATaxRateCanBeTriedOnPastOrders(t *testing.T) {
	ctx := t.Context()
	from := time.Now().Add(-time.Second)

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
		TaxRegionID: rootID, Name: "E2E Reduced, not yet ruled", RateBps: rateTrialRuledBps,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = taxSvc.DeleteTaxRate(ctx, rate.ID) })
	rates, err := taxSvc.ListTaxRates(ctx, rootID)
	require.NoError(t, err)
	var defaultID string
	for _, r := range rates {
		if r.IsDefault {
			defaultID = r.ID
		}
	}
	require.NotEmpty(t, defaultID)

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Rate Trial Product",
		map[string]int64{taxedCurrency: rateTrialPrice}, 10)
	variant, err := productSvc.GetVariant(ctx, variantID)
	require.NoError(t, err)

	place := func(quantity int64, outcome string) (checkoutwf.CompleteCartResult, error) {
		cartID, totals := prepareCart(ctx, t, customerID, variantID, quantity)

		return orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
			CartID:            cartID,
			LocationID:        stockLocationID,
			PaymentProviderID: paymentmanual.ID,
			PaymentData:       paymentBehavior(t, outcome),
			Email:             email,
			ExpectedTotal:     totals.Total,
		})
	}
	two, err := place(2, paymentmanual.OutcomeAuthorize)
	require.NoError(t, err)
	one, err := place(1, paymentmanual.OutcomeAuthorize)
	require.NoError(t, err)
	_, err = place(1, paymentmanual.OutcomeDecline)
	require.Error(t, err, "the declined completion is the canceled order of this scenario")
	to := time.Now()

	both := scopedAdminToken(t, "tax:read", "order:read")
	change := url.Values{"rate_bps": {"800"}, "rule": {"product:" + variant.ProductID}}
	rec := adminRequest(t, http.MethodGet, rateTrialPath(rate.ID, from, to, change), "Bearer "+both)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	type sums struct {
		CurrencyCode string `json:"currency_code"`
		LinesReached int    `json:"lines_reached"`
		LinesChanged int    `json:"lines_changed"`
		Charged      int64  `json:"charged"`
		Baseline     int64  `json:"baseline"`
		Trial        int64  `json:"trial"`
	}
	var envelope struct {
		Data struct {
			TaxRateID      string   `json:"tax_rate_id"`
			Assumptions    []string `json:"assumptions"`
			OrdersRead     int      `json:"orders_read"`
			OrdersCanceled int      `json:"orders_canceled"`
			Currencies     []sums   `json:"currencies"`
			Orders         []struct {
				OrderID  string `json:"order_id"`
				Charged  int64  `json:"charged"`
				Baseline int64  `json:"baseline"`
				Trial    int64  `json:"trial"`
			} `json:"orders"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "body: %s", rec.Body.String())
	report := envelope.Data
	assert.Equal(t, rate.ID, report.TaxRateID)

	// Other scenarios place orders in the same window; the rule names a
	// product only this one sells, so the moved orders are exactly ours and
	// every other line adds the same to both sums.
	moved := map[string][3]int64{}
	for _, order := range report.Orders {
		moved[order.OrderID] = [3]int64{order.Charged, order.Baseline, order.Trial}
	}
	charged := func(quantity int64) int64 { return quantity * rateTrialPrice * int64(taxRateBps) / 10_000 }
	assert.Equal(t, map[string][3]int64{
		two.OrderID: {charged(2), charged(2), rateTrialTriedTax2},
		one.OrderID: {charged(1), charged(1), rateTrialTriedTax1},
	}, moved, "each line taxed from the amount the cart sent, and no other order")

	var sum *sums
	for i := range report.Currencies {
		if report.Currencies[i].CurrencyCode == taxedCurrency {
			sum = &report.Currencies[i]
		}
	}
	require.NotNil(t, sum, "the scenario's currency is summed; body: %s", rec.Body.String())
	assert.Equal(t, rateTrialTriedTax2+rateTrialTriedTax1-charged(3), sum.Trial-sum.Baseline,
		"the change's effect is ours alone")
	assert.Equal(t, 2, sum.LinesReached, "the rate reaches only the lines its rule names")
	assert.Equal(t, 2, sum.LinesChanged)
	assert.GreaterOrEqual(t, report.OrdersCanceled, 1, "the canceled order is counted apart")
	assert.GreaterOrEqual(t, report.OrdersRead, 3)
	assert.Contains(t, report.Assumptions, "todays_rates")
	assert.Contains(t, report.Assumptions, "todays_catalog")

	stored, err := taxSvc.GetTaxRate(ctx, rate.ID)
	require.NoError(t, err)
	assert.Equal(t, rateTrialRuledBps, stored.RateBps, "a trial writes no value")
	rules, err := taxSvc.ListRateRules(ctx, rate.ID)
	require.NoError(t, err)
	assert.Empty(t, rules, "a trial writes no rule")

	t.Run("a line the rate reaches at the value it already pays is reached and not changed", func(t *testing.T) {
		same := url.Values{"rate_bps": {strconv.Itoa(int(taxRateBps))}, "rule": {"product:" + variant.ProductID}}
		rec := adminRequest(t, http.MethodGet, rateTrialPath(rate.ID, from, to, same), "Bearer "+both)
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
		var envelope struct {
			Data struct {
				Currencies []sums `json:"currencies"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "body: %s", rec.Body.String())
		var sum *sums
		for i := range envelope.Data.Currencies {
			if envelope.Data.Currencies[i].CurrencyCode == taxedCurrency {
				sum = &envelope.Data.Currencies[i]
			}
		}
		require.NotNil(t, sum, "body: %s", rec.Body.String())
		assert.Equal(t, 2, sum.LinesReached, "the rule moves the product's lines to the rate tried")
		assert.Equal(t, 0, sum.LinesChanged, "at the default's own value no line's tax moves")
		assert.Equal(t, sum.Baseline, sum.Trial)
	})

	t.Run("an identity that may not read orders is refused", func(t *testing.T) {
		taxesOnly := scopedAdminToken(t, "tax:read")
		rec := adminRequest(t, http.MethodGet, rateTrialPath(rate.ID, from, to, change), "Bearer "+taxesOnly)
		assert.Equal(t, http.StatusForbidden, rec.Code, "the report is orders; body: %s", rec.Body.String())
	})

	t.Run("a rule on the default is refused before any order is read", func(t *testing.T) {
		rec := adminRequest(t, http.MethodGet, rateTrialPath(defaultID, from, to,
			url.Values{"rule": {"product:" + variant.ProductID}}), "Bearer "+both)
		assert.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())
		assert.Contains(t, rec.Body.String(), taxsvc.CodeTrialChangeRefused)
	})

	t.Run("a period reaching into the future is refused", func(t *testing.T) {
		rec := adminRequest(t, http.MethodGet,
			rateTrialPath(rate.ID, from, time.Now().Add(time.Hour), change), "Bearer "+both)
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
	})
}

// rateTrialPath is the trial's address for a tax rate, a period and a change.
func rateTrialPath(rateID string, from, to time.Time, change url.Values) string {
	query := url.Values{}
	for name, values := range change {
		query[name] = values
	}
	query.Set("from", from.UTC().Format(time.RFC3339Nano))
	query.Set("to", to.UTC().Format(time.RFC3339Nano))

	return "/admin/v1/tax-rates/" + rateID + "/trial?" + query.Encode()
}
