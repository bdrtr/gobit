//go:build integration

package e2e

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
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

// panelInvoiceIssued reads the number the issue form reports.
var panelInvoiceIssued = regexp.MustCompile(`Invoice ([A-Z0-9]{3}\d{13}) was issued\.`)

// TestAnOperatorInvoicesAnOrderInThePanel is ADR 0335 on the production
// wiring: an order's page says it has no invoice and offers the form through
// the order module's registered surface; the invoice is issued on the series
// named through the invoicing flow the API calls, the page then names it and
// offers no second one, and the same form sent again issues nothing new.
func TestAnOperatorInvoicesAnOrderInThePanel(t *testing.T) {
	ctx := t.Context()
	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Panel Invoiced", map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, happyInitialStock)
	cartID, _ := prepareCart(ctx, t, customerID, variantID, happyQuantity)
	order, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID: cartID, LocationID: stockLocationID, PaymentProviderID: paymentmanual.ID,
		PaymentData: paymentBehavior(t, paymentmanual.OutcomeAuthorize), Email: email, ExpectedTotal: happyTotal,
	})
	require.NoError(t, err)
	writeStoreProfile(t)

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	send := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_accounts", Kind: "user", Scopes: []string{"order:read", "order:write", "invoice:read"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	pagePath := adminui.OrdersPath + "/" + order.OrderID
	page := send(http.MethodGet, pagePath, nil).Body.String()
	assert.Contains(t, page, "No invoice has been issued for this order.")
	require.Contains(t, page, `action="`+pagePath+`/invoice"`, "a writer is offered the form")

	// The order was billed to no address, so the buyer is typed; the e-mail
	// is the order's.
	form := url.Values{
		"new_series": {strings.ToLower(invoiceSeriesPrefix)}, "buyer_name": {"Ada Lovelace"},
		"buyer_address": {"12 Main St, Springfield"}, "buyer_country": {"tr"}, "tax_number": {"1234567890"},
	}
	issued := send(http.MethodPost, pagePath+"/invoice", form)
	require.Equal(t, http.StatusOK, issued.Code, issued.Body.String())
	number := panelInvoiceIssued.FindStringSubmatch(issued.Body.String())
	require.Len(t, number, 2, "the page names the invoice it issued")
	assert.Equal(t, invoiceSeriesPrefix, number[1][:3], "on the series named, upper-cased")
	assert.Contains(t, issued.Body.String(), "Invoice <strong>"+number[1]+"</strong>", "the page names the order's invoice")
	assert.NotContains(t, issued.Body.String(), `action="`+pagePath+`/invoice"`, "and offers no second one")

	again := send(http.MethodPost, pagePath+"/invoice", form)
	require.Equal(t, http.StatusOK, again.Code, again.Body.String())
	assert.Contains(t, again.Body.String(), "This order already had invoice "+number[1]+"; nothing new was issued.")
}
