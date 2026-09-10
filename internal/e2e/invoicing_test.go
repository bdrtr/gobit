//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
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
		// No seller: the shop's identity is its own record since ADR 0115, and a
		// body that still carried one would be refused as an unknown field.
		"buyer": map[string]any{
			"name":         "E2E Customer",
			"country_code": "TR",
		},
	}
}

// The shop's identity, as the harness writes it before any document is issued.
const (
	e2eShopName      = "Gobit E2E Shop"
	e2eShopTaxNumber = "1234567890"
	e2eShopTaxOffice = "Central"
	e2eShopAddress   = "1 Example Street"
)

// storeProfileBody is the shop's identity as the admin endpoint takes it.
func storeProfileBody() map[string]any {
	return map[string]any{
		"legal_name":   e2eShopName,
		"tax_number":   e2eShopTaxNumber,
		"tax_office":   e2eShopTaxOffice,
		"email":        "billing@example.test",
		"address":      e2eShopAddress,
		"country_code": "TR",
	}
}

// writeStoreProfile makes sure the shop has said who it is.
//
// It is called by every test that issues a document, and it is idempotent: PUT
// replaces, so running it in any order leaves the same record. The alternative —
// writing it once in TestMain — would hide the endpoint from the authorization
// walk and from the failure this ordering exposes, which is a test issuing a
// document against a profile another test wrote.
func writeStoreProfile(t *testing.T) {
	t.Helper()

	recorder, err := adminRequestWithBody(http.MethodPut,
		"/admin/v1/store-profile", storeProfileBody())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, recorder.Code,
		"the shop's identity has to be writable: %s", recorder.Body.String())
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
	//
	// The shop says who it is FIRST: the seller is no longer part of the body
	// and issuing before the profile exists is refused (ADR 0115).
	writeStoreProfile(t)

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

// TestTheDocumentNamesTheShopFromItsOwnRecord is the hop no unit test can make.
//
// The settings module answers with "legal_name" and the document's party field
// is "name"; the two ends cannot import each other, so nothing but a real
// request through both modules can say the mapping is right. A decoder that
// matched field names would leave the seller nameless and every other assertion
// in this file would still pass.
func TestTheDocumentNamesTheShopFromItsOwnRecord(t *testing.T) {
	ctx := t.Context()

	writeStoreProfile(t)

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Seller Product", map[string]int64{
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

	issued, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+order.OrderID+"/invoice", issueInvoiceBody())
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, issued.Code, issued.Body.String())

	var identity invoiceIssueResponse
	require.NoError(t, json.Unmarshal(issued.Body.Bytes(), &identity))

	document, err := adminRequestWithBody(http.MethodGet,
		"/admin/v1/invoices/"+identity.Data.InvoiceID, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, document.Code, document.Body.String())

	var printed struct {
		Data struct {
			Seller struct {
				Name      string `json:"name"`
				TaxNumber string `json:"tax_number"`
				TaxOffice string `json:"tax_office"`
				Address   string `json:"address"`
			} `json:"seller"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(document.Body.Bytes(), &printed))

	assert.Equal(t, e2eShopName, printed.Data.Seller.Name,
		"the shop's legal_name is what the document prints as the seller's name")
	assert.Equal(t, e2eShopTaxNumber, printed.Data.Seller.TaxNumber)
	assert.Equal(t, e2eShopTaxOffice, printed.Data.Seller.TaxOffice)
	assert.Equal(t, e2eShopAddress, printed.Data.Seller.Address)
}

// TestTheStoreProfileConstraintsAreTheLastDefence checks the three rules the
// schema holds on its own.
//
// The service refuses all three first, which is exactly why they need a test
// that goes around it: these statements are the shape a hand-written INSERT or a
// second writer takes, and a CHECK nobody exercises is a CHECK nobody notices
// the loss of.
func TestTheStoreProfileConstraintsAreTheLastDefence(t *testing.T) {
	ctx := t.Context()

	writeStoreProfile(t)

	for name, statement := range map[string]string{
		"no legal name": `UPDATE store_profile SET legal_name = '' WHERE id = 'default'`,
		"a country that is not a code": `UPDATE store_profile SET country_code = 'TUR'
			WHERE id = 'default'`,
		"a second profile": `INSERT INTO store_profile (id, legal_name, country_code)
			VALUES ('second', 'Another Shop', 'TR')`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := testPool.Pool().Exec(ctx, statement)

			require.Error(t, err, "the schema has to refuse it on its own")
		})
	}

	// And the record is still the one the shop wrote.
	var name string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT legal_name FROM store_profile WHERE id = 'default'`).Scan(&name))
	assert.Equal(t, e2eShopName, name)
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

// invoiceLineTax is one rate inside a document row's tax, as the invoice
// module's own endpoint returns it.
type invoiceLineTax struct {
	Position      int32  `json:"position"`
	RateID        string `json:"rate_id"`
	RateBps       int32  `json:"rate_bps"`
	Compound      bool   `json:"compound"`
	TaxableAmount int64  `json:"taxable_amount"`
	TaxAmount     int64  `json:"tax_amount"`
}

// TestAStackedLineReachesTheDocumentWithNoFakeInBetween binds the three hops the
// breakdown crosses, on real modules.
//
// # Why this test exists
//
// ADR 0097 said the breakdown reached the document and it did not. The invoicing
// flow had been taught to read `tax_components` and the invoice module to store
// it, and the flow's test passed — because the flow's FAKE order surface sent
// the key. The real producer never wrote it, and no test asked the producer.
// A document kept printing the stack's base rate while a record said the limit
// had closed.
//
// Every piece here is real: the order module writes the components, its invoice
// surface encodes them, the invoicing flow reads them over the container, the
// invoice module stores them, and the assertion reads them back over HTTP. A
// break in ANY of the four turns this red.
func TestAStackedLineReachesTheDocumentWithNoFakeInBetween(t *testing.T) {
	ctx := t.Context()

	// 5% + 8% compound on 2000: 100 and 168, adding to the 268 the row carries.
	placed, err := orderSvc.CreateOrder(ctx, ordersvc.CreateOrderInput{
		RegionID:     taxedRegionID,
		Email:        "stacked@example.test",
		CurrencyCode: taxedCurrency,
		Subtotal:     2000,
		TaxTotal:     268,
		Total:        2268,
		Items: []ordersvc.CreateOrderItemInput{{
			VariantID:  "variant_stacked_e2e",
			Title:      "A Book",
			Quantity:   2,
			UnitPrice:  1000,
			Subtotal:   2000,
			TaxRateBps: 500,
			TaxTotal:   268,
			Total:      2268,
			TaxComponents: []ordersvc.CreateOrderLineTaxInput{
				{RateID: "txr_base", RateBps: 500, TaxableAmount: 2000, TaxAmount: 100},
				{RateID: "txr_top", RateBps: 800, Compound: true, TaxableAmount: 2100, TaxAmount: 168},
			},
		}},
	})
	require.NoError(t, err)

	completed, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+placed.ID+"/complete", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, completed.Code,
		"the order could not be completed; body: %s", completed.Body.String())

	writeStoreProfile(t)

	issued, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+placed.ID+"/invoice", issueInvoiceBody())
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, issued.Code,
		"the invoice could not be issued; body: %s", issued.Body.String())

	invoiceID := decodeIssuedInvoice(t, issued.Body.Bytes())

	read, err := adminRequestWithBody(http.MethodGet, "/admin/v1/invoices/"+invoiceID, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, read.Code,
		"the document could not be read back; body: %s", read.Body.String())

	var document struct {
		Data struct {
			Lines []struct {
				Description   string           `json:"description"`
				TaxRateBps    int32            `json:"tax_rate_bps"`
				TaxTotal      int64            `json:"tax_total"`
				TaxComponents []invoiceLineTax `json:"tax_components"`
			} `json:"lines"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(read.Body.Bytes(), &document))
	require.NotEmpty(t, document.Data.Lines)

	goods := document.Data.Lines[0]
	assert.Equal(t, int32(500), goods.TaxRateBps, "the row still carries the stack's base")
	require.Len(t, goods.TaxComponents, 2,
		"the document has to print BOTH rates; a fake in the middle is what hid this")

	assert.Equal(t, int32(1), goods.TaxComponents[0].Position, "a document counts from one")
	assert.Equal(t, "txr_base", goods.TaxComponents[0].RateID)
	assert.Equal(t, int64(100), goods.TaxComponents[0].TaxAmount)

	assert.Equal(t, int32(2), goods.TaxComponents[1].Position)
	assert.Equal(t, int32(800), goods.TaxComponents[1].RateBps)
	assert.True(t, goods.TaxComponents[1].Compound)
	assert.Equal(t, int64(2100), goods.TaxComponents[1].TaxableAmount)
	assert.Equal(t, int64(168), goods.TaxComponents[1].TaxAmount)

	assert.Equal(t, goods.TaxTotal,
		goods.TaxComponents[0].TaxAmount+goods.TaxComponents[1].TaxAmount,
		"and the printed components have to add up to the row")
}
