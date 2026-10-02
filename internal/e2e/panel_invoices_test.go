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

// TestAnOperatorListsTheInvoicesInThePanel is ADR 0343 on the production
// wiring: an invoice issued from its order's page is listed on the Invoices
// screen through the registered `invoice.admin` surface, with its buyer and
// its total, among every status and the issued ones, and not among the
// canceled.
func TestAnOperatorListsTheInvoicesInThePanel(t *testing.T) {
	ctx := t.Context()
	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Panel Invoice Listed", map[string]int64{
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

	issued := send(http.MethodPost, adminui.OrdersPath+"/"+order.OrderID+"/invoice", url.Values{
		"new_series": {invoiceSeriesPrefix}, "buyer_name": {"Grace Hopper"}, "buyer_country": {"TR"},
	})
	require.Equal(t, http.StatusOK, issued.Code, issued.Body.String())
	number := panelInvoiceIssued.FindStringSubmatch(issued.Body.String())
	require.Len(t, number, 2)

	// row is the invoice's row on the screen, or empty when it is not listed.
	row := func(status string) string {
		t.Helper()

		rec := send(http.MethodGet, adminui.InvoicesPath+"?"+url.Values{"status": {status}}.Encode(), nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		_, after, found := strings.Cut(rec.Body.String(), ">"+number[1]+"</a></td>")
		if !found {
			return ""
		}
		after, _, _ = strings.Cut(after, "</tr>")

		return after
	}

	listed := row("")
	require.NotEmpty(t, listed, "the invoice is among every status")
	assert.Contains(t, listed, "<td>Grace Hopper</td>")
	assert.Contains(t, listed, "<td>sale</td>")
	assert.Contains(t, listed, "issued")
	assert.Regexp(t, `<td>\d+\.\d{2} `+taxedCurrency+`</td>`, listed, "the total in its currency's decimals")
	assert.NotEmpty(t, row("issued"), "and among the issued")
	assert.Empty(t, row("canceled"), "and not among the canceled")
}
