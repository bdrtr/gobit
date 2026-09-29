//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// TestATaxInclusiveStickerIsWhatTheOrderCharges sells in a market whose prices
// include their tax, from the cart to the order and its payment (ADR 0246,
// D169).
//
// ADR 0086 made the line's subtotal what is left of the sticker once its tax
// is taken out, and asserted the sticker at the cart's computed total; the cart
// module, the checkout and the order each held the subtotal to unit price x
// quantity, so the totals of such a cart could never be written and nothing
// could be sold in the market. The figures are the sticker 11_999 twice at 20%:
// 23_998 holds 3_999 of tax, and 19_999 is what is left.
func TestATaxInclusiveStickerIsWhatTheOrderCharges(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Tax-Inclusive Product", map[string]int64{
		taxedCurrency: 11_999,
	}, 10)

	cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{
		CountryCode: inclusiveTaxCountry,
		CustomerID:  customerID,
	})
	require.NoError(t, err)
	added, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID:    cart.CartID,
		VariantID: variantID,
		Quantity:  2,
	})
	require.NoError(t, err, "the cart's totals have to be written in a tax-inclusive market")

	assert.True(t, added.Totals.PricesIncludeTax)
	assertTotals(t, added.Totals, expectedTotal{
		subtotal: 19_999, discount: 0, tax: 3_999, shipping: 0, total: 23_998,
	}, "in the tax-inclusive market")

	stored, err := cartSvc.GetCart(ctx, cart.CartID)
	require.NoError(t, err)
	assert.True(t, stored.PricesIncludeTax, "the cart keeps the flag its totals were computed under")
	assert.Equal(t, int64(23_998), stored.Total)
	assert.False(t, stored.TotalsStale())

	placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cart.CartID,
		LocationID:        stockLocationID,
		PaymentProviderID: paymentmanual.ID,
		PaymentData:       paymentBehavior(t, paymentmanual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     23_998,
	})
	require.NoError(t, err, "a tax-inclusive cart has to become an order")

	order, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err)
	assert.True(t, order.PricesIncludeTax, "the order keeps the flag its lines were checked under")
	assert.Equal(t, int64(23_998), order.Total, "the sticker is what the order charges")
	assert.Equal(t, int64(19_999), order.Subtotal)
	assert.Equal(t, int64(3_999), order.TaxTotal)
	require.Len(t, order.Items, 1)
	assert.Equal(t, int64(11_999), order.Items[0].UnitPrice, "the unit price stays the sticker")
	assert.Equal(t, int64(19_999), order.Items[0].Subtotal)
	assert.Equal(t, int64(3_999), order.Items[0].TaxTotal)

	collection, err := paymentSvc.GetPaymentCollection(ctx, placed.PaymentCollectionID)
	require.NoError(t, err)
	assert.Equal(t, int64(23_998), collection.Amount, "the shopper pays the sticker")

	// The document holds its rows to the flag, so without the flag the order
	// would not be invoiced at all (ADR 0248).
	writeStoreProfile(t)
	recorder, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+placed.OrderID+"/invoice", issueInvoiceBody())
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	var issued invoiceIssueResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &issued))

	read, err := adminRequestWithBody(http.MethodGet, "/admin/v1/invoices/"+issued.Data.InvoiceID, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	var document struct {
		Data struct {
			PricesIncludeTax bool  `json:"prices_include_tax"`
			Subtotal         int64 `json:"subtotal"`
			TaxTotal         int64 `json:"tax_total"`
			Total            int64 `json:"total"`
			Lines            []struct {
				UnitPrice int64 `json:"unit_price"`
				Subtotal  int64 `json:"subtotal"`
				TaxTotal  int64 `json:"tax_total"`
			} `json:"lines"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(read.Body.Bytes(), &document))
	assert.True(t, document.Data.PricesIncludeTax, "the document says how its rows are read")
	assert.Equal(t, int64(23_998), document.Data.Total)
	assert.Equal(t, int64(19_999), document.Data.Subtotal)
	assert.Equal(t, int64(3_999), document.Data.TaxTotal)
	require.Len(t, document.Data.Lines, 1)
	assert.Equal(t, int64(11_999), document.Data.Lines[0].UnitPrice)
	assert.Equal(t, int64(19_999), document.Data.Lines[0].Subtotal)
}
