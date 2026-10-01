//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// offlineMethod is the offline payment method the harness names (ADR 0284).
const offlineMethod = "bank_transfer"

// TestAnOfflineMethodPlacesAnOrderTheShopCapturesLater is ADR 0284 on the
// production wiring: a guest completes a cart with a bank transfer through the
// storefront, the order is placed owing its total with its stock deducted, and
// the operator's capture of the session — the money arrived — raises the
// order's paid total through the payment event.
func TestAnOfflineMethodPlacesAnOrderTheShopCapturesLater(t *testing.T) {
	ctx := t.Context()
	variantID, itemID := newStockedVariant(ctx, t, "Offline payment product",
		map[string]int64{taxedCurrency: 10_000}, 5)
	cartID, total := giftCart(t, variantID)

	completed := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		fmt.Sprintf(`{"payment_provider_id":%q,"expected_total":%d}`, offlineMethod, total))
	require.Equal(t, http.StatusOK, completed.Code, completed.Body.String())
	data := storefrontData(t, completed)
	assert.Equal(t, float64(total), data["total"])
	assert.Equal(t, float64(total), data["outstanding"], "the order owes its whole total")
	orderID, ok := data["order_id"].(string)
	require.True(t, ok, completed.Body.String())

	placed, err := orderSvc.GetOrder(ctx, orderID)
	require.NoError(t, err)
	assert.Zero(t, placed.Summary.PaidTotal, "nothing is paid at the checkout")
	assert.Empty(t, captures(t, cartID), "nothing is captured at the checkout")
	assert.Equal(t, int64(4), stockLevel(ctx, t, itemID).StockedQuantity, "the order's stock is deducted")

	var sessionID, status string
	require.NoError(t, testPool.Pool().QueryRow(ctx, `
        SELECT s.id, s.status FROM payment_sessions s
        JOIN payment_collections c ON c.id = s.payment_collection_id
        WHERE c.reference = $1 AND s.provider_id = $2`, cartID, offlineMethod).Scan(&sessionID, &status))
	assert.Equal(t, "authorized", status, "the promise is authorized and waits")

	captured, err := adminRequestWithBody(http.MethodPost, "/admin/v1/payment-sessions/"+sessionID+"/capture", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, captured.Code, captured.Body.String())

	var paid int64
	require.Eventually(t, func() bool {
		order, readErr := orderSvc.GetOrder(ctx, orderID)
		if readErr != nil {
			return false
		}
		paid = order.Summary.PaidTotal
		return paid == total
	}, olayBeklemeSuresi, 20*time.Millisecond,
		"the money the shop recorded reaches the order (expected %d, last read %d)", total, paid)
	assert.Equal(t, map[string]int64{offlineMethod: total}, captures(t, cartID))
}
