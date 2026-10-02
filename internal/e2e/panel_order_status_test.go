//go:build integration

package e2e

import (
	"fmt"
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
)

// TestAnOperatorListsTheCanceledOrdersInThePanel is ADR 0361 on the
// production wiring: an order canceled on its page is on the order list
// asked for the canceled orders, through the order entity's status filter,
// and not on the list asked for the pending ones.
func TestAnOperatorListsTheCanceledOrdersInThePanel(t *testing.T) {
	ctx := t.Context()
	variantID, _ := newStockedVariant(ctx, t, "E2E Panel Status", map[string]int64{taxedCurrency: 10_000}, 5)
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
	send := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_support", Kind: "user", Scopes: []string{"order:read", "order:write"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	link := `<a href="` + adminui.OrdersPath + "/" + orderID + `">#`

	assert.Contains(t, send(http.MethodGet, adminui.OrdersPath+"?status=pending", nil).Body.String(), link,
		"a new order is pending")
	canceled := send(http.MethodPost, adminui.OrdersPath+"/"+orderID+"/cancel", url.Values{"reason": {"a duplicate"}})
	require.Less(t, canceled.Code, 400, canceled.Body.String())

	assert.Contains(t, send(http.MethodGet, adminui.OrdersPath+"?status=canceled", nil).Body.String(), link,
		"the canceled order is among the canceled")
	assert.NotContains(t, send(http.MethodGet, adminui.OrdersPath+"?status=pending", nil).Body.String(), link,
		"and no longer among the pending")
}
