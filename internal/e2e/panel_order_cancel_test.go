//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
	ordermodels "github.com/bdrtr/gobit/internal/modules/order/models"
	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// TestAnOperatorCancelsAnOrderInThePanel is ADR 0339 on the production
// wiring: an order placed on an offline method and never paid is canceled
// from its page through the order module's registered surface, the page says
// so and offers no second cancel, and the unit the checkout deducted comes
// back to the shelf; an order paid at checkout is refused with the module's
// reason, drawn on its page.
func TestAnOperatorCancelsAnOrderInThePanel(t *testing.T) {
	ctx := t.Context()
	variantID, itemID := newStockedVariant(ctx, t, "E2E Panel Canceled",
		map[string]int64{taxedCurrency: 10_000}, 5)
	cartID, total := giftCart(t, variantID)
	completed := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		fmt.Sprintf(`{"payment_provider_id":%q,"expected_total":%d}`, offlineMethod, total))
	require.Equal(t, http.StatusOK, completed.Code, completed.Body.String())
	orderID, ok := storefrontData(t, completed)["order_id"].(string)
	require.True(t, ok, completed.Body.String())
	require.Equal(t, int64(4), stockLevel(ctx, t, itemID).StockedQuantity, "precondition: the sale deducted the unit")

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

	pagePath := adminui.OrdersPath + "/" + orderID
	require.Contains(t, send(http.MethodGet, pagePath, nil).Body.String(), `action="`+pagePath+`/cancel"`,
		"a pending order is offered the cancel")
	canceled := send(http.MethodPost, pagePath+"/cancel", url.Values{"reason": {"the transfer never came"}})
	require.Equal(t, http.StatusOK, canceled.Code, canceled.Body.String())
	assert.Contains(t, canceled.Body.String(), "The order was canceled")
	assert.NotContains(t, canceled.Body.String(), `action="`+pagePath+`/cancel"`, "a canceled order offers no second cancel")
	detail, err := orderSvc.GetOrder(ctx, orderID)
	require.NoError(t, err)
	assert.Equal(t, ordermodels.OrderCanceled, detail.Status)
	var stocked int64
	require.Eventually(t, func() bool {
		levels, err := inventorySvc.ListInventoryLevels(ctx, itemID)
		if err != nil || len(levels) != 1 {
			return false
		}
		stocked = levels[0].StockedQuantity
		return stocked == 5
	}, olayBeklemeSuresi, 20*time.Millisecond, "the canceled order's unit comes back to the shelf (last read %d)", stocked)

	customerID, email := newCustomer(ctx, t)
	paidVariant, _ := newStockedVariant(ctx, t, "E2E Panel Paid", map[string]int64{taxedCurrency: happyUnitPrice}, happyInitialStock)
	paidCart, _ := prepareCart(ctx, t, customerID, paidVariant, happyQuantity)
	paid, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID: paidCart, LocationID: stockLocationID, PaymentProviderID: paymentmanual.ID,
		PaymentData: paymentBehavior(t, paymentmanual.OutcomeAuthorize), Email: email, ExpectedTotal: happyTotal,
	})
	require.NoError(t, err)
	refused := send(http.MethodPost, adminui.OrdersPath+"/"+paid.OrderID+"/cancel", url.Values{})
	require.Equal(t, http.StatusUnprocessableEntity, refused.Code, refused.Body.String())
	assert.Contains(t, refused.Body.String(), "cannot be canceled", "an order paid at checkout is refused")
}
