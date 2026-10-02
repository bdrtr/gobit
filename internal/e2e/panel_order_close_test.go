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
	ordermodels "github.com/bdrtr/gobit/internal/modules/order/models"
)

// TestAnOperatorCompletesAndArchivesAnOrderInThePanel is ADR 0340 on the
// production wiring: a pending order's page marks it completed through the
// order module's registered surface, the completed order's page archives
// it, and the archived order is offered neither move.
func TestAnOperatorCompletesAndArchivesAnOrderInThePanel(t *testing.T) {
	ctx := t.Context()
	variantID, _ := newStockedVariant(ctx, t, "E2E Panel Completed", map[string]int64{taxedCurrency: 10_000}, 5)
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
	send := func(method, path string) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(url.Values{}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_support", Kind: "user", Scopes: []string{"order:read", "order:write"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	pagePath := adminui.OrdersPath + "/" + orderID
	marked := send(http.MethodPost, pagePath+"/complete")
	require.Equal(t, http.StatusOK, marked.Code, marked.Body.String())
	assert.Contains(t, marked.Body.String(), "The order was marked completed.")
	assert.Contains(t, marked.Body.String(), `action="`+pagePath+`/archive"`, "a completed order is offered the archive")

	archived := send(http.MethodPost, pagePath+"/archive")
	require.Equal(t, http.StatusOK, archived.Code, archived.Body.String())
	assert.Contains(t, archived.Body.String(), "The order was archived; it leaves the daily lists.")
	assert.NotContains(t, archived.Body.String(), `action="`+pagePath+`/complete"`)
	assert.NotContains(t, archived.Body.String(), `action="`+pagePath+`/archive"`, "an archived order is offered neither move")
	detail, err := orderSvc.GetOrder(ctx, orderID)
	require.NoError(t, err)
	assert.Equal(t, ordermodels.OrderArchived, detail.Status)
}
