//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// TestAnInvoiceRowNamesTheProductItSold is ADR 0365 on the production wiring:
// the checkout copies the product's title onto the order line beside the
// variant's, the invoice prints the two together, and renaming the product
// afterwards leaves the order as it was sold.
func TestAnInvoiceRowNamesTheProductItSold(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "1 kg / Filtre", map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, happyInitialStock)
	variant, err := productSvc.GetVariant(ctx, variantID)
	require.NoError(t, err)
	rename := func(title string) {
		t.Helper()

		_, err := productSvc.UpdateProduct(ctx, variant.ProductID, productsvc.UpdateProductInput{Title: &title})
		require.NoError(t, err)
	}
	rename("E2E Roastery Blend")

	cartID, _ := prepareCart(ctx, t, customerID, variantID, happyQuantity)
	order, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: paymentmanual.ID,
		PaymentData:       paymentBehavior(t, paymentmanual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     happyTotal,
	})
	require.NoError(t, err)

	rename("E2E Renamed Blend")
	detail, err := orderSvc.GetOrder(ctx, order.OrderID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)
	assert.Equal(t, "E2E Roastery Blend", detail.Items[0].ProductTitle,
		"the order keeps the product's title as it was sold")
	assert.Equal(t, "1 kg / Filtre", detail.Items[0].Title)

	writeStoreProfile(t)
	recorder, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+order.OrderID+"/invoice", issueInvoiceBody())
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	var issued invoiceIssueResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &issued))

	document, err := adminRequestWithBody(http.MethodGet, "/admin/v1/invoices/"+issued.Data.InvoiceID, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, document.Code, document.Body.String())
	var invoice invoiceDocumentResponse
	require.NoError(t, json.Unmarshal(document.Body.Bytes(), &invoice))
	require.NotEmpty(t, invoice.Data.Lines)
	assert.Equal(t, "E2E Roastery Blend — 1 kg / Filtre", invoice.Data.Lines[0].Description)

	line, err := adminRequestWithBody(http.MethodGet, "/admin/v1/orders/"+order.OrderID, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, line.Code, line.Body.String())
	assert.Contains(t, line.Body.String(), `"product_title":"E2E Roastery Blend"`,
		"the admin order record carries the product's title")
}
