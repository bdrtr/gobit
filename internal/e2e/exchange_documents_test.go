//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ordermodels "github.com/bdrtr/gobit/internal/modules/order/models"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	taxsvc "github.com/bdrtr/gobit/internal/modules/tax/service"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// An exchange that names its return is documented as a return and a sale, on
// the wiring production runs (ADR 0432, second commit; D247): the order module
// lists the exchange as one act, the invoicing flow issues a refund of the
// returned units on their sale rows and an amending sale adding a row per item
// sent, and the order journal moves each one's tax against sales.

// exchangeReducedBps is the jacket's rate, a class the shirt is not taxed in.
const exchangeReducedBps int32 = 100

// reducedRateOn taxes the variant's product at exchangeReducedBps in the taxed
// country, as a rate rule naming the product.
func reducedRateOn(t *testing.T, variantID string) {
	t.Helper()

	ctx := t.Context()
	page, err := taxSvc.ListTaxRegions(ctx, taxedCountry, 0, 0)
	require.NoError(t, err)
	var rootID string
	for i := range page.Items {
		if page.Items[i].IsRoot() {
			rootID = page.Items[i].ID
		}
	}
	require.NotEmpty(t, rootID)
	variant, err := productSvc.GetVariant(ctx, variantID)
	require.NoError(t, err)
	rate, err := taxSvc.CreateTaxRate(ctx, taxsvc.CreateTaxRateInput{
		TaxRegionID: rootID, Name: fmt.Sprintf("E2E Exchange Reduced %d", fixtureCounter.Add(1)),
		RateBps: exchangeReducedBps,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = taxSvc.DeleteTaxRate(ctx, rate.ID) })
	_, err = taxSvc.CreateRateRule(ctx, taxsvc.CreateRateRuleInput{
		TaxRateID: rate.ID, Reference: "product", ReferenceID: variant.ProductID,
	})
	require.NoError(t, err)
}

// exchangeScene is an invoiced order of two shirts with one of them on a
// return an exchange takes back.
type exchangeScene struct {
	orderID, collectionID, lineID, exchangeID, optionID, returnID string
	order                                                         ordermodels.OrderDetail
	from                                                          time.Time
}

// openExchangeScene places the order of two shirts, invoices it, opens a
// return of one shirt and an exchange that names it.
func openExchangeScene(t *testing.T, shirtTitle string) exchangeScene {
	t.Helper()

	ctx := t.Context()
	scene := exchangeScene{from: time.Now().UTC().Add(-time.Second)}
	writeStoreProfile(t)
	customerID, email := newCustomer(ctx, t)
	shirtID, _ := newStockedVariant(ctx, t, shirtTitle,
		map[string]int64{taxedCurrency: happyUnitPrice}, happyInitialStock)
	cartID, _ := prepareCart(ctx, t, customerID, shirtID, happyQuantity)
	placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID: cartID, LocationID: stockLocationID, PaymentProviderID: paymentmanual.ID,
		PaymentData: paymentBehavior(t, paymentmanual.OutcomeAuthorize), Email: email,
		ExpectedTotal: happyTotal,
	})
	require.NoError(t, err)
	scene.orderID, scene.collectionID = placed.OrderID, placed.PaymentCollectionID
	scene.order, err = orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err)
	require.Len(t, scene.order.Items, 1)
	scene.lineID = scene.order.Items[0].ID
	base := "/admin/v1/orders/" + scene.orderID

	issued, err := adminRequestWithBody(http.MethodPost, base+"/invoice", issueInvoiceBody())
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, issued.Code, issued.Body.String())

	returned, err := adminRequestWithBody(http.MethodPost, base+"/returns",
		map[string]any{"reason": "the other one", "lines": []map[string]any{{
			"order_line_item_id": scene.lineID, "quantity": 1,
		}}})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, returned.Code, returned.Body.String())
	var ret afterSalesRecordResponse
	require.NoError(t, json.Unmarshal(returned.Body.Bytes(), &ret))
	scene.returnID = ret.Data.ID

	opened, err := adminRequestWithBody(http.MethodPost, base+"/exchanges",
		map[string]any{"return_id": ret.Data.ID, "note": "documented"})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, opened.Code, opened.Body.String())
	var exchange pricedExchangeResponse
	require.NoError(t, json.Unmarshal(opened.Body.Bytes(), &exchange))
	scene.exchangeID = exchange.Data.ID
	scene.optionID = newShippingOption(ctx, t, newShippingProfile(ctx, t, "E2E Exchange Documents"),
		"E2E Exchange Documents Shipping", 0, false)

	return scene
}

// send writes a replacement of one line and returns its id.
func (s exchangeScene) send(t *testing.T, line map[string]any) string {
	t.Helper()

	rec, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+s.orderID+"/exchanges/"+s.exchangeID+"/replacements",
		map[string]any{"shipping_option_id": s.optionID, "location_id": stockLocationID,
			"lines": []map[string]any{line}})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var replacement pricedReplacementResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &replacement))

	return replacement.Data.ID
}

// dispatch sends the replacement's goods.
func (s exchangeScene) dispatch(t *testing.T, replacementID string) {
	t.Helper()

	rec, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+s.orderID+"/exchanges/"+s.exchangeID+"/replacements/"+replacementID+"/dispatch", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// receive takes the returned goods back into the warehouse.
func (s exchangeScene) receive(t *testing.T) {
	t.Helper()

	rec, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+s.orderID+"/returns/"+s.returnID+"/receive",
		map[string]any{"location_id": stockLocationID})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// issuedAnswer is the amendment endpoint's answer: the act's documents, each
// saying whether this request issued it.
type issuedAnswer struct {
	Data struct {
		AlreadyIssued bool `json:"already_issued"`
		Documents     []struct {
			InvoiceID string `json:"invoice_id"`
			Kind      string `json:"kind"`
			Issued    bool   `json:"issued"`
		} `json:"documents"`
	} `json:"data"`
}

// document asks for the exchange's documents and returns the answer.
func (s exchangeScene) document(t *testing.T) (code int, body string) {
	t.Helper()

	rec, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+s.orderID+"/invoice/amendments",
		map[string]any{"series_prefix": invoiceSeriesPrefix, "act": map[string]any{"kind": "exchange", "id": s.exchangeID}})
	require.NoError(t, err)

	return rec.Code, rec.Body.String()
}

// exchangeDocument is one of the exchange's two documents, with its rows.
type exchangeDocument struct {
	Kind, Reason    string
	Total, TaxTotal int64
	RateBps         []int32
	Descriptions    []string
	AmendsSaleRows  []string
}

// exchangeDocuments reads the documents amending the order's sale under the
// exchange's two keys.
func (s exchangeScene) exchangeDocuments(t *testing.T) (refund, sale *exchangeDocument) {
	t.Helper()

	saleID := saleOf(t, s.orderID)
	for _, amendment := range amendmentsOf(t, saleID).Data {
		var into **exchangeDocument
		switch amendment.AmendmentKey {
		case "exchange_returned:" + s.exchangeID:
			into = &refund
		case "exchange_sent:" + s.exchangeID:
			into = &sale
		default:
			continue
		}
		read := adminCartRequest(t, http.MethodGet, "/admin/v1/invoices/"+amendment.ID, "")
		require.Equal(t, http.StatusOK, read.Code, read.Body.String())
		var body struct {
			Data struct {
				Lines []struct {
					Description  string `json:"description"`
					TaxRateBps   int32  `json:"tax_rate_bps"`
					AmendsLineID string `json:"amends_line_id"`
				} `json:"lines"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(read.Body.Bytes(), &body))
		document := &exchangeDocument{
			Kind: amendment.Kind, Reason: amendment.AmendmentReason,
			Total: amendment.Total, TaxTotal: amendment.TaxTotal,
		}
		for _, line := range body.Data.Lines {
			document.RateBps = append(document.RateBps, line.TaxRateBps)
			document.Descriptions = append(document.Descriptions, line.Description)
			document.AmendsSaleRows = append(document.AmendsSaleRows, line.AmendsLineID)
		}
		*into = document
	}

	return refund, sale
}

// TestAnExchangesDocumentsCarryWhatItsBuyerPaid is D247's exchange half on the
// production wiring: one of two shirts at 20% comes back on a return, the
// exchange that names it sends a jacket the order never sold, taxed 1% by a
// rule on its product, the difference is funded and the jacket leaves; once
// the shirt has come back the exchange is documented as a refund of it on its
// sale row, at
// the row's tax, and an amending sale adding the jacket at its quote's 1%, so
// the documents end at what the buyer paid and kept paid, and the order
// journal's tax_payable moves by the jacket's tax less the shirt's.
//
// Red before the change: the order listed no exchange act, so the request
// naming it answered 404 invoicing_act_unknown, and an exchange's funding is
// refused as on no document. Red before the review's follow-up: the exchange
// was documented before the shirt came back, and the answer named the sale
// alone.
func TestAnExchangesDocumentsCarryWhatItsBuyerPaid(t *testing.T) {
	ctx := t.Context()
	scene := openExchangeScene(t, "E2E Documented Shirt")
	jacketID, _ := newStockedVariant(ctx, t, "E2E Documented Jacket",
		map[string]int64{taxedCurrency: exchangeJacketPrice}, happyInitialStock)
	reducedRateOn(t, jacketID)

	replacementID := scene.send(t, map[string]any{"variant_id": jacketID, "quantity": 1})
	jacketTax := exchangeJacketPrice * int64(exchangeReducedBps) / 10_000
	shirtWorth, shirtTax := happyTotal/happyQuantity, scene.order.TaxTotal/happyQuantity
	difference := exchangeJacketPrice + jacketTax - shirtWorth
	read, err := adminRequestWithBody(http.MethodGet,
		"/admin/v1/orders/"+scene.orderID+"/exchanges/"+scene.exchangeID, nil)
	require.NoError(t, err)
	var exchange pricedExchangeResponse
	require.NoError(t, json.Unmarshal(read.Body.Bytes(), &exchange))
	require.Equal(t, difference, exchange.Data.DifferenceDue,
		"the jacket is quoted at its own 1%%: %s", read.Body.String())

	collected := collectDifference(t, scene.orderID, difference, difference)
	funded, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+scene.orderID+"/exchanges/"+scene.exchangeID+"/funding",
		map[string]any{"payment_collection_id": collected})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, funded.Code, funded.Body.String())
	scene.dispatch(t, replacementID)

	early, body := scene.document(t)
	require.Equal(t, http.StatusConflict, early, "the shirt has not come back: %s", body)
	assert.Contains(t, body, "invoicing_act_not_documented")
	scene.receive(t)

	code, body := scene.document(t)
	require.Equal(t, http.StatusCreated, code, "the exchange is one act with two documents: %s", body)
	var issued issuedAnswer
	require.NoError(t, json.Unmarshal([]byte(body), &issued))
	require.Len(t, issued.Data.Documents, 2, body)
	assert.Equal(t, "refund", issued.Data.Documents[0].Kind)
	assert.Equal(t, "sale", issued.Data.Documents[1].Kind)
	assert.True(t, issued.Data.Documents[0].Issued && issued.Data.Documents[1].Issued, body)
	again, body := scene.document(t)
	assert.Equal(t, http.StatusOK, again, "a second press issues nothing: %s", body)
	var repeated issuedAnswer
	require.NoError(t, json.Unmarshal([]byte(body), &repeated))
	assert.True(t, repeated.Data.AlreadyIssued)
	require.Len(t, repeated.Data.Documents, 2, body)
	assert.False(t, repeated.Data.Documents[0].Issued || repeated.Data.Documents[1].Issued, body)
	assert.Equal(t, issued.Data.Documents[1].InvoiceID, repeated.Data.Documents[1].InvoiceID)

	refund, sale := scene.exchangeDocuments(t)
	require.NotNil(t, refund, "the shirt's return is on a refund")
	require.NotNil(t, sale, "the jacket is on an amending sale")
	assert.Equal(t, "refund", refund.Kind)
	assert.Equal(t, "returned", refund.Reason)
	assert.Equal(t, shirtWorth, refund.Total, "the shirt at what it was sold for")
	assert.Equal(t, shirtTax, refund.TaxTotal, "the shirt row's own tax")
	assert.Equal(t, []int32{taxRateBps}, refund.RateBps)
	assert.NotEmpty(t, refund.AmendsSaleRows[0], "the refund names the shirt's sale row")
	assert.Equal(t, "sale", sale.Kind)
	assert.Equal(t, "exchanged", sale.Reason)
	assert.Equal(t, exchangeJacketPrice+jacketTax, sale.Total)
	assert.Equal(t, jacketTax, sale.TaxTotal, "the jacket at its own 1%, not the shirt's 20%")
	assert.Equal(t, []int32{exchangeReducedBps}, sale.RateBps)
	assert.Equal(t, []string{"E2E Documented Jacket"}, sale.Descriptions, "the goods sent are named")
	assert.Equal(t, []string{""}, sale.AmendsSaleRows, "a row the sale did not have")

	assert.Equal(t, paidFor(t, scene.from, scene.collectionID, collected), documentedFor(t, scene.orderID),
		"the sale and its amendments add up to what the buyer paid and kept paid")

	books := orderBooks(t, scene.orderID, scene.from, time.Now().UTC().Add(time.Minute))
	assert.Equal(t, scene.order.TaxTotal-shirtTax+jacketTax, books[ordermodels.AccountTaxPayable],
		"tax_payable moves by the jacket's tax less the shirt's")
	assert.Equal(t, happyUnitPrice*happyQuantity-happyUnitPrice+exchangeJacketPrice, books[ordermodels.AccountSales],
		"sales hold the goods kept and sent, net of their tax")

	rec := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+scene.orderID+"/invoice/amendments", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var acts struct {
		Data []struct {
			Kind         string `json:"kind"`
			ID           string `json:"id"`
			Amount       int64  `json:"amount"`
			Documentable bool   `json:"documentable"`
			Document     *struct {
				InvoiceID string `json:"invoice_id"`
			} `json:"document"`
			Documents []struct {
				Kind string `json:"kind"`
			} `json:"documents"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &acts))
	var listed bool
	for _, act := range acts.Data {
		if act.Kind != "exchange" {
			continue
		}
		listed = true
		assert.Equal(t, scene.exchangeID, act.ID)
		assert.Equal(t, exchangeJacketPrice+jacketTax, act.Amount, "what the exchange sends")
		assert.True(t, act.Documentable)
		assert.NotNil(t, act.Document, "both documents stand")
		require.Len(t, act.Documents, 2, rec.Body.String())
		assert.Equal(t, "refund", act.Documents[0].Kind)
		assert.Equal(t, "sale", act.Documents[1].Kind)
	}
	assert.True(t, listed, "the exchange is listed as an act: %s", rec.Body.String())
}

// TestAnEvenSwapNetsToNothing sends the shirt line's own units back for the
// unit returned: the exchange owes nothing, writes no money entry, and its two
// documents are equal and opposite, so tax_payable does not move.
//
// Red before the change: the order listed no exchange act (404
// invoicing_act_unknown).
func TestAnEvenSwapNetsToNothing(t *testing.T) {
	scene := openExchangeScene(t, "E2E Swapped Shirt")
	replacementID := scene.send(t, map[string]any{"order_line_item_id": scene.lineID, "quantity": 1})
	scene.dispatch(t, replacementID)
	scene.receive(t)

	code, body := scene.document(t)
	require.Equal(t, http.StatusCreated, code, body)
	refund, sale := scene.exchangeDocuments(t)
	require.NotNil(t, refund)
	require.NotNil(t, sale)
	assert.Equal(t, refund.Total, sale.Total, "equal and opposite")
	assert.Equal(t, refund.TaxTotal, sale.TaxTotal, "equal and opposite in tax")
	assert.Equal(t, happyTotal/happyQuantity, sale.Total)
	assert.Equal(t, refund.RateBps, sale.RateBps)
	assert.Equal(t, []string{"E2E Swapped Shirt"}, sale.Descriptions)

	journal, err := orderSvc.Journal(t.Context(), ordersvc.JournalQuery{
		From: scene.from, To: time.Now().UTC().Add(time.Minute),
	})
	require.NoError(t, err)
	for _, entry := range journal.Entries {
		if entry.OrderID == scene.orderID {
			assert.NotContains(t, []ordermodels.JournalKind{
				ordermodels.JournalExchangeFunded, ordermodels.JournalExchangeRefunded,
			}, entry.Kind, "an even swap moves no money")
		}
	}
	books := orderBooks(t, scene.orderID, scene.from, time.Now().UTC().Add(time.Minute))
	assert.Equal(t, scene.order.TaxTotal, books[ordermodels.AccountTaxPayable], "tax_payable does not move")
	assert.Equal(t, paidFor(t, scene.from, scene.collectionID), documentedFor(t, scene.orderID))
}
