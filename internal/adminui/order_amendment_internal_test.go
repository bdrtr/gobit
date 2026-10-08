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

// fakeAmender names the order's invoice and lists and documents its acts after
// the sale as scripted, recording each document.
type fakeAmender struct {
	fakeInvoicer
	acts     string
	actsErr  error
	amended  []string
	amendErr error
}

func (f *fakeAmender) AmendmentsOfOrder(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(f.acts), f.actsErr
}

func (f *fakeAmender) IssueAmendment(
	_ context.Context, orderID, prefix, kind, id string,
) (invoiceID, number string, alreadyIssued bool, err error) {
	f.amended = append(f.amended, orderID+"|"+prefix+"|"+kind+"|"+id)
	return "inv_2", "GBT2026000000008", f.already, f.amendErr
}

// TestAnOrdersActsAfterTheSaleAreDocumentedOnItsPage is ADR 0406 on the
// panel: an invoiced order lists what moved after its sale, each act with its
// documents, none for an exchange's money, and a writer documents the rest on
// a series of the shop's; an exchange that names its return shows both of its
// documents, and one documented half shows what stands and offers the rest
// (ADR 0432).
func TestAnOrdersActsAfterTheSaleAreDocumentedOnItsPage(t *testing.T) {
	t.Parallel()

	page := OrdersPath + "/order_1"
	writer := []string{scopeOrderRead, scopeOrderWrite, scopeInvoiceRead}
	amender := &fakeAmender{fakeInvoicer: fakeInvoicer{found: true}, acts: `[
		{"kind":"credit_line","id":"ocl_1","amount":1500,"documentable":true,
		 "document":{"invoice_id":"inv_2","number":"GBT2026000000008","kind":"refund","status":"issued"},
		 "documents":[{"invoice_id":"inv_2","number":"GBT2026000000008","kind":"refund","status":"issued"}]},
		{"kind":"return_refunded","id":"ref_1","amount":2400,"documentable":true,"document":null,"documents":[]},
		{"kind":"exchange_funded","id":"exc_1","amount":700,"documentable":false,"document":null,"documents":[]},
		{"kind":"exchange","id":"exc_2","amount":61610,"documentable":true,
		 "document":{"invoice_id":"inv_5","number":"GBT2026000000010","kind":"sale","status":"issued"},
		 "documents":[{"invoice_id":"inv_4","number":"GBT2026000000009","kind":"refund","status":"issued"},
		  {"invoice_id":"inv_5","number":"GBT2026000000010","kind":"sale","status":"issued"}]},
		{"kind":"exchange","id":"exc_3","amount":5400,"documentable":true,"document":null,
		 "documents":[{"invoice_id":"inv_6","number":"GBT2026000000011","kind":"refund","status":"issued"}]},
		{"kind":"exchange","id":"exc_4","amount":900,"documentable":false,"document":null,"documents":[]},
		{"kind":"exchange","id":"exc_5","amount":800,"documentable":false,"withdrawn":true,
		 "document":{"invoice_id":"inv_8","number":"GBT2026000000013","kind":"sale","status":"issued"},
		 "documents":[{"invoice_id":"inv_7","number":"GBT2026000000012","kind":"refund","status":"issued"},
		  {"invoice_id":"inv_8","number":"GBT2026000000013","kind":"sale","status":"issued"}]}]`}
	panel := newCatalogPanel(t, linkedOrderCatalog())
	panel.afterSales = amender
	panel.invoices = &fakeSeries{body: `[{"prefix":"GBT","year":2026,"last_number":9}]`}
	panel.scopes = builtInScopes()

	section := invoiceSection(t, campaignsRequest(panel, http.MethodGet, page, nil, writer...).Body.String())
	assert.Contains(t, section, `<strong>GBT2026000000008</strong> <span class="pill">issued</span>`)
	assert.Contains(t, section, `<a href="`+InvoicesPath+`/inv_2">Open</a>`)
	assert.Contains(t, section, "No document carries it.", "an exchange's money is on no document of its own")
	for _, document := range []string{"inv_4", "inv_5", "inv_6"} {
		assert.Contains(t, section, `<a href="`+InvoicesPath+`/`+document+`">Open</a>`,
			"each of an exchange's documents is shown")
	}
	assert.Equal(t, 2, strings.Count(section, `action="`+page+`/invoice/amendments"`),
		"the act with no document and the exchange with one of two are offered the rest")
	assert.Contains(t, section, `name="act_id" value="ref_1"`)
	assert.Contains(t, section, `name="act_id" value="exc_3"`)
	assert.NotContains(t, section, `name="act_id" value="exc_2"`, "both of its documents stand")
	assert.Equal(t, 1, strings.Count(section, "No document carries it."), "only the exchange's money is on none")
	assert.Contains(t, section, "Documented once its return has come back and every replacement it sends has left.",
		"an exchange whose goods have not moved both ways says when it is documented")
	assert.Contains(t, section, "The rest can be issued.", "a half documented exchange says the rest can be")
	assert.Equal(t, 1, strings.Count(section, "Issue the rest"), "and offers it")
	assert.Contains(t, section, "Withdrawn: its sale names goods it no longer sends; cancel both documents, as "+
		"the return's own refund documents what came back.",
		"a withdrawn exchange's documents are shown to be voided")
	assert.NotContains(t, section, `name="act_id" value="exc_5"`)
	assert.Contains(t, section, `<option value="GBT">GBT</option>`)

	section = invoiceSection(t, campaignsRequest(panel, http.MethodGet, page, nil, scopeOrderRead).Body.String())
	assert.NotContains(t, section, "<form", "a reader documents nothing")
	assert.Contains(t, section, "Not documented.")

	rec := campaignsRequest(panel, http.MethodPost, page+"/invoice/amendments", url.Values{
		formInvoiceSeries: {"GBT"}, formAmendActKind: {"return_refunded"}, formAmendActID: {"ref_1"},
	}, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "Document GBT2026000000008 was issued.")
	assert.Equal(t, []string{"order_1|GBT|return_refunded|ref_1"}, amender.amended)

	rec = campaignsRequest(panel, http.MethodPost, page+"/invoice/amendments", url.Values{
		formAmendActKind: {"return_refunded"}, formAmendActID: {"ref_1"},
	}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Len(t, amender.amended, 1, "no series, nothing sent")

	amender.amendErr = errors.Conflict("invoicing_act_does_not_fit", "the rows have 100 left")
	rec = campaignsRequest(panel, http.MethodPost, page+"/invoice/amendments", url.Values{
		formInvoiceNewSeries: {"rfd"}, formAmendActKind: {"credit_line"}, formAmendActID: {"ocl_2"},
	}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "the rows have 100 left")
	assert.Equal(t, "order_1|RFD|credit_line|ocl_2", amender.amended[1])

	amender.actsErr = errors.Unavailable("db_down", "no answer")
	section = invoiceSection(t, campaignsRequest(panel, http.MethodGet, page, nil, writer...).Body.String())
	assert.Contains(t, section, "What moved after the sale could not be read.")
	assert.Contains(t, section, "GBT2026000000007", "the invoice itself is still named")
}
