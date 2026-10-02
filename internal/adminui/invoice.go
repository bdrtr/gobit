package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// An invoice's page (ADR 0344): the document as the invoice module issued
// it, its parties, rows and totals, and the moves its status may make,
// offered to an operator who may write the invoices from the status the page
// was drawn in.

// InvoicePath shows one document, and InvoiceStatusPath moves it.
const (
	InvoicePath       = InvoicesPath + "/{id}"
	InvoiceStatusPath = InvoicePath + "/status"
)

// scopeInvoiceWrite is the invoice module's write privilege, under which a
// document's status is moved.
const scopeInvoiceWrite = "invoice:write"

// The move form's fields: the status the page was drawn in, the one chosen
// and why.
const (
	formInvoiceReadStatus = "read_status"
	formInvoiceTo         = "to"
	formInvoiceReason     = "reason"
)

// paramMoved carries the status a document was moved to into the page the
// move returns to.
const paramMoved = "moved"

// InvoiceReader is the narrow surface a document is read through: the
// invoice module's.
type InvoiceReader interface {
	// InvoiceJSON returns the document with its rows and the statuses it may
	// move to.
	InvoiceJSON(ctx context.Context, id string) (json.RawMessage, error)
}

// InvoiceMover is the narrow surface a document's status is moved through.
type InvoiceMover interface {
	// MoveInvoice moves the document from the status read to the next one,
	// with why, and refuses when it moved since.
	MoveInvoice(ctx context.Context, id, readStatus, to, reason string) error
}

// invoiceParty is one side of the document as the surface sends it.
type invoiceParty struct {
	Name        string `json:"name"`
	TaxNumber   string `json:"tax_number"`
	TaxOffice   string `json:"tax_office"`
	Email       string `json:"email"`
	Address     string `json:"address"`
	CountryCode string `json:"country_code"`
}

// invoiceLine is one row of the document as the surface sends it, with its
// amounts as the page prints them.
type invoiceLine struct {
	Description   string `json:"description"`
	Quantity      int64  `json:"quantity"`
	UnitPrice     int64  `json:"unit_price"`
	Subtotal      int64  `json:"subtotal"`
	DiscountTotal int64  `json:"discount_total"`
	TaxRateBps    int32  `json:"tax_rate_bps"`
	TaxTotal      int64  `json:"tax_total"`
	Total         int64  `json:"total"`
	// The amounts in the document's currency's decimals, and the rate as a
	// percent.
	UnitPriceText, DiscountText, TaxText, TotalText, Rate string `json:"-"`
}

// invoiceDocument is the document as the surface sends it; the json tags are
// the contract with that surface, exercised end to end.
type invoiceDocument struct {
	invoiceRow
	Seller           invoiceParty  `json:"seller"`
	Buyer            invoiceParty  `json:"buyer"`
	Subtotal         int64         `json:"subtotal"`
	DiscountTotal    int64         `json:"discount_total"`
	TaxTotal         int64         `json:"tax_total"`
	PricesIncludeTax bool          `json:"prices_include_tax"`
	ProviderID       string        `json:"provider_id"`
	ExternalID       string        `json:"external_id"`
	Lines            []invoiceLine `json:"lines"`
	Moves            []string      `json:"moves"`
	// The totals in the document's currency's decimals.
	SubtotalText, DiscountText, TaxText string `json:"-"`
}

// showInvoice renders the document in the path.
func (u *UI) showInvoice(w http.ResponseWriter, r *http.Request) {
	u.renderInvoice(w, r, http.StatusOK, chi.URLParam(r, "id"), "", nil)
}

// moveInvoice moves the document in the path from the status the page was
// drawn in to the one chosen, with why, and returns to the page, which says
// so; a refusal, a document moved since included, comes back on the page
// with what was chosen and typed (ADR 0344).
func (u *UI) moveInvoice(w http.ResponseWriter, r *http.Request) {
	mover, ok := u.invoices.(InvoiceMover)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Invoices unavailable",
			"The invoice module's panel surface cannot move an invoice in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	id, to := chi.URLParam(r, "id"), r.PostFormValue(formInvoiceTo)
	err := mover.MoveInvoice(r.Context(), id, r.PostFormValue(formInvoiceReadStatus), to,
		strings.TrimSpace(r.PostFormValue(formInvoiceReason)))
	switch {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w, InvoicesPath+"/"+url.PathEscape(id)+"?"+url.Values{paramMoved: {to}}.Encode())
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderInvoice(w, r, http.StatusUnprocessableEntity, id, messageFor(err), r.PostForm)
	default:
		u.unexpectedFailure(w, r, err, "The invoice could not be moved")
	}
}

// renderInvoice draws the document with a refused move's reason and what
// was typed in it. An operator who may move and not read is told the reason
// alone (ADR 0260).
func (u *UI) renderInvoice(w http.ResponseWriter, r *http.Request, code int, id, refused string, typed url.Values) {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	if refused != "" && !principal.HasScope(scopeInvoiceRead) {
		u.errorPage(w, r, code, "Not done", refused)
		return
	}
	reader, ok := u.invoices.(InvoiceReader)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Invoices unavailable",
			"The invoice module's panel surface cannot show an invoice in this installation.")
		return
	}

	ctx := r.Context()
	raw, err := reader.InvoiceJSON(ctx, id)
	var document invoiceDocument
	if err == nil {
		err = json.Unmarshal(raw, &document)
	}
	switch {
	case errors.IsNotFound(err):
		u.errorPage(w, r, http.StatusNotFound, "Not found", "There is no invoice "+id+".")
		return
	case err != nil:
		u.unexpectedFailure(w, r, err, "The invoice could not be read")
		return
	}

	scales := u.currencyScales(ctx)
	money := func(minor int64) string {
		text, exact := formatAmount(minor, document.CurrencyCode, scales)
		if !exact {
			return text + " (minor units)"
		}

		return text
	}
	document.Amount = money(document.Total)
	document.SubtotalText, document.DiscountText, document.TaxText =
		money(document.Subtotal), money(document.DiscountTotal), money(document.TaxTotal)
	for i := range document.Lines {
		line := &document.Lines[i]
		line.UnitPriceText, line.DiscountText, line.TaxText, line.TotalText =
			money(line.UnitPrice), money(line.DiscountTotal), money(line.TaxTotal), money(line.Total)
		line.Rate = percentText(int64(line.TaxRateBps))
	}

	_, canMove := u.invoices.(InvoiceMover)
	chosen, reason := "", ""
	if typed != nil {
		chosen, reason = typed.Get(formInvoiceTo), typed.Get(formInvoiceReason)
	}
	data := map[string]any{
		titleKey:   "Invoice " + document.Number,
		"Invoice":  document,
		"CanMove":  canMove && len(document.Moves) > 0 && principal.HasScope(scopeInvoiceWrite),
		"Chosen":   chosen,
		"Reason":   reason,
		"Moved":    r.URL.Query().Get(paramMoved),
		refusedKey: refused,
		pathKey:    InvoicesPath,
	}

	u.templates.render(w, r, code, "invoice.gohtml", data)
}
