//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	pricingmodels "github.com/bdrtr/gobit/internal/modules/pricing/models"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// The amounts of the price list trial, computed by hand.
//
//	base 10_000; the list: 8_000 from one, 7_000 from two
//	order of two:  baseline 20_000, trial 14_000
//	order of one:  baseline 10_000, trial  8_000
//	the list's effect on the period: -8_000
const (
	listTrialBase   int64 = 10_000
	listTrialSale   int64 = 8_000
	listTrialVolume int64 = 7_000
)

// TestAPriceListCanBeTriedOnPastOrders is the gate ADR 0220 rests on: a DRAFT
// sale list written after the orders were placed, priced against them through
// the production endpoint over the order, product and pricing modules' real
// schemas.
//
// Two orders were placed at the base price and a third was canceled. The trial
// has to price each line at its own quantity with the list and without it,
// report what the lines were sold at, leave the canceled order out, leave the
// list a draft, and refuse an identity that may read prices but not orders.
func TestAPriceListCanBeTriedOnPastOrders(t *testing.T) {
	ctx := t.Context()
	from := time.Now().Add(-time.Second)

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E List Trial Product", nil, 10)
	set, err := pricingSvc.CreatePriceSet(ctx, []pricingsvc.PriceInput{
		{CurrencyCode: taxedCurrency, Amount: listTrialBase, MinQuantity: 1},
	})
	require.NoError(t, err)
	require.NoError(t, productSvc.SetVariantPriceSet(ctx, variantID, set.ID))

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

	list, err := pricingSvc.CreatePriceList(ctx, pricingsvc.PriceListInput{
		Title: "E2E List Trial Sale", Type: pricingmodels.PriceListSale, Status: pricingmodels.PriceListDraft,
	})
	require.NoError(t, err)
	_, err = pricingSvc.SetPrices(ctx, set.ID, []pricingsvc.PriceInput{
		{CurrencyCode: taxedCurrency, Amount: listTrialBase, MinQuantity: 1},
		{CurrencyCode: taxedCurrency, Amount: listTrialSale, MinQuantity: 1, PriceListID: &list.ID},
		{CurrencyCode: taxedCurrency, Amount: listTrialVolume, MinQuantity: 2, PriceListID: &list.ID},
	})
	require.NoError(t, err)
	to := time.Now()

	both := scopedAdminToken(t, "pricing:read", "order:read")
	rec := adminRequest(t, http.MethodGet, listTrialPath(list.ID, from, to), "Bearer "+both)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var envelope struct {
		Data struct {
			PriceListID    string   `json:"price_list_id"`
			Assumptions    []string `json:"assumptions"`
			OrdersRead     int      `json:"orders_read"`
			OrdersCanceled int      `json:"orders_canceled"`
			Currencies     []struct {
				CurrencyCode string `json:"currency_code"`
				LinesChanged int    `json:"lines_changed"`
				Charged      int64  `json:"charged"`
				Baseline     int64  `json:"baseline"`
				Trial        int64  `json:"trial"`
			} `json:"currencies"`
			Orders []struct {
				OrderID  string `json:"order_id"`
				Baseline int64  `json:"baseline"`
				Trial    int64  `json:"trial"`
			} `json:"orders"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "body: %s", rec.Body.String())
	report := envelope.Data
	assert.Equal(t, list.ID, report.PriceListID)

	// Other scenarios place orders in the same window; the list prices a set
	// only this one sells, so the changed orders are exactly ours and every
	// other line adds the same to both sums.
	changed := map[string][2]int64{}
	for _, order := range report.Orders {
		changed[order.OrderID] = [2]int64{order.Baseline, order.Trial}
	}
	assert.Equal(t, map[string][2]int64{
		two.OrderID: {2 * listTrialBase, 2 * listTrialVolume},
		one.OrderID: {listTrialBase, listTrialSale},
	}, changed, "each line at its own quantity, the list offered though a draft, and no other order")

	var sum *struct {
		CurrencyCode string `json:"currency_code"`
		LinesChanged int    `json:"lines_changed"`
		Charged      int64  `json:"charged"`
		Baseline     int64  `json:"baseline"`
		Trial        int64  `json:"trial"`
	}
	for i := range report.Currencies {
		if report.Currencies[i].CurrencyCode == taxedCurrency {
			sum = &report.Currencies[i]
		}
	}
	require.NotNil(t, sum, "the scenario's currency is summed; body: %s", rec.Body.String())
	assert.Equal(t, -(2*(listTrialBase-listTrialVolume) + (listTrialBase - listTrialSale)), sum.Trial-sum.Baseline,
		"the list's effect is ours alone")
	assert.Equal(t, 2, sum.LinesChanged)
	assert.GreaterOrEqual(t, sum.Charged, 3*listTrialBase, "what our lines were sold at is in the sum")
	assert.GreaterOrEqual(t, report.OrdersCanceled, 1, "the canceled order is counted apart")
	assert.GreaterOrEqual(t, report.OrdersRead, 3)
	assert.Contains(t, report.Assumptions, "list_active_without_window")

	stored, err := pricingSvc.GetPriceList(ctx, list.ID)
	require.NoError(t, err)
	assert.Equal(t, pricingmodels.PriceListDraft, stored.Status, "a trial publishes nothing")

	t.Run("an identity that may not read orders is refused", func(t *testing.T) {
		pricesOnly := scopedAdminToken(t, "pricing:read")
		rec := adminRequest(t, http.MethodGet, listTrialPath(list.ID, from, to), "Bearer "+pricesOnly)
		assert.Equal(t, http.StatusForbidden, rec.Code, "the report is orders; body: %s", rec.Body.String())
	})

	t.Run("a period reaching into the future is refused", func(t *testing.T) {
		rec := adminRequest(t, http.MethodGet,
			listTrialPath(list.ID, from, time.Now().Add(time.Hour)), "Bearer "+both)
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
	})

	t.Run("an unknown list is not found", func(t *testing.T) {
		rec := adminRequest(t, http.MethodGet, listTrialPath("plist_missing", from, to), "Bearer "+both)
		assert.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
	})
}

// listTrialPath is the trial's address for a price list and a period.
func listTrialPath(listID string, from, to time.Time) string {
	query := url.Values{}
	query.Set("from", from.UTC().Format(time.RFC3339Nano))
	query.Set("to", to.UTC().Format(time.RFC3339Nano))

	return "/admin/v1/price-lists/" + listID + "/trial?" + query.Encode()
}
