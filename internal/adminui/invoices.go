package adminui

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"slices"
	"time"
)

// The Invoices screen (ADR 0343): the invoice module's documents, the latest
// first, in one status or in all of them, read through the module's panel
// surface for an operator who may read the invoices.

// InvoicesPath lists the documents.
const InvoicesPath = URLPrefix + "/invoices"

// invoicesLabel is what the section is called on screen.
const invoicesLabel = "Invoices"

// paramInvoiceStatus is the screen's status tab.
const paramInvoiceStatus = "status"

// invoiceStatuses are the statuses the screen's tabs offer, in the order a
// document moves through them; the empty one, every status, comes first and
// is listed when none is chosen.
var invoiceStatuses = []string{"", "issued", "sent", "accepted", "rejected", "canceled"}

// invoicesPerPage is the list's page size, the other lists'.
const invoicesPerPage = 25

// InvoiceLister is the narrow surface the screen reads through: the invoice
// module's.
type InvoiceLister interface {
	// InvoicesJSON lists the documents in the status, or in every status when
	// it is empty, the latest first, a page at a time, with how many there
	// are.
	InvoicesJSON(ctx context.Context, status string, limit, offset int32) (json.RawMessage, int64, error)
}

// invoiceRow is one document as the surface sends it; the json tags are the
// contract with that surface, exercised end to end.
type invoiceRow struct {
	ID           string    `json:"id"`
	Number       string    `json:"number"`
	Kind         string    `json:"kind"`
	Status       string    `json:"status"`
	StatusReason string    `json:"status_reason"`
	BuyerName    string    `json:"buyer_name"`
	CurrencyCode string    `json:"currency_code"`
	Total        int64     `json:"total"`
	IssuedAt     time.Time `json:"issued_at"`
	// Amount is the total in its currency's decimals, and Minor says the
	// currency's scale is not known, so Amount is in minor units.
	Amount string `json:"-"`
	Minor  bool   `json:"-"`
}

// listInvoices renders the documents in the chosen status.
func (u *UI) listInvoices(w http.ResponseWriter, r *http.Request) {
	lister, ok := u.invoices.(InvoiceLister)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Invoices unavailable",
			"The invoice module's panel surface cannot list the invoices in this installation.")
		return
	}

	ctx := r.Context()
	status := r.URL.Query().Get(paramInvoiceStatus)
	if !slices.Contains(invoiceStatuses, status) {
		status = invoiceStatuses[0]
	}
	page := pageNumber(r.URL.Query().Get("page"))
	offset := (page - 1) * invoicesPerPage
	if offset > math.MaxInt32 {
		offset = math.MaxInt32
	}

	raw, total, err := lister.InvoicesJSON(ctx, status, invoicesPerPage, int32(offset))
	var rows []invoiceRow
	if err == nil {
		err = json.Unmarshal(raw, &rows)
	}
	if err != nil {
		u.unexpectedFailure(w, r, err, "The invoices could not be read")
		return
	}
	scales := u.currencyScales(ctx)
	for i := range rows {
		var exact bool
		rows[i].Amount, exact = formatAmount(rows[i].Total, rows[i].CurrencyCode, scales)
		rows[i].Minor = !exact
	}

	data := map[string]any{
		titleKey:    invoicesLabel,
		"Invoices":  rows,
		statusKey:   status,
		statusesKey: invoiceStatuses,
		totalKey:    total,
	}
	addPaging(data, page, int64(page*invoicesPerPage) < total, InvoicesPath)

	u.templates.render(w, r, http.StatusOK, "invoices.gohtml", data)
}
