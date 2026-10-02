//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
)

// TestAnOperatorFindsAnAuthorizedPaymentInThePanel is ADR 0357 on the
// production wiring: an order placed to be paid offline leaves a collection
// authorized in full, which the Payments screen lists on its first tab
// through the payment module's collection entity, its order named through
// the order_payment link, and not among the captured.
func TestAnOperatorFindsAnAuthorizedPaymentInThePanel(t *testing.T) {
	ctx := t.Context()
	variantID, _ := newStockedVariant(ctx, t, "E2E Panel Authorized", map[string]int64{taxedCurrency: 10_000}, 5)
	cartID, total := giftCart(t, variantID)
	completed := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		fmt.Sprintf(`{"payment_provider_id":%q,"expected_total":%d}`, offlineMethod, total))
	require.Equal(t, http.StatusOK, completed.Code, completed.Body.String())
	orderID, ok := storefrontData(t, completed)["order_id"].(string)
	require.True(t, ok, completed.Body.String())

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	list := func(query string) string {
		t.Helper()

		req := httptest.NewRequest(http.MethodGet, adminui.PaymentsPath+query, http.NoBody)
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_accounts", Kind: "user", Scopes: []string{"payment:read", "order:read"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		return rec.Body.String()
	}
	link := `<a href="` + adminui.OrdersPath + "/" + orderID + `">#`

	awaiting := list("")
	_, row, found := strings.Cut(awaiting, link)
	require.True(t, found, "the order's collection is authorized, on the first tab")
	row, _, _ = strings.Cut(row, "</tr>")
	assert.Contains(t, row, fmt.Sprintf("<td>%d.%02d %s</td>", total/100, total%100, taxedCurrency), "for the order's total")
	assert.NotContains(t, list("?status=captured"), link, "and not among the captured")
}
