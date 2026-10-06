//go:build integration

package e2e

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// panelAs is the panel on the production wiring, sending as an operator
// holding exactly the scopes given.
func panelAs(t *testing.T, scopes ...string) func(method, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)

	return func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_desk", Kind: "user", Scopes: scopes,
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
}

// TestThePanelShowsTheMarginAnOrderWasPlacedAt is ADR 0412 on the production
// wiring: a variant's cost is written from its page, from the cost the page was
// drawn with, through the product module's registered surface; an order placed
// through the checkout keeps it, and the order list and the order page print
// the margin it was placed at and the line's cost through the order module's
// registered surface, after the variant's cost has moved on. A form drawn
// before that move is refused.
func TestThePanelShowsTheMarginAnOrderWasPlacedAt(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Panel Kettle", map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, happyInitialStock)
	variant, err := productSvc.GetVariant(ctx, variantID)
	require.NoError(t, err)

	variantPage := adminui.ProductsPath + "/" + variant.ProductID + "/variants/" + variantID
	catalogWriter := panelAs(t, "product:read", "product:write")
	page := catalogWriter(http.MethodGet, variantPage, nil)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Body.String(), "No unit cost is written for this variant.")

	added := catalogWriter(http.MethodPost, variantPage+"/cost", url.Values{
		"currency": {taxedCurrency}, "read_amount": {""}, "amount": {"313.37"},
	})
	require.Equal(t, http.StatusSeeOther, added.Code, added.Body.String())
	page = panelAs(t, "product:read")(http.MethodGet, variantPage, nil)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Body.String(), taxedCurrency+": 313.37", "a reader reads the cost it wrote")

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

	moved := catalogWriter(http.MethodPost, variantPage+"/cost", url.Values{
		"currency": {taxedCurrency}, "read_amount": {"313.37"}, "amount": {"440.00"},
	})
	require.Equal(t, http.StatusSeeOther, moved.Code, moved.Body.String())
	stale := catalogWriter(http.MethodPost, variantPage+"/cost", url.Values{
		"currency": {taxedCurrency}, "read_amount": {"313.37"}, "amount": {"1.00"},
	})
	require.Equal(t, http.StatusUnprocessableEntity, stale.Code, stale.Body.String())
	assert.Contains(t, stale.Body.String(), "the form was drawn with")
	costs, err := productSvc.VariantCosts(ctx, variantID)
	require.NoError(t, err)
	require.Len(t, costs, 1)
	assert.Equal(t, int64(44_000), costs[0].Amount, "the stale form wrote nothing")

	reader := panelAs(t, "order:read")
	list := reader(http.MethodGet, adminui.OrdersPath+"?customer="+customerID, nil)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	assert.Contains(t, list.Body.String(), "Placed margin")
	assert.Contains(t, list.Body.String(), "273.26 "+taxedCurrency,
		"900.00 of sales less 2 × 313.37, the cost the order was placed at")

	detail := reader(http.MethodGet, adminui.OrdersPath+"/"+order.OrderID, nil)
	require.Equal(t, http.StatusOK, detail.Code, detail.Body.String())
	body := detail.Body.String()
	assert.Contains(t, body, `<td class="num">313.37</td>`, "the line keeps the cost it was sold at")
	assert.Contains(t, body, "273.26 "+taxedCurrency+` <span class="muted">(sales 900.00 `+taxedCurrency+
		", cost 626.74 "+taxedCurrency+")</span>")
	assert.NotContains(t, body, "440.00", "a cost changed after the sale changes no order")
}
