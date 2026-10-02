package adminui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// fakeInvoiceDocuments lists the documents as fakeInvoices does, reads one
// as scripted and moves it, recording each move.
type fakeInvoiceDocuments struct {
	fakeInvoices
	document string
	readErr  error
	moved    []string
	moveErr  error
}

func (f *fakeInvoiceDocuments) InvoiceJSON(_ context.Context, id string) (json.RawMessage, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return json.RawMessage(strings.ReplaceAll(f.document, "{id}", id)), nil
}

func (f *fakeInvoiceDocuments) MoveInvoice(_ context.Context, id, readStatus, to, reason string) error {
	f.moved = append(f.moved, fmt.Sprintf("%s|%s|%s|%s", id, readStatus, to, reason))
	return f.moveErr
}

// sentDocument is a sent invoice of one row, in lira.
const sentDocument = `{"id":"{id}","number":"GBT2026000000004","kind":"sale","status":"sent","status_reason":"",
	"buyer_name":"Ada Lovelace","currency_code":"TRY","total":240000,"issued_at":"2026-10-02T09:30:00Z",
	"seller":{"name":"Gobit Shop","tax_number":"1234567890","tax_office":"Kadikoy","email":"","address":"1 Shop St",
	"country_code":"TR"},"buyer":{"name":"Ada Lovelace","tax_number":"12345678901","tax_office":"",
	"email":"ada@example.test","address":"12 Main St","country_code":"TR"},"subtotal":200000,"discount_total":1050,
	"tax_total":40000,"prices_include_tax":false,"provider_id":"","external_id":"EXT-9","lines":[{"description":
	"Red T-Shirt","quantity":2,"unit_price":100000,"subtotal":200000,"discount_total":1050,"tax_rate_bps":1850,
	"tax_total":40000,"total":240000}],"moves":["accepted","rejected","canceled"]}`

// TestAnInvoiceIsShownAndMovedOnItsPage is ADR 0344: a reader is shown the
// document's parties, rows and totals in its currency's decimals, the rate
// as a percent; a writer is offered the moves its status may make, carrying
// the status the page was drawn in, and the surface is asked to move it
// with why, trimmed, and the page says so; a refusal comes back with what
// was chosen and typed; a document with no move left offers no form; and
// the Invoices screen links each number to its page.
func TestAnInvoiceIsShownAndMovedOnItsPage(t *testing.T) {
	t.Parallel()

	documents := &fakeInvoiceDocuments{document: sentDocument}
	documents.listing = `[{"id":"inv_4","number":"GBT2026000000004","status":"sent","currency_code":"TRY"}]`
	panel := invoicesPanel(t, documents)
	page := InvoicesPath + "/inv_4"
	writer := []string{scopeInvoiceRead, scopeInvoiceWrite}

	rec := campaignsRequest(panel, http.MethodGet, page, nil, scopeInvoiceRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	for _, want := range []string{
		"Invoice GBT2026000000004", `<span class="pill">sent</span>`, "EXT-9", "Gobit Shop", "1234567890", "Kadikoy",
		"Ada Lovelace", "12345678901", "ada@example.test", "12 Main St", "<td>Red T-Shirt</td>", "<td>2</td>",
		"<td>1000.00</td>", "<td>10.50</td>", "<td>18.5%</td>", "<td>400.00</td>", "<td>2400.00</td>",
		"Subtotal 2000.00", "total 2400.00 TRY",
	} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, body, "/status", "a reader moves nothing")

	rec = campaignsRequest(panel, http.MethodGet, page, nil, writer...)
	form := rec.Body.String()
	_, form, found := strings.Cut(form, `action="`+page+`/status"`)
	require.True(t, found, "a writer is offered the moves")
	form, _, _ = strings.Cut(form, "</form>")
	assert.Contains(t, form, `name="read_status" value="sent"`)
	assert.Contains(t, form, `<option value="accepted">accepted</option><option value="rejected">rejected</option>`+
		`<option value="canceled">canceled</option>`)

	rec = campaignsRequest(panel, http.MethodPost, page+"/status", url.Values{
		formInvoiceReadStatus: {"sent"}, formInvoiceTo: {"rejected"}, formInvoiceReason: {" wrong tax number "},
	}, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, page+"?moved=rejected", rec.Header().Get("Location"))
	assert.Equal(t, []string{"inv_4|sent|rejected|wrong tax number"}, documents.moved)
	landed := campaignsRequest(panel, http.MethodGet, rec.Header().Get("Location"), nil, scopeInvoiceRead)
	assert.Contains(t, landed.Body.String(), "The invoice was moved to rejected.")

	documents.moveErr = errors.Conflict("invoice_status_moved",
		`invoice inv_4 is "accepted" now, not "sent" as it was read; draw the page again`)
	rec = campaignsRequest(panel, http.MethodPost, page+"/status", url.Values{
		formInvoiceReadStatus: {"sent"}, formInvoiceTo: {"canceled"}, formInvoiceReason: {"a duplicate"},
	}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body = rec.Body.String()
	assert.Contains(t, body, "draw the page again")
	assert.Contains(t, body, `<option value="canceled" selected>canceled</option>`, "what was chosen")
	assert.Contains(t, body, `name="reason" value="a duplicate"`, "and typed")
	rec = campaignsRequest(panel, http.MethodPost, page+"/status", url.Values{
		formInvoiceTo: {"issued"}, formInvoiceReason: {"x"},
	}, writer...)
	assert.NotContains(t, rec.Body.String(), " selected>", "a status it may not move to is not chosen")
	rec = campaignsRequest(panel, http.MethodPost, page+"/status", url.Values{formInvoiceTo: {"sent"}}, scopeInvoiceWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.NotContains(t, rec.Body.String(), "Ada Lovelace", "a writer who cannot read is shown none of the document")
	documents.moveErr = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, page+"/status", url.Values{formInvoiceTo: {"sent"}}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	documents.document = strings.Replace(sentDocument, `"moves":["accepted","rejected","canceled"]`, `"moves":[]`, 1)
	rec = campaignsRequest(panel, http.MethodGet, page, nil, writer...)
	assert.NotContains(t, rec.Body.String(), "/status", "a document with no move left offers no form")

	rec = campaignsRequest(panel, http.MethodGet, InvoicesPath, nil, scopeInvoiceRead)
	assert.Contains(t, rec.Body.String(), `<a href="`+InvoicesPath+`/inv_4">GBT2026000000004</a>`)

	documents.readErr = errors.NotFound("invoice_not_found", "invoice inv_9 not found")
	rec = campaignsRequest(panel, http.MethodGet, InvoicesPath+"/inv_9", nil, scopeInvoiceRead)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "There is no invoice inv_9.", "said, not logged as a failure")
	documents.readErr = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopeInvoiceRead)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	lister := invoicesPanel(t, &fakeInvoices{})
	assert.Equal(t, http.StatusServiceUnavailable,
		campaignsRequest(lister, http.MethodGet, page, nil, scopeInvoiceRead).Code, "a surface that cannot read one")
	assert.Equal(t, http.StatusServiceUnavailable,
		campaignsRequest(lister, http.MethodPost, page+"/status", url.Values{formInvoiceTo: {"sent"}}, writer...).Code,
		"nor move one")
}
