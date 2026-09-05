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

// This file proves that an order can be invoiced OVER HTTP.
//
// Everything below the endpoint is already covered elsewhere: the numbering by
// the invoice module's integration tests, the assembling by the flow's unit
// tests. What only this file can show is that the pieces find each other — the
// order module resolves the flow from the container by a name repeated in two
// packages, the flow resolves the invoice module's surface by another, and the
// link travels through the core's link service. A typo in any of those names
// compiles, and the endpoint answers 500 at the first request.

// invoiceSeriesPrefix is the series the scenario issues into.
//
// It is its own prefix rather than a shared one so the numbering assertions do
// not depend on what other tests in this package have issued.
const invoiceSeriesPrefix = "E2E"

// invoiceIssueResponse is what the issue endpoint answers with.
type invoiceIssueResponse struct {
	Data struct {
		InvoiceID     string `json:"invoice_id"`
		Number        string `json:"number"`
		AlreadyIssued bool   `json:"already_issued"`
	} `json:"data"`
}

// invoiceDocumentResponse is the document as the invoice module's own endpoint
// returns it.
type invoiceDocumentResponse struct {
	Data struct {
		Number       string `json:"number"`
		Status       string `json:"status"`
		CurrencyCode string `json:"currency_code"`
		Buyer        struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		} `json:"buyer"`
		Subtotal      int64 `json:"subtotal"`
		DiscountTotal int64 `json:"discount_total"`
		TaxTotal      int64 `json:"tax_total"`
		Total         int64 `json:"total"`
		Lines         []struct {
			Position    int32  `json:"position"`
			Description string `json:"description"`
			Quantity    int64  `json:"quantity"`
			TaxRateBps  int32  `json:"tax_rate_bps"`
			Total       int64  `json:"total"`
		} `json:"lines"`
	} `json:"data"`
}

// issueInvoiceBody is the body the endpoint takes.
//
// The two parties are in the body because neither side is in this framework's
// data: the seller's legal details are the shop's configuration and the buyer's
// tax number is not in the customer model.
func issueInvoiceBody() map[string]any {
	return map[string]any{
		"series_prefix": invoiceSeriesPrefix,
		"seller": map[string]any{
			"name":         "Gobit E2E Shop",
			"tax_number":   "1234567890",
			"tax_office":   "Kadikoy",
			"country_code": "TR",
		},
		"buyer": map[string]any{
			"name":         "E2E Customer",
			"country_code": "TR",
		},
	}
}

// TestAnOrderCanBeInvoicedOverHTTP is the whole chain in one scenario.
func TestAnOrderCanBeInvoicedOverHTTP(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Invoiced Product", map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, happyInitialStock)

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
	require.NotEmpty(t, order.OrderID)

	// --- the document is issued ---
	recorder, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+order.OrderID+"/invoice", issueInvoiceBody())
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, recorder.Code,
		"the first issue has to CREATE: %s", recorder.Body.String())

	var issued invoiceIssueResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &issued))
	require.NotEmpty(t, issued.Data.InvoiceID)
	assert.False(t, issued.Data.AlreadyIssued)
	assert.Len(t, issued.Data.Number, 16,
		"the number is 3 letters + 4 year digits + 9 sequence digits: %q", issued.Data.Number)
	assert.Equal(t, invoiceSeriesPrefix, issued.Data.Number[:3])

	// --- issuing again returns the SAME document ---
	//
	// A number is spent for good once it is taken, so a second press of the
	// button must not produce a second document. The status code carries the
	// difference as well, so a client that retried after a timeout can tell
	// whether its first attempt landed.
	again, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+order.OrderID+"/invoice", issueInvoiceBody())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, again.Code,
		"a second issue has to answer 200, not 201: %s", again.Body.String())

	var repeated invoiceIssueResponse
	require.NoError(t, json.Unmarshal(again.Body.Bytes(), &repeated))
	assert.True(t, repeated.Data.AlreadyIssued, "the second call has to say it created nothing")
	assert.Equal(t, issued.Data.Number, repeated.Data.Number,
		"a second call must NOT spend another number")

	// --- the order says which document it has ---
	linked, err := adminRequestWithBody(http.MethodGet,
		"/admin/v1/orders/"+order.OrderID+"/invoice", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, linked.Code, linked.Body.String())

	var identity invoiceIssueResponse
	require.NoError(t, json.Unmarshal(linked.Body.Bytes(), &identity))
	assert.Equal(t, issued.Data.Number, identity.Data.Number,
		"the link has to lead back to the document that was issued")

	// --- the document itself adds up and carries the order ---
	document, err := adminRequestWithBody(http.MethodGet,
		"/admin/v1/invoices/"+issued.Data.InvoiceID, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, document.Code, document.Body.String())

	var invoice invoiceDocumentResponse
	require.NoError(t, json.Unmarshal(document.Body.Bytes(), &invoice))

	assert.Equal(t, issued.Data.Number, invoice.Data.Number)
	assert.Equal(t, "issued", invoice.Data.Status, "a document is born issued")
	assert.Equal(t, taxedCurrency, invoice.Data.CurrencyCode)
	assert.Equal(t, email, invoice.Data.Buyer.Email,
		"the buyer's e-mail is the one field the ORDER knows, and it has to be filled in")
	assert.Equal(t, "E2E Customer", invoice.Data.Buyer.Name,
		"everything else about the buyer comes from the caller")

	assert.Equal(t, happySubtotal, invoice.Data.Subtotal)
	assert.Equal(t, happyTax, invoice.Data.TaxTotal)
	assert.Equal(t, happyTotal, invoice.Data.Total,
		"the document's total has to be the ORDER's total, unchanged")
	assert.Equal(t,
		invoice.Data.Subtotal-invoice.Data.DiscountTotal+invoice.Data.TaxTotal,
		invoice.Data.Total,
		"the document's identity has to hold")

	require.Len(t, invoice.Data.Lines, 1,
		"this order has no carriage, so the document has one line")
	assert.Equal(t, int32(1), invoice.Data.Lines[0].Position, "rows are numbered from one")
	assert.Equal(t, happyQuantity, invoice.Data.Lines[0].Quantity)
	assert.Positive(t, invoice.Data.Lines[0].TaxRateBps,
		"the RATE the line was charged at has to reach the document; a zero here means it was "+
			"dropped somewhere between the cart's calculation and the printed row")
}

// TestAnOrderWithoutAnInvoiceAnswersNotFound covers the read side's empty case.
//
// A 200 with an empty body would be the wrong answer: a client asking which
// document an order has, and getting one with no number, cannot tell that from
// a document whose number failed to encode.
func TestAnOrderWithoutAnInvoiceAnswersNotFound(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Uninvoiced Product", map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, happyInitialStock)

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

	recorder, err := adminRequestWithBody(http.MethodGet,
		"/admin/v1/orders/"+order.OrderID+"/invoice", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
}
