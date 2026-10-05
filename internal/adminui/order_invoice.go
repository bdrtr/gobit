package adminui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// An order's invoice on its page (ADR 0335): the order module names the
// document issued for the order and issues one through the invoicing flow
// under order:write, on a series the invoice module lists for an operator who
// may read the invoices.

// ServiceInvoiceAdmin is the invoice module's panel surface, spelled by hand
// and pinned against the module's constant in internal/arch.
const ServiceInvoiceAdmin = "invoice.admin"

// OrderInvoicePath issues the order's invoice.
const OrderInvoicePath = OrderPath + "/invoice"

// OrderAmendmentsPath documents an act after the order's sale on a document
// amending its invoice (ADR 0406).
const OrderAmendmentsPath = OrderInvoicePath + "/amendments"

// scopeInvoiceRead is the invoice module's read privilege, under which the
// order page offers the series an invoice is numbered on.
const scopeInvoiceRead = "invoice:read"

// The invoice form's fields: a series the shop has, or a new one, and the
// buyer as printed, each left empty taken from the order's billing address,
// and the tax identity the order does not know.
const (
	formInvoiceSeries    = "series"
	formInvoiceNewSeries = "new_series"
	formInvoiceName      = "buyer_name"
	formInvoiceAddress   = "buyer_address"
	formInvoiceCountry   = "buyer_country"
	formInvoiceTaxNumber = "tax_number"
	formInvoiceTaxOffice = "tax_office"
	// formAmendActKind and formAmendActID name the act an amendment
	// documents, as the order journal names it.
	formAmendActKind = "act_kind"
	formAmendActID   = "act_id"
)

// invoiceBuyer is the buyer the form sends, as the invoicing flow's party
// spells it; the json tags are the contract with the order module's surface,
// which hands it on, exercised end to end. An empty field is left out, so the
// flow takes it from the order.
type invoiceBuyer struct {
	Name        string `json:"name,omitempty"`
	Address     string `json:"address,omitempty"`
	CountryCode string `json:"country_code,omitempty"`
	TaxNumber   string `json:"tax_number,omitempty"`
	TaxOffice   string `json:"tax_office,omitempty"`
}

// OrderInvoicer is the narrow surface an order's invoice is read and issued
// through: the order module's, over the invoicing flow.
type OrderInvoicer interface {
	// InvoiceOfOrder names the order's document, and reports when there is
	// none.
	InvoiceOfOrder(ctx context.Context, orderID string) (invoiceID, number, status string, found bool, err error)
	// IssueInvoice issues the order's document on the series the prefix
	// names, to the buyer as given, or returns the one it has, reporting
	// which.
	IssueInvoice(
		ctx context.Context, orderID, seriesPrefix string, buyer json.RawMessage,
	) (invoiceID, number string, alreadyIssued bool, err error)
}

// OrderAmender is the narrow surface an order's acts after its sale are read
// and documented through: the order module's, over the invoicing flow
// (ADR 0406).
type OrderAmender interface {
	// AmendmentsOfOrder lists the order's acts after its sale with the live
	// document of each.
	AmendmentsOfOrder(ctx context.Context, orderID string) (json.RawMessage, error)
	// IssueAmendment documents one act on the series the prefix names, or
	// returns the document it has, reporting which.
	IssueAmendment(
		ctx context.Context, orderID, seriesPrefix, kind, id string,
	) (invoiceID, number string, alreadyIssued bool, err error)
}

// orderAct is one act after the order's sale as the surface sends it; the
// json tags are the contract with the flow, exercised end to end.
type orderAct struct {
	Kind         string `json:"kind"`
	ID           string `json:"id"`
	Amount       int64  `json:"amount"`
	Documentable bool   `json:"documentable"`
	Document     *struct {
		InvoiceID string `json:"invoice_id"`
		Number    string `json:"number"`
		Status    string `json:"status"`
	} `json:"document"`
	// Printed is the amount with its currency.
	Printed string `json:"-"`
	// Open is the document's page, for an operator who may read the invoices.
	Open string `json:"-"`
}

// InvoiceSeriesLister is the narrow surface the numbering series are read
// through: the invoice module's.
type InvoiceSeriesLister interface {
	// SeriesJSON lists the series, the latest year first.
	SeriesJSON(ctx context.Context) (json.RawMessage, error)
}

// invoiceSeries is one series as the surface sends it; the json tags are the
// contract with that surface, exercised end to end.
type invoiceSeries struct {
	Prefix     string `json:"prefix"`
	Year       int32  `json:"year"`
	LastNumber int64  `json:"last_number"`
}

// orderInvoice is what the order page says of its invoice.
type orderInvoice struct {
	// Unread says the order's document could not be read.
	Unread bool
	// Found says the order has a document, which the rest names.
	Found  bool
	ID     string
	Number string
	Status string
	// Open is the document's page, for an operator who may read the
	// invoices (ADR 0344).
	Open string
	// CanIssue says the operator may issue one and the order has none.
	CanIssue bool
	// Acts are the order's acts after its sale with their documents, once
	// the order has an invoice (ADR 0406); ActsUnread says they could not be
	// read, and CanAmend that the operator may document one.
	Acts       []orderAct
	ActsUnread bool
	CanAmend   bool
	// Prefixes are the series the form offers, the latest year's first; empty
	// for an operator who may not read the invoices, or a shop with none.
	Prefixes []string
}

// invoiceOf is what the order page says of the order's invoice, nil when the
// order module's surface cannot say.
func (u *UI) invoiceOf(r *http.Request, orderID, currency string, scales map[string]int) *orderInvoice {
	invoicer, ok := u.afterSales.(OrderInvoicer)
	if !ok {
		return nil
	}
	ctx := r.Context()
	invoice := &orderInvoice{}
	var err error
	invoice.ID, invoice.Number, invoice.Status, invoice.Found, err = invoicer.InvoiceOfOrder(ctx, orderID)
	if err != nil {
		corehttp.LoggerFromContext(ctx).WarnContext(ctx,
			"the panel could not read the order's invoice", "error", err, "order_id", orderID)
		return &orderInvoice{Unread: true}
	}
	principal, _ := corehttp.PrincipalFromContext(ctx)
	if invoice.Found && principal.HasScope(scopeInvoiceRead) {
		invoice.Open = InvoicesPath + "/" + url.PathEscape(invoice.ID)
	}
	invoice.CanIssue = !invoice.Found && principal.HasScope(scopeOrderWrite)
	if amender, ok := u.afterSales.(OrderAmender); ok && invoice.Found {
		invoice.Acts, invoice.ActsUnread = u.actsOf(r, amender, orderID, currency, scales)
		invoice.CanAmend = !invoice.ActsUnread && principal.HasScope(scopeOrderWrite)
	}
	if (invoice.CanIssue || invoice.CanAmend) && u.invoices != nil && principal.HasScope(scopeInvoiceRead) {
		invoice.Prefixes = u.seriesPrefixes(ctx)
	}

	return invoice
}

// actsOf reads the order's acts after its sale with their documents; unread
// says the read failed, which leaves the invoice on the page.
func (u *UI) actsOf(
	r *http.Request, amender OrderAmender, orderID, currency string, scales map[string]int,
) (acts []orderAct, unread bool) {
	ctx := r.Context()
	raw, err := amender.AmendmentsOfOrder(ctx, orderID)
	if err == nil {
		err = json.Unmarshal(raw, &acts)
	}
	if err != nil {
		corehttp.LoggerFromContext(ctx).WarnContext(ctx,
			"the panel could not read the order's acts after its sale", "error", err, "order_id", orderID)
		return nil, true
	}
	principal, _ := corehttp.PrincipalFromContext(ctx)
	for i := range acts {
		text, known := formatAmount(acts[i].Amount, currency, scales)
		acts[i].Printed = withCurrency(text, currency, known)
		if acts[i].Document != nil && principal.HasScope(scopeInvoiceRead) {
			acts[i].Open = InvoicesPath + "/" + url.PathEscape(acts[i].Document.InvoiceID)
		}
	}

	return acts, false
}

// issueAmendment documents the act the form names on the series it chose, or
// a new one it named, and draws the order again saying what it did
// (ADR 0406).
func (u *UI) issueAmendment(w http.ResponseWriter, r *http.Request) {
	amender, ok := u.afterSales.(OrderAmender)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Invoices unavailable",
			"The order module's panel surface cannot document an act in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	orderID := chi.URLParam(r, "id")
	prefix := strings.ToUpper(strings.TrimSpace(r.PostFormValue(formInvoiceNewSeries)))
	if prefix == "" {
		prefix = strings.TrimSpace(r.PostFormValue(formInvoiceSeries))
	}
	kind, id := strings.TrimSpace(r.PostFormValue(formAmendActKind)), strings.TrimSpace(r.PostFormValue(formAmendActID))
	if prefix == "" || kind == "" || id == "" {
		u.renderOrder(w, r, http.StatusUnprocessableEntity, orderID,
			&afterSaleOutcome{Refused: "Choose a series, or name a new one, for the act; nothing was issued."})
		return
	}

	_, number, already, err := amender.IssueAmendment(r.Context(), orderID, prefix, kind, id)
	switch {
	case err == nil && already:
		u.renderOrder(w, r, http.StatusOK, orderID, &afterSaleOutcome{
			Done: fmt.Sprintf("This act already had document %s; nothing new was issued.", number)})
	case err == nil:
		u.renderOrder(w, r, http.StatusOK, orderID, &afterSaleOutcome{
			Done: fmt.Sprintf("Document %s was issued.", number)})
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderOrder(w, r, http.StatusUnprocessableEntity, orderID, &afterSaleOutcome{Refused: refusalOf(err)})
	default:
		u.unexpectedFailure(w, r, err, "The document could not be issued")
	}
}

// seriesPrefixes reads the series' prefixes once each, the latest year's
// first.
func (u *UI) seriesPrefixes(ctx context.Context) []string {
	raw, err := u.invoices.SeriesJSON(ctx)
	var series []invoiceSeries
	if err == nil {
		err = json.Unmarshal(raw, &series)
	}
	if err != nil {
		corehttp.LoggerFromContext(ctx).WarnContext(ctx, "the panel could not read the invoice series", "error", err)
		return nil
	}
	prefixes := make([]string, 0, len(series))
	for _, s := range series {
		if !slices.Contains(prefixes, s.Prefix) {
			prefixes = append(prefixes, s.Prefix)
		}
	}

	return prefixes
}

// issueInvoice issues the order's invoice on the series the form chose, or a
// new one it named, and draws the order again saying what it did (ADR 0335).
func (u *UI) issueInvoice(w http.ResponseWriter, r *http.Request) {
	invoicer, ok := u.afterSales.(OrderInvoicer)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Invoices unavailable",
			"The order module's panel surface cannot issue an invoice in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	orderID := chi.URLParam(r, "id")
	prefix := strings.ToUpper(strings.TrimSpace(r.PostFormValue(formInvoiceNewSeries)))
	if prefix == "" {
		prefix = strings.TrimSpace(r.PostFormValue(formInvoiceSeries))
	}
	if prefix == "" {
		u.renderOrder(w, r, http.StatusUnprocessableEntity, orderID,
			&afterSaleOutcome{Refused: "Choose a series, or name a new one; nothing was issued."})
		return
	}

	buyer, err := json.Marshal(invoiceBuyer{
		Name:        strings.TrimSpace(r.PostFormValue(formInvoiceName)),
		Address:     strings.TrimSpace(r.PostFormValue(formInvoiceAddress)),
		CountryCode: strings.ToUpper(strings.TrimSpace(r.PostFormValue(formInvoiceCountry))),
		TaxNumber:   strings.TrimSpace(r.PostFormValue(formInvoiceTaxNumber)),
		TaxOffice:   strings.TrimSpace(r.PostFormValue(formInvoiceTaxOffice)),
	})
	if err != nil {
		u.unexpectedFailure(w, r, err, "The invoice could not be issued")
		return
	}
	_, number, already, err := invoicer.IssueInvoice(r.Context(), orderID, prefix, buyer)
	switch {
	case err == nil && already:
		u.renderOrder(w, r, http.StatusOK, orderID, &afterSaleOutcome{
			Done: fmt.Sprintf("This order already had invoice %s; nothing new was issued.", number)})
	case err == nil:
		u.renderOrder(w, r, http.StatusOK, orderID, &afterSaleOutcome{
			Done: fmt.Sprintf("Invoice %s was issued.", number)})
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderOrder(w, r, http.StatusUnprocessableEntity, orderID, &afterSaleOutcome{Refused: refusalOf(err)})
	default:
		u.unexpectedFailure(w, r, err, "The invoice could not be issued")
	}
}
