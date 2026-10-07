//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// An exchange that names its return prices what it sends, through the wiring
// production runs (ADR 0432): the order module asks the cart flows' quote by
// name, the two sides read each other's JSON, and the refund flow reads the
// order's answer that the return is the exchange's.

// exchangeJacketPrice is the jacket an exchange sends for a shirt. It is a
// figure no other test uses, so a price that leaks from elsewhere shows.
const exchangeJacketPrice int64 = 61_000

// pricedExchangeResponse is the exchange record with the field this record adds.
type pricedExchangeResponse struct {
	Data struct {
		ID            string `json:"id"`
		Status        string `json:"status"`
		DifferenceDue int64  `json:"difference_due"`
		ReturnID      string `json:"return_id"`
	} `json:"data"`
}

// pricedReplacementResponse is a replacement record with its items' prices.
type pricedReplacementResponse struct {
	Data struct {
		ID    string `json:"id"`
		Items []struct {
			VariantID string `json:"variant_id"`
			Price     *struct {
				UnitPrice  int64  `json:"unit_price"`
				Total      int64  `json:"total"`
				TaxTotal   int64  `json:"tax_total"`
				TaxRateBps int32  `json:"tax_rate_bps"`
				PricedBy   string `json:"priced_by"`
			} `json:"price"`
		} `json:"items"`
	} `json:"data"`
}

// TestAnExchangeThatNamesItsReturnPricesWhatItSends walks the record over HTTP:
// a shirt comes back on a return, the exchange that takes it back owes it, a
// jacket the order never sold is priced as the buyer's cart would price it, the
// difference is the jacket less the shirt and is funded at that figure, and the
// return refunds nothing of its own.
//
// Red before the change: the exchange took no return_id, its difference was the
// figure typed, and the replacement item carried no price.
func TestAnExchangeThatNamesItsReturnPricesWhatItSends(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	shirtID, _ := newStockedVariant(ctx, t, "E2E Exchanged Shirt",
		map[string]int64{taxedCurrency: happyUnitPrice}, happyInitialStock)
	jacketID, _ := newStockedVariant(ctx, t, "E2E Exchange Jacket",
		map[string]int64{taxedCurrency: exchangeJacketPrice}, happyInitialStock)
	cartID, _ := prepareCart(ctx, t, customerID, shirtID, happyQuantity)
	placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID: cartID, LocationID: stockLocationID, PaymentProviderID: paymentmanual.ID,
		PaymentData: paymentBehavior(t, paymentmanual.OutcomeAuthorize), Email: email,
		ExpectedTotal: happyTotal,
	})
	require.NoError(t, err)
	order, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err)
	require.Len(t, order.Items, 1)
	base := "/admin/v1/orders/" + placed.OrderID

	returned, err := adminRequestWithBody(http.MethodPost, base+"/returns",
		map[string]any{"reason": "a size up", "lines": []map[string]any{{
			"order_line_item_id": order.Items[0].ID, "quantity": 1,
		}}})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, returned.Code, returned.Body.String())
	var ret afterSalesRecordResponse
	require.NoError(t, json.Unmarshal(returned.Body.Bytes(), &ret))

	opened, err := adminRequestWithBody(http.MethodPost, base+"/exchanges",
		map[string]any{"return_id": ret.Data.ID, "note": "the jacket instead"})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, opened.Code, opened.Body.String())
	var exchange pricedExchangeResponse
	require.NoError(t, json.Unmarshal(opened.Body.Bytes(), &exchange))
	shirtWorth := happyTotal / happyQuantity
	assert.Equal(t, ret.Data.ID, exchange.Data.ReturnID)
	assert.Equal(t, -shirtWorth, exchange.Data.DifferenceDue, "it owes the shirt that comes back")

	sent, err := adminRequestWithBody(http.MethodPost, base+"/exchanges/"+exchange.Data.ID+"/replacements",
		map[string]any{"shipping_option_id": "so_exchange", "location_id": stockLocationID,
			"lines": []map[string]any{{"variant_id": jacketID, "quantity": 1}}})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, sent.Code, sent.Body.String())
	var replacement pricedReplacementResponse
	require.NoError(t, json.Unmarshal(sent.Body.Bytes(), &replacement))
	require.Len(t, replacement.Data.Items, 1)
	price := replacement.Data.Items[0].Price
	require.NotNil(t, price, "the jacket is priced when it is written; body: %s", sent.Body.String())
	jacketTax := exchangeJacketPrice * int64(taxRateBps) / 10_000
	assert.Equal(t, "quote", price.PricedBy)
	assert.Equal(t, exchangeJacketPrice, price.UnitPrice, "the price list's price in the order's region")
	assert.Equal(t, jacketTax, price.TaxTotal, "taxed by the tax module as the cart taxes it")
	assert.Equal(t, taxRateBps, price.TaxRateBps)
	assert.Equal(t, exchangeJacketPrice+jacketTax, price.Total)

	read, err := adminRequestWithBody(http.MethodGet, base+"/exchanges/"+exchange.Data.ID, nil)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(read.Body.Bytes(), &exchange))
	difference := exchangeJacketPrice + jacketTax - shirtWorth
	assert.Equal(t, difference, exchange.Data.DifferenceDue, "what it sends less what comes back")

	funded, err := adminRequestWithBody(http.MethodPost, base+"/exchanges/"+exchange.Data.ID+"/funding",
		map[string]any{"payment_collection_id": collectDifference(t, placed.OrderID, difference, difference)})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, funded.Code, funded.Body.String())

	received, err := adminRequestWithBody(http.MethodPost, base+"/returns/"+ret.Data.ID+"/receive",
		map[string]any{"location_id": stockLocationID})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, received.Code, received.Body.String())
	refunded, err := adminRequestWithBody(http.MethodPost, base+"/returns/"+ret.Data.ID+"/refund",
		map[string]any{"amount": shirtWorth, "reason": "a size up"})
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, refunded.Code,
		"the shirt's worth is already in the exchange's difference; body: %s", refunded.Body.String())
	assert.Contains(t, refunded.Body.String(), "returns_workflow_return_settled_by_exchange")

	untouched, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err)
	assert.Zero(t, untouched.Summary.RefundedTotal, "no money went back for the shirt")
}
