//go:build integration

package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/workflows/stockalert"
)

// stockAlertMails returns the captured back-in-stock mails about a variant.
func (s *notificationProviderSpy) stockAlertMails(variantID string) []coreprovider.Notification {
	s.mu.Lock()
	defer s.mu.Unlock()

	var found []coreprovider.Notification
	for i := range s.captured {
		if s.captured[i].Template == stockalert.TemplateBackInStock &&
			s.captured[i].Data[stockalert.DataVariantID] == variantID {
			found = append(found, s.captured[i])
		}
	}
	return found
}

// TestAWishlistVariantBackInStockIsMailedOnce is ADR 0215 on the production
// wiring: a proven shopper marks a variant that is out of stock, a pass arms
// the mark, the variant is restocked, and the next pass mails the shopper's
// own address once and clears the mark.
func TestAWishlistVariantBackInStockIsMailedOnce(t *testing.T) {
	ctx := t.Context()
	shopper, address := newCustomer(ctx, t)
	variantID, itemID := newStockedVariant(ctx, t, "E2E Stock Alert Product", map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, 0)
	alert := "/store/v1/customers/" + shopper + "/wishlist/" + variantID + "/stock-alert"

	marked := identifiedStorefrontRequest(t, shopper, http.MethodPut, alert, "")
	require.Equal(t, http.StatusOK, marked.Code, marked.Body.String())
	assert.Equal(t, true, storefrontData(t, marked)["stock_alert"])

	_, _, _, err := stockAlerts.Pass(ctx)
	require.NoError(t, err)
	assert.Empty(t, notificationSpy.stockAlertMails(variantID), "out of stock mails nothing")

	_, err = inventorySvc.SetInventoryLevel(ctx, itemID, stockLocationID, 5)
	require.NoError(t, err)
	_, _, _, err = stockAlerts.Pass(ctx)
	require.NoError(t, err)

	mails := notificationSpy.stockAlertMails(variantID)
	require.Len(t, mails, 1)
	assert.Equal(t, address, mails[0].To, "the shopper's own address")
	assert.Equal(t, "E2E Stock Alert Product", mails[0].Data[stockalert.DataProductTitle])

	list := identifiedStorefrontRequest(t, shopper, http.MethodGet, "/store/v1/customers/"+shopper+"/wishlist", "")
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	assert.Contains(t, list.Body.String(), `"stock_alert":false`, "the mail cleared the mark")

	_, _, _, err = stockAlerts.Pass(ctx)
	require.NoError(t, err)
	assert.Len(t, notificationSpy.stockAlertMails(variantID), 1, "once")
}

// TestAnExpectedReceiptMailsNoStockAlert is ADR 0425 on the production wiring:
// a supplier receipt expected for a marked variant puts a date on the page
// (ADR 0399) and mails nothing, a second receipt expected later mails nothing
// either, and the units received are what mails the shopper, once.
func TestAnExpectedReceiptMailsNoStockAlert(t *testing.T) {
	ctx := t.Context()
	shopper, _ := newCustomer(ctx, t)
	variantID, itemID := newStockedVariant(ctx, t, "E2E Stock Alert Expected", map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, 0)
	alert := "/store/v1/customers/" + shopper + "/wishlist/" + variantID + "/stock-alert"
	marked := identifiedStorefrontRequest(t, shopper, http.MethodPut, alert, "")
	require.Equal(t, http.StatusOK, marked.Code, marked.Body.String())
	_, _, _, err := stockAlerts.Pass(ctx)
	require.NoError(t, err)

	soon := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	receipt := recordSupplierReceipt(t, itemID, 3, soon)
	recordSupplierReceipt(t, itemID, 2, soon.Add(48*time.Hour))
	at, inStock := restockOf(ctx, t, variantID)
	require.False(t, inStock)
	require.True(t, soon.Equal(at), "the page shows when units are expected: %v", at)
	_, _, _, err = stockAlerts.Pass(ctx)
	require.NoError(t, err)
	assert.Empty(t, notificationSpy.stockAlertMails(variantID), "an expected receipt is a date, not stock")

	receiveSupplierReceipt(t, itemID, receipt, 3)
	_, _, _, err = stockAlerts.Pass(ctx)
	require.NoError(t, err)
	assert.Len(t, notificationSpy.stockAlertMails(variantID), 1, "the units received mail the shopper")
}

// TestAStockAlertNeedsTheProvenShopper: another shopper's session cannot mark a
// list that is not theirs.
func TestAStockAlertNeedsTheProvenShopper(t *testing.T) {
	ctx := t.Context()
	shopper, _ := newCustomer(ctx, t)
	stranger, _ := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Stock Alert Stranger", map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, 0)

	refused := identifiedStorefrontRequest(t, stranger, http.MethodPut,
		"/store/v1/customers/"+shopper+"/wishlist/"+variantID+"/stock-alert", "")

	assert.Equal(t, http.StatusForbidden, refused.Code, refused.Body.String())
}
