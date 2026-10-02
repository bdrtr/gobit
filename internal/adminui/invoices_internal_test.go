package adminui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// fakeInvoices lists the series as fakeSeries does and the documents as
// scripted, recording what each listing asked for.
type fakeInvoices struct {
	fakeSeries
	listing string
	total   int64
	err     error
	asked   []string
}

func (f *fakeInvoices) InvoicesJSON(_ context.Context, status string, limit, offset int32) (json.RawMessage, int64, error) {
	f.asked = append(f.asked, fmt.Sprintf("%s|%d|%d", status, limit, offset))
	return json.RawMessage(f.listing), f.total, f.err
}

// invoicesPanel is a panel whose invoice surface is the one given, over a
// shop whose lira has two decimals.
func invoicesPanel(t *testing.T, invoices InvoiceSeriesLister) *UI {
	t.Helper()

	panel := newCatalogPanel(t, &fakeCatalog{byEntity: map[string][]query.Record{
		EntityRegion: {currencyRecord("TRY", 2)},
	}})
	panel.invoices = invoices
	panel.scopes = builtInScopes()

	return panel
}

// TestTheInvoicesScreenListsTheDocuments is ADR 0343: every status is listed
// when none is chosen, a tab lists its own a page at a time, each document
// with its number, kind, buyer, total in its currency's decimals, status and
// why, and the moment it was issued; an unknown tab is every status; the
// screen is in the menu and asks to read the invoices, and a surface that
// cannot list answers that it cannot.
func TestTheInvoicesScreenListsTheDocuments(t *testing.T) {
	t.Parallel()

	invoices := &fakeInvoices{total: 27, listing: `[{"id":"inv_1","number":"GBT2026000000004","kind":"sale",
		"status":"rejected","status_reason":"wrong tax number","buyer_name":"Ada Lovelace","currency_code":"TRY",
		"total":123456,"issued_at":"2026-10-02T09:30:00Z"},{"id":"inv_2","number":"GBT2026000000003",
		"kind":"refund","status":"issued","status_reason":"","buyer_name":"Charles Babbage","currency_code":"XAU",
		"total":7,"issued_at":"2026-10-01T08:00:00Z"}]`}
	panel := invoicesPanel(t, invoices)

	rec := campaignsRequest(panel, http.MethodGet, InvoicesPath, nil, scopeInvoiceRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	for _, want := range []string{
		"GBT2026000000004", "sale", "Ada Lovelace", "<td>1234.56 TRY</td>", "rejected", "wrong tax number",
		"2026-10-02 09:30", "GBT2026000000003", "refund", "Charles Babbage",
		`<td>7 XAU <span class="muted">(minor units)</span></td>`, "2026-10-01 08:00",
		"27 in all.", `href="` + InvoicesPath + `?status=" aria-current="page">all</a>`,
		`href="` + InvoicesPath + `?status=canceled">canceled</a>`,
		`href="` + InvoicesPath + `?status=&amp;page=2">Next</a>`,
	} {
		assert.Contains(t, body, want)
	}
	assert.Equal(t, []string{"|25|0"}, invoices.asked, "every status, the first page")
	assert.Contains(t, body, `href="`+InvoicesPath+`"`, "the screen is in the menu")

	invoices.asked = nil
	rec = campaignsRequest(panel, http.MethodGet, InvoicesPath+"?status=sent&page=2", nil, scopeInvoiceRead)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"sent|25|25"}, invoices.asked)
	assert.Contains(t, rec.Body.String(), `?status=sent" aria-current="page">sent</a>`)
	assert.Contains(t, rec.Body.String(), "27 sent.")
	assert.Contains(t, rec.Body.String(), `?status=sent&amp;page=1">Previous</a>`)
	assert.NotContains(t, rec.Body.String(), ">Next</a>", "the second page of 27 is the last")

	invoices.asked = nil
	campaignsRequest(panel, http.MethodGet, InvoicesPath+"?status=paid", nil, scopeInvoiceRead)
	assert.Equal(t, []string{"|25|0"}, invoices.asked, "an unknown tab is every status")

	rec = campaignsRequest(panel, http.MethodGet, InvoicesPath, nil, scopeOrderRead)
	assert.Equal(t, http.StatusForbidden, rec.Code, "an operator who may not read the invoices")

	rec = campaignsRequest(invoicesPanel(t, &fakeSeries{body: `[]`}), http.MethodGet, InvoicesPath, nil, scopeInvoiceRead)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "a surface that cannot list")
	invoices.err = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodGet, InvoicesPath, nil, scopeInvoiceRead)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
