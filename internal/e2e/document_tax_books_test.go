//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ordermodels "github.com/bdrtr/gobit/internal/modules/order/models"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestAnOrderCanceledUnderACreditsDocumentOwesItsTaxBack is ADR 0419's stated
// cost on the production wiring: an order is invoiced, a credit on it is
// documented, and the order is then canceled while that document stands. The
// cancellation reverses the whole placement and touches no document, so the
// order's tax_payable on the books ends at minus the document's tax until the
// document is canceled too.
func TestAnOrderCanceledUnderACreditsDocumentOwesItsTaxBack(t *testing.T) {
	ctx := t.Context()
	from := time.Now().UTC().Add(-time.Second)
	writeStoreProfile(t)

	variantID, _ := newStockedVariant(ctx, t, "E2E Credited Then Canceled",
		map[string]int64{taxedCurrency: 10_000}, 5)
	cartID, total := giftCart(t, variantID)
	completed := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		fmt.Sprintf(`{"payment_provider_id":%q,"expected_total":%d}`, offlineMethod, total))
	require.Equal(t, http.StatusOK, completed.Code, completed.Body.String())
	orderID, ok := storefrontData(t, completed)["order_id"].(string)
	require.True(t, ok, completed.Body.String())
	order, err := orderSvc.GetOrder(ctx, orderID)
	require.NoError(t, err)
	require.Positive(t, order.TaxTotal, "precondition: the order is taxed")

	issued, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+orderID+"/invoice",
		issueInvoiceBody())
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, issued.Code, "body: %s", issued.Body.String())
	_, err = orderSvc.CreateCreditLine(ctx, orderID, ordersvc.CreateCreditLineInput{
		Amount: total / 2, Reason: "goodwill",
	})
	require.NoError(t, err)
	creditID, _, _ := actOf(t, orderID, "credit_line")
	require.Equal(t, http.StatusCreated, documentAct(t, orderID, "credit_line", creditID))

	var documentTax int64
	var document string
	for _, amendment := range amendmentsOf(t, saleOf(t, orderID)).Data {
		if amendment.AmendmentKey == "credit_line:"+creditID {
			documentTax, document = amendment.TaxTotal, amendment.ID
		}
	}
	require.NotEmpty(t, document, "the credit's document amends the sale")
	require.Positive(t, documentTax, "precondition: the credit's document gives tax back")

	canceled, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+orderID+"/cancel",
		map[string]any{"reason": "the buyer withdrew"})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, canceled.Code, canceled.Body.String())

	books := orderBooks(t, orderID, from, time.Now().UTC().Add(time.Minute))
	assert.Equal(t, -documentTax, books[ordermodels.AccountTaxPayable],
		"the cancellation took the whole tax back and the standing document gave its part back again")

	voided, err := adminRequestWithBody(http.MethodPost, "/admin/v1/invoices/"+document+"/status",
		map[string]any{"status": "canceled", "reason": "the order was canceled"})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, voided.Code, voided.Body.String())
	books = orderBooks(t, orderID, from, time.Now().UTC().Add(time.Minute))
	assert.Zero(t, books[ordermodels.AccountTaxPayable], "the document canceled too, the order owes no tax")
}
