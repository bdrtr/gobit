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

// TestAnOfflineOrderNeverPaidGivesItsStockBack is ADR 0285 on the production
// wiring: the customer never transfers, the shop cancels the order through the
// admin route, and the unit the checkout deducted comes back to the shelf. The
// bank transfer the order was promised is closed as well (ADR 0288), through
// the order's cancel event and the payment module's subscription.
func TestAnOfflineOrderNeverPaidGivesItsStockBack(t *testing.T) {
	ctx := t.Context()
	variantID, itemID := newStockedVariant(ctx, t, "Unpaid offline product",
		map[string]int64{taxedCurrency: 10_000}, 5)
	cartID, total := giftCart(t, variantID)

	completed := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		fmt.Sprintf(`{"payment_provider_id":%q,"expected_total":%d}`, offlineMethod, total))
	require.Equal(t, http.StatusOK, completed.Code, completed.Body.String())
	orderID, ok := storefrontData(t, completed)["order_id"].(string)
	require.True(t, ok, completed.Body.String())
	require.Equal(t, int64(4), stockLevel(ctx, t, itemID).StockedQuantity, "precondition: the sale deducted the unit")

	canceled, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+orderID+"/cancel",
		map[string]any{"reason": "the transfer never came"})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, canceled.Code, canceled.Body.String())

	// The condition reads without asserting: it runs on its own goroutine,
	// where a failed require would end that goroutine rather than the test.
	var stocked int64
	require.Eventually(t, func() bool {
		levels, err := inventorySvc.ListInventoryLevels(ctx, itemID)
		if err != nil || len(levels) != 1 {
			return false
		}
		stocked = levels[0].StockedQuantity
		return stocked == 5
	}, olayBeklemeSuresi, 20*time.Millisecond,
		"the canceled order's unit comes back to the shelf (last read %d)", stocked)

	var status string
	require.Eventually(t, func() bool {
		readErr := testPool.Pool().QueryRow(ctx, `
            SELECT s.status FROM payment_sessions s
            JOIN payment_collections c ON c.id = s.payment_collection_id
            WHERE c.reference = $1 AND s.provider_id = $2`, cartID, offlineMethod).Scan(&status)
		return readErr == nil && status == "canceled"
	}, olayBeklemeSuresi, 20*time.Millisecond,
		"the transfer the canceled order was promised is closed (last read %q)", status)
}
