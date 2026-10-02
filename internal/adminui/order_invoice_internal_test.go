package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// fakeInvoicer acts on after-sales records as the shared fake does and names
// and issues the order's invoice as scripted, recording each issue.
type fakeInvoicer struct {
	fakeAfterSales
	found    bool
	readErr  error
	issued   []string
	already  bool
	issueErr error
}

func (f *fakeInvoicer) InvoiceOfOrder(context.Context, string) (id, number, status string, found bool, err error) {
	if f.readErr != nil {
		return "", "", "", false, f.readErr
	}
	if !f.found {
		return "", "", "", false, nil
	}
	return "inv_1", "GBT2026000000007", "issued", true, nil
}

func (f *fakeInvoicer) IssueInvoice(
	_ context.Context, orderID, prefix string, buyer json.RawMessage,
) (id, number string, already bool, err error) {
	f.issued = append(f.issued, orderID+"|"+prefix+"|"+string(buyer))
	return "inv_1", "GBT2026000000007", f.already, f.issueErr
}

// fakeSeries lists the series as scripted.
type fakeSeries struct {
	body   string
	listed int
}

func (f *fakeSeries) SeriesJSON(context.Context) (json.RawMessage, error) {
	f.listed++
	return json.RawMessage(f.body), nil
}

// invoicePanel is a panel over the order with the invoice surfaces.
func invoicePanel(t *testing.T, invoicer *fakeInvoicer, series *fakeSeries) *UI {
	t.Helper()

	panel := newCatalogPanel(t, linkedOrderCatalog())
	panel.afterSales = invoicer
	if series != nil {
		panel.invoices = series
	}
	panel.scopes = builtInScopes()

	return panel
}

// invoiceSection is the order page's invoice section.
func invoiceSection(t *testing.T, body string) string {
	t.Helper()

	_, section, found := strings.Cut(body, "<h2>Invoice</h2>")
	require.True(t, found, "the order page has an invoice section")
	section, _, _ = strings.Cut(section, "<h2>")

	return section
}

// TestAnOrdersInvoiceIsIssuedOnItsPage is ADR 0335: the page names the
// order's invoice, or says it has none, to a reader; a writer of an order
// with none is offered the form, the series the shop has offered only to one
// who may read the invoices, each once and the latest year's first; the
// invoice is issued on the series chosen, or on a new one named, with the
// buyer's tax identity, and the page says which, or that the order already
// had one.
func TestAnOrdersInvoiceIsIssuedOnItsPage(t *testing.T) {
	t.Parallel()

	page := OrdersPath + "/order_1"
	writer := []string{scopeOrderRead, scopeOrderWrite, scopeInvoiceRead}
	series := &fakeSeries{body: `[{"prefix":"GBT","year":2026,"last_number":9},
		{"prefix":"IAD","year":2026,"last_number":2},{"prefix":"GBT","year":2025,"last_number":40}]`}
	invoicer := &fakeInvoicer{}
	panel := invoicePanel(t, invoicer, series)

	rec := campaignsRequest(panel, http.MethodGet, page, nil, scopeOrderRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	section := invoiceSection(t, rec.Body.String())
	assert.Contains(t, section, "No invoice has been issued for this order.")
	assert.NotContains(t, section, "<form", "a reader issues nothing")
	assert.Zero(t, series.listed, "a reader's page reads no series")

	section = invoiceSection(t, campaignsRequest(panel, http.MethodGet, page, nil, writer...).Body.String())
	assert.Contains(t, section, `action="`+page+`/invoice"`)
	assert.Contains(t, section, `<option value="GBT">GBT</option><option value="IAD">IAD</option>`,
		"each series once, the latest year's first")
	assert.Equal(t, 1, strings.Count(section, `value="GBT"`))
	assert.Contains(t, section, `placeholder="or a new series"`)
	section = invoiceSection(t, campaignsRequest(panel, http.MethodGet, page, nil, scopeOrderRead, scopeOrderWrite).Body.String())
	assert.NotContains(t, section, `name="series"`, "a writer who may not read the invoices is offered no series")
	assert.Contains(t, section, `placeholder="series, e.g. GBT"`)

	rec = campaignsRequest(panel, http.MethodPost, page+"/invoice", url.Values{
		formInvoiceSeries: {"IAD"}, formInvoiceName: {" Ada Ltd "}, formInvoiceAddress: {" 12 Main St "},
		formInvoiceCountry: {" tr "}, formInvoiceTaxNumber: {" 1234567890 "}, formInvoiceTaxOffice: {" Kadikoy "},
	}, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "Invoice GBT2026000000007 was issued.")
	rec = campaignsRequest(panel, http.MethodPost, page+"/invoice", url.Values{
		formInvoiceSeries: {"IAD"}, formInvoiceNewSeries: {" efa "},
	}, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{
		`order_1|IAD|{"name":"Ada Ltd","address":"12 Main St","country_code":"TR","tax_number":"1234567890","tax_office":"Kadikoy"}`,
		`order_1|EFA|{}`,
	}, invoicer.issued, "the series chosen, or a new one named, upper-cased; the buyer as typed, what is left empty left out")

	invoicer.already = true
	rec = campaignsRequest(panel, http.MethodPost, page+"/invoice", url.Values{formInvoiceSeries: {"GBT"}}, writer...)
	assert.Contains(t, rec.Body.String(), "This order already had invoice GBT2026000000007; nothing new was issued.")

	rec = campaignsRequest(panel, http.MethodPost, page+"/invoice", url.Values{}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "Choose a series, or name a new one; nothing was issued.")
	assert.Len(t, invoicer.issued, 3, "no series, nothing sent")

	invoicer.issueErr = errors.Invalid("invoice_invalid_input", "the series prefix has to be 3 letters")
	rec = campaignsRequest(panel, http.MethodPost, page+"/invoice", url.Values{formInvoiceNewSeries: {"GB"}}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "the series prefix has to be 3 letters")
	invoicer.issueErr = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, page+"/invoice", url.Values{formInvoiceSeries: {"GBT"}}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestAnIssuedInvoiceIsNamedAndNotOfferedAgain: an order with an invoice is
// named with its number and status and offered no form, and its page is
// linked for an operator who may read the invoices; an invoice that
// cannot be read is said so; a surface that cannot invoice draws no section
// and answers 503.
func TestAnIssuedInvoiceIsNamedAndNotOfferedAgain(t *testing.T) {
	t.Parallel()

	page := OrdersPath + "/order_1"
	writer := []string{scopeOrderRead, scopeOrderWrite, scopeInvoiceRead}
	issued := &fakeInvoicer{found: true}
	section := invoiceSection(t, campaignsRequest(invoicePanel(t, issued, &fakeSeries{body: `[]`}), http.MethodGet,
		page, nil, writer...).Body.String())
	assert.Contains(t, section, `Invoice <strong>GBT2026000000007</strong> <span class="pill">issued</span>`)
	assert.NotContains(t, section, "<form", "an invoiced order is offered no second one")
	assert.Contains(t, section, `<a href="`+InvoicesPath+`/inv_1">Open the invoice</a>`, "ADR 0344")
	section = invoiceSection(t, campaignsRequest(invoicePanel(t, issued, &fakeSeries{body: `[]`}), http.MethodGet,
		page, nil, scopeOrderRead).Body.String())
	assert.NotContains(t, section, "Open the invoice", "an operator who may not read the invoices is not sent to one")

	failing := &fakeInvoicer{readErr: errors.Unavailable("db_down", "no answer")}
	section = invoiceSection(t, campaignsRequest(invoicePanel(t, failing, nil), http.MethodGet, page, nil, writer...).Body.String())
	assert.Contains(t, section, "The invoice of this order could not be read.")
	assert.NotContains(t, section, "<form")

	bare := parcelsPanel(t, &fakeParcelOpener{}, nil)
	body := campaignsRequest(bare, http.MethodGet, page, nil, writer...).Body.String()
	assert.NotContains(t, body, "<h2>Invoice</h2>", "a surface that cannot invoice draws no section")
	rec := campaignsRequest(bare, http.MethodPost, page+"/invoice", url.Values{formInvoiceSeries: {"GBT"}}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
