package invoicing_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/workflows/invoicing"
)

// TestInvoicingAnOrderProducesADocument covers the ordinary path and the shape
// of what it assembles.
func TestInvoicingAnOrderProducesADocument(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	out, err := h.flow.IssueForOrder(context.Background(), validIssue())
	require.NoError(t, err)

	assert.Equal(t, "inv_1", out.InvoiceID)
	assert.Equal(t, "GBT2026000000001", out.Number)
	assert.False(t, out.AlreadyIssued)

	document := h.invoices.lastDocument(t)
	assert.Equal(t, "GBT", document.SeriesPrefix)
	assert.Equal(t, "sale", document.Kind)
	assert.Equal(t, "TRY", document.CurrencyCode)
	assert.Equal(t, "Gobit Shop", document.Seller.Name)
}

// TestTheDocumentAddsUpAfterCarriageBecomesALine is the arithmetic the move has
// to preserve.
//
// The order keeps carriage as a separate total; a document has no such term and
// prints it as a row. The document's subtotal therefore carries the carriage,
// and its total is the ORDER'S total unchanged — if that stopped holding, the
// invoice module would refuse the document, which is the check working, but the
// mistake would be here.
func TestTheDocumentAddsUpAfterCarriageBecomesALine(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	_, err := h.flow.IssueForOrder(context.Background(), validIssue())
	require.NoError(t, err)

	document := h.invoices.lastDocument(t)

	require.Len(t, document.Lines, 2, "one product line and one carriage line")
	assert.Equal(t, "Shipping", document.Lines[1].Description)
	assert.Equal(t, int64(2500), document.Lines[1].Total)

	assert.Equal(t, int64(4500), document.Subtotal, "the subtotal carries the carriage")
	assert.Equal(t, int64(400), document.TaxTotal)
	assert.Equal(t, int64(4900), document.Total, "the total is the ORDER's total unchanged")
	assert.Equal(t, document.Subtotal-document.DiscountTotal+document.TaxTotal, document.Total,
		"the document's identity has to hold after the move")
}

// TestTheLineTaxRateReachesTheDocument is why the rate was put on the order
// line in the first place.
//
// The document prints the rate of every row, and the rate cannot be recomputed
// from the amount: rounding down per line maps a range of rates onto one figure.
func TestTheLineTaxRateReachesTheDocument(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	_, err := h.flow.IssueForOrder(context.Background(), validIssue())
	require.NoError(t, err)

	assert.Equal(t, int32(2000), h.invoices.lastDocument(t).Lines[0].TaxRateBps)
}

// TestIssuingTwiceDoesNotSpendASecondNumber is the flow's own guarantee.
//
// A number is spent for good once it is taken (ADR 0024). A double-click that
// issued a second document would burn a number, put two documents on one sale,
// and leave the shop to cancel one of them — and a cancellation is a permanent
// mark on a record that should never have existed.
func TestIssuingTwiceDoesNotSpendASecondNumber(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	first, err := h.flow.IssueForOrder(ctx, validIssue())
	require.NoError(t, err)
	require.False(t, first.AlreadyIssued)

	second, err := h.flow.IssueForOrder(ctx, validIssue())
	require.NoError(t, err)

	assert.True(t, second.AlreadyIssued, "the second call has to say it created nothing")
	assert.Equal(t, first.InvoiceID, second.InvoiceID)
	assert.Equal(t, first.Number, second.Number)
	assert.Equal(t, 1, h.invoices.issues, "a second document must NOT have been issued")
}

// TestTheBuyerEmailIsFilledInFromTheOrder covers the one field the order does
// know.
//
// Everything else about the buyer — the tax number, the tax office — is not in
// this framework's customer model, so it comes from the caller. The e-mail is,
// and making the caller repeat it would invite it to be repeated wrongly.
func TestTheBuyerEmailIsFilledInFromTheOrder(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	_, err := h.flow.IssueForOrder(context.Background(), validIssue())
	require.NoError(t, err)

	assert.Equal(t, "customer@example.test", h.invoices.lastDocument(t).Buyer.Email)
}

// TestAGivenBuyerEmailIsNotOverwritten holds the other half of that rule.
//
// A shop that invoices a company sends the document to the accounts address,
// which is not the one the order was placed with.
func TestAGivenBuyerEmailIsNotOverwritten(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	in := validIssue()
	in.Buyer.Email = "accounts@company.test"

	_, err := h.flow.IssueForOrder(context.Background(), in)
	require.NoError(t, err)

	assert.Equal(t, "accounts@company.test", h.invoices.lastDocument(t).Buyer.Email)
}

// TestAnOrderWithNoLinesIsRefused keeps an empty document from being issued.
//
// The invoice module would refuse it too, but by then the request has already
// traveled; refusing here says which order is the problem.
func TestAnOrderWithNoLinesIsRefused(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.orders.order.Items = nil

	_, err := h.flow.IssueForOrder(context.Background(), validIssue())
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Zero(t, h.invoices.issues, "no number may be spent on an order with nothing on it")
}

// TestAFailedLinkIsReportedAndNotSwallowed is the residual the flow has to be
// honest about.
//
// The document was issued and the number is spent; what failed is the binding.
// Returning success would leave the next call unable to find it and ready to
// spend another number, so the call fails with both identifiers in the message.
func TestAFailedLinkIsReportedAndNotSwallowed(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.links.linkErr = errors.Internal("fake_link_down", "the link store is unreachable")

	_, err := h.flow.IssueForOrder(context.Background(), validIssue())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GBT2026000000001",
		"the message has to carry the number that was spent")
	assert.Contains(t, err.Error(), "order_1")
}

// TestReadingAnOrderWithNoInvoiceIsNotFound covers the read side.
func TestReadingAnOrderWithNoInvoiceIsNotFound(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	_, _, _, err := h.flow.InvoiceOfOrder(context.Background(), "order_1")
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestReadingAnInvoicedOrderReturnsItsIdentity is the reader that makes the
// link worth writing.
func TestReadingAnInvoicedOrderReturnsItsIdentity(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	_, err := h.flow.IssueForOrder(ctx, validIssue())
	require.NoError(t, err)

	invoiceID, number, status, err := h.flow.InvoiceOfOrder(ctx, "order_1")
	require.NoError(t, err)

	assert.Equal(t, "inv_1", invoiceID)
	assert.Equal(t, "GBT2026000000001", number)
	assert.Equal(t, "issued", status)
}

// TestTheSellerComesFromTheShopsOwnRecord is the decision in one assertion.
//
// The caller cannot name the issuer, so two documents from one shop cannot name
// two different sellers.
func TestTheSellerComesFromTheShopsOwnRecord(t *testing.T) {
	h := newHarness(t)
	h.profile.profile = storeProfile{
		LegalName: "Another Shop Ltd", TaxNumber: "9876543210",
		TaxOffice: "Central", Address: "1 Example Street", CountryCode: "DE",
	}

	_, err := h.flow.IssueForOrder(context.Background(), validIssue())
	require.NoError(t, err)

	document := h.invoices.lastDocument(t)
	assert.Equal(t, "Another Shop Ltd", document.Seller.Name,
		"the shop's legal_name becomes the party's name; the two spell it differently "+
			"and a decoder that matched field names would leave this empty")
	assert.Equal(t, "9876543210", document.Seller.TaxNumber)
	assert.Equal(t, "Central", document.Seller.TaxOffice)
	assert.Equal(t, "1 Example Street", document.Seller.Address)
	assert.Equal(t, "DE", document.Seller.CountryCode)
	assert.Equal(t, 1, h.profile.calls, "the record is read once per document")
}

// TestNoDocumentIsIssuedBeforeTheShopSaysWhoItIs refuses the state that would
// otherwise print an issuer of nobody.
func TestNoDocumentIsIssuedBeforeTheShopSaysWhoItIs(t *testing.T) {
	h := newHarness(t)
	h.profile.err = errors.NotFound("settings_store_profile_not_found",
		"the shop has not said who it is yet")

	_, err := h.flow.IssueForOrder(context.Background(), validIssue())

	require.Error(t, err)
	assert.Equal(t, invoicing.CodeSellerUnknown, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "/admin/v1/store-profile",
		"the operator issuing their first invoice is exactly the one who has not "+
			"written the profile; the message has to say where to go")
	assert.Empty(t, h.invoices.issued, "and nothing was issued")
}

// validIssue is a request that passes every rule.
func validIssue() invoicing.IssueInput {
	return invoicing.IssueInput{
		OrderID:      "order_1",
		SeriesPrefix: "GBT",
		Buyer:        invoicing.Party{Name: "A Customer", CountryCode: "TR"},
	}
}

// issuedDocument is the body the flow sends to the invoice module.
type issuedDocument struct {
	SeriesPrefix  string `json:"series_prefix"`
	Kind          string `json:"kind"`
	CurrencyCode  string `json:"currency_code"`
	Seller        party  `json:"seller"`
	Buyer         party  `json:"buyer"`
	Lines         []line `json:"lines"`
	Subtotal      int64  `json:"subtotal"`
	DiscountTotal int64  `json:"discount_total"`
	TaxTotal      int64  `json:"tax_total"`
	Total         int64  `json:"total"`
}

// party is one side of that body.
type party struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	// The rest of the printed party, read since ADR 0115 made the seller a
	// record: a mapping that dropped a field would otherwise be invisible here.
	TaxNumber   string `json:"tax_number"`
	TaxOffice   string `json:"tax_office"`
	Address     string `json:"address"`
	CountryCode string `json:"country_code"`
}

// line is one row of that body.
type line struct {
	Description   string    `json:"description"`
	Quantity      int64     `json:"quantity"`
	Subtotal      int64     `json:"subtotal"`
	TaxRateBps    int32     `json:"tax_rate_bps"`
	TaxTotal      int64     `json:"tax_total"`
	Total         int64     `json:"total"`
	TaxComponents []lineTax `json:"tax_components"`
}

// lineTax is one rate inside a stacked row's tax on that body.
type lineTax struct {
	RateID        string `json:"rate_id"`
	RateBps       int32  `json:"rate_bps"`
	Compound      bool   `json:"compound"`
	TaxableAmount int64  `json:"taxable_amount"`
	TaxAmount     int64  `json:"tax_amount"`
}

// decodeDocument reads the body the flow sent.
func decodeDocument(t *testing.T, raw json.RawMessage) issuedDocument {
	t.Helper()

	var out issuedDocument
	require.NoError(t, json.Unmarshal(raw, &out))

	return out
}
