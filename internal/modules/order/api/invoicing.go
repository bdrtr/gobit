package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// Invoicing is the part of the invoicing flow this module's endpoints call.
//
// It is declared HERE, on the consumer's side, and carries only primitives and
// JSON: the flow lives in internal/workflows and this module cannot import it
// (ADR 0006 holds in both directions).
//
// # Why the endpoints are on the ORDER and not on the invoice
//
// "Invoice this order" is a question asked about an order, and the client
// asking it is holding an order id. The invoice module's own endpoint takes a
// finished document and knows nothing about orders; putting an order id into it
// would give that module a fact about another module for the sake of a URL.
type Invoicing interface {
	// IssueForOrder issues the document for the order, or returns the one the
	// order already has.
	//
	// alreadyIssued being true means nothing new was created. It is reported
	// rather than inferred, because an operator who pressed the button twice
	// has to be told the second press did nothing — a number is spent for good
	// once it is taken.
	IssueForOrder(ctx context.Context, orderID string, request json.RawMessage) (
		invoiceID, number string, alreadyIssued bool, err error,
	)

	// InvoiceOfOrder returns the identity of the document bound to the order.
	InvoiceOfOrder(ctx context.Context, orderID string) (
		invoiceID, number, status string, err error,
	)
}

// invoicingParty is one side of the document as the endpoint accepts it.
//
// It exists so the OpenAPI document can describe the body. The body itself is
// passed through to the flow as raw JSON — this module does not decode it — and
// the two shapes are kept in step by the description rather than by a decoder,
// which is written down because it is the unusual arrangement.
type invoicingParty struct {
	Name        string `json:"name"`
	TaxNumber   string `json:"tax_number"`
	TaxOffice   string `json:"tax_office"`
	Email       string `json:"email"`
	Address     string `json:"address"`
	CountryCode string `json:"country_code"`
}

// invoicingIssueRequest is the body of the issue endpoint.
type invoicingIssueRequest struct {
	// SeriesPrefix is the letters of the series to take the number from.
	SeriesPrefix string `json:"series_prefix"`
	// Seller is the shop, as it is to be printed.
	Seller invoicingParty `json:"seller"`
	// Buyer is the customer; an empty e-mail is filled in from the order.
	Buyer invoicingParty `json:"buyer"`
	// Metadata is free structured context for the document.
	Metadata map[string]any `json:"metadata"`
}

// invoiceIssuedDTO is what the issue endpoint answers with.
//
// It is deliberately NOT the whole document: the caller asked to invoice an
// order and what it needs back is the identity of what happened. The document
// itself is one GET away and is the same body either way.
type invoiceIssuedDTO struct {
	// InvoiceID and Number identify the document.
	InvoiceID string `json:"invoice_id"`
	Number    string `json:"number"`
	// AlreadyIssued reports that the order had a document and this call
	// returned it instead of issuing a second one.
	AlreadyIssued bool `json:"already_issued"`
}

// adminIssueInvoice issues the document for an order.
//
// POST /admin/v1/orders/{id}/invoice
//
// It answers 200 rather than 201 when the order already had a document, so the
// two outcomes are distinguishable by status code as well as by body: a client
// that retried after a timeout can tell whether its first attempt landed.
func (h *Handler) adminIssueInvoice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// The body is passed through as raw JSON rather than decoded here: it is
	// the flow's contract, and a copy of its shape in this package would be a
	// second definition to keep in step by hand.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
			"the request body could not be read"))

		return
	}
	if len(body) == 0 {
		// The flow needs a series prefix and two parties; an empty body cannot
		// carry them, and refusing here says so rather than letting the flow
		// report a missing prefix the caller never sent.
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
			"the request body cannot be empty; it carries the series prefix and the two parties"))

		return
	}

	flow, err := h.invoicingFlow()
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	invoiceID, number, already, err := flow.IssueForOrder(ctx, orderID(r), body)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	response := singleEnvelope{Data: invoiceIssuedDTO{
		InvoiceID:     invoiceID,
		Number:        number,
		AlreadyIssued: already,
	}}

	// The two codes are written at two call sites rather than through a
	// variable: the repository's error-path audit resolves a status only when it
	// is a constant at the call, and a status it cannot resolve is one nobody
	// can prove bypasses the core's error writer.
	if already {
		corehttp.WriteJSON(ctx, w, http.StatusOK, response)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusCreated, response)
}

// orderInvoiceDTO is what the read endpoint answers with.
//
// It is an IDENTITY and not the document: this endpoint answers a question
// about an ORDER — which document does it have — and the document itself is
// served by /admin/v1/invoices/{id}, where its shape lives. Copying that shape
// into this module would be a second definition of a legal document, kept in
// step by hand.
type orderInvoiceDTO struct {
	// InvoiceID and Number identify the document.
	InvoiceID string `json:"invoice_id"`
	Number    string `json:"number"`
	// Status is where the document stands.
	Status string `json:"status"`
}

// adminGetOrderInvoice returns the identity of the order's document.
//
// GET /admin/v1/orders/{id}/invoice
func (h *Handler) adminGetOrderInvoice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	flow, err := h.invoicingFlow()
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	invoiceID, number, status, err := flow.InvoiceOfOrder(ctx, orderID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: orderInvoiceDTO{
		InvoiceID: invoiceID,
		Number:    number,
		Status:    status,
	}})
}
