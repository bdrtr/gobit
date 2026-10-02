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

// panelInvoiceOpened is the order page's link to its invoice.
var panelInvoiceOpened = regexp.MustCompile(`<a href="(/admin/ui/invoices/[^"]+)">Open the invoice</a>`)

// TestAnOperatorMovesAnInvoiceInThePanel is ADR 0344 on the production
// wiring: the order page links its invoice's page, which shows the document
// as issued through the registered `invoice.admin` surface; the page moves
// it from the status it was drawn in; and the same form sent again, now
// stale, is refused on the page with the document as it is now.
func TestAnOperatorMovesAnInvoiceInThePanel(t *testing.T) {
	ctx := t.Context()
	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Panel Invoice Moved", map[string]int64{
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
			ID: "usr_accounts", Kind: "user",
			Scopes: []string{"order:read", "order:write", "invoice:read", "invoice:write"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	orderPage := adminui.OrdersPath + "/" + order.OrderID
	issued := send(http.MethodPost, orderPage+"/invoice", url.Values{
		"new_series": {invoiceSeriesPrefix}, "buyer_name": {"Grace Hopper"}, "tax_number": {"12345678901"},
	})
	require.Equal(t, http.StatusOK, issued.Code, issued.Body.String())
	number := panelInvoiceIssued.FindStringSubmatch(issued.Body.String())
	require.Len(t, number, 2)
	link := panelInvoiceOpened.FindStringSubmatch(send(http.MethodGet, orderPage, nil).Body.String())
	require.Len(t, link, 2, "the order page links its invoice")

	page := send(http.MethodGet, link[1], nil)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	for _, want := range []string{
		"Invoice " + number[1], `<span class="pill">issued</span>`, "Grace Hopper", "12345678901",
		"E2E Panel Invoice Moved", `name="read_status" value="issued"`,
		`<option value="sent">sent</option><option value="canceled">canceled</option>`,
	} {
		assert.Contains(t, page.Body.String(), want)
	}

	sent := send(http.MethodPost, link[1]+"/status", url.Values{"read_status": {"issued"}, "to": {"sent"}})
	require.Equal(t, http.StatusSeeOther, sent.Code, sent.Body.String())
	landed := send(http.MethodGet, sent.Header().Get("Location"), nil).Body.String()
	assert.Contains(t, landed, "The invoice was moved to sent.")
	assert.Contains(t, landed, `<span class="pill">sent</span>`)

	stale := send(http.MethodPost, link[1]+"/status", url.Values{
		"read_status": {"issued"}, "to": {"canceled"}, "reason": {"a duplicate"},
	})
	require.Equal(t, http.StatusUnprocessableEntity, stale.Code, stale.Body.String())
	assert.Contains(t, stale.Body.String(), "draw the page again")
	assert.Contains(t, stale.Body.String(), `name="read_status" value="sent"`, "the page is drawn from the document as it is now")

	canceled := send(http.MethodPost, link[1]+"/status", url.Values{
		"read_status": {"sent"}, "to": {"canceled"}, "reason": {"a duplicate"},
	})
	require.Equal(t, http.StatusSeeOther, canceled.Code, canceled.Body.String())
	landed = send(http.MethodGet, canceled.Header().Get("Location"), nil).Body.String()
	assert.Contains(t, landed, "Why: a duplicate")
	assert.NotContains(t, landed, `/status"`, "a canceled document has no move left")
}
