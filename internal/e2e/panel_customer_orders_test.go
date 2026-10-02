//go:build integration

package e2e

import (
	"net/http"
	"net/http/httptest"
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

// TestAnOperatorReadsACustomersOrdersInThePanel is ADR 0358 on the
// production wiring: an order a customer placed is listed on their page
// through the order entity's customer filter, and on the order list asked
// for that customer, which lists no other customer's.
func TestAnOperatorReadsACustomersOrdersInThePanel(t *testing.T) {
	ctx := t.Context()
	variantID, _ := newStockedVariant(ctx, t, "E2E Panel Customer Order", map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, happyInitialStock)
	// place has a new customer place an order, returning the customer and
	// the order.
	place := func() (string, string) {
		t.Helper()

		customerID, email := newCustomer(ctx, t)
		cartID, _ := prepareCart(ctx, t, customerID, variantID, happyQuantity)
		order, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
			CartID: cartID, LocationID: stockLocationID, PaymentProviderID: paymentmanual.ID,
			PaymentData: paymentBehavior(t, paymentmanual.OutcomeAuthorize), Email: email, ExpectedTotal: happyTotal,
		})
		require.NoError(t, err)

		return customerID, order.OrderID
	}
	customerID, orderID := place()
	// Another customer's order, the newest, which neither list may show.
	_, otherOrderID := place()

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	get := func(path string) string {
		t.Helper()

		req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_support", Kind: "user", Scopes: []string{"customer:read", "order:read"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		return rec.Body.String()
	}
	link := `<a href="` + adminui.OrdersPath + "/" + orderID + `">#`
	other := `<a href="` + adminui.OrdersPath + "/" + otherOrderID + `">#`

	_, section, found := strings.Cut(get(adminui.CustomersPath+"/"+customerID), "<h2>Orders</h2>")
	require.True(t, found, "the customer's page lists their orders")
	section, _, _ = strings.Cut(section, "<h2>")
	assert.Contains(t, section, link)
	assert.NotContains(t, section, other, "and no other customer's")

	list := get(adminui.OrdersPath + "?customer=" + customerID)
	assert.Contains(t, list, link, "the order list lists the customer's order")
	assert.NotContains(t, list, other, "and no other customer's")
}
