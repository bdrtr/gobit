package invoicing

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
)

// InteropName is the name of the invoicing flow in the container (ADR 0006).
//
// The order module's API resolves it BY NAME at request time: the flow is born
// after every module has registered, while the handler is built during
// registration, and deferring the resolution is how that circle is broken.
const InteropName = "workflows.invoicing.interop"

// Interop is the flow's cross-module surface.
//
// It carries only PRIMITIVE and stdlib types, so a consumer can declare the
// interface on its own side without importing this package (ADR 0001/0006).
type Interop struct {
	w *Workflows
}

// NewInterop builds the surface over the given flow.
func NewInterop(w *Workflows) *Interop { return &Interop{w: w} }

// interopIssueRequest is the body [Interop.IssueForOrder] accepts.
//
// The buyer travels as JSON rather than as six string arguments: they are
// printed fields with fixed meanings, and a positional signature of six strings
// is one a caller gets wrong silently.
type interopIssueRequest struct {
	// SeriesPrefix is the letters of the series to take the number from.
	SeriesPrefix string `json:"series_prefix"`
	// Buyer is the customer as they are to be printed. The SELLER is not here:
	// it comes from the shop's own record (ADR 0115), so a caller cannot decide
	// who issued the document.
	Buyer Party `json:"buyer"`
	// Metadata is free structured context for the document.
	Metadata map[string]any `json:"metadata"`
}

// IssueForOrder issues the document for an order, or returns the one it has.
//
// alreadyIssued being true means the order HAD a document and this call
// returned it instead of issuing a second one. It crosses as its own value
// rather than being inferred, because the two outcomes are identical to a
// caller that only reads the number and an operator who pressed twice deserves
// to be told the second press did nothing.
func (i *Interop) IssueForOrder(
	ctx context.Context, orderID string, request json.RawMessage,
) (invoiceID, number string, alreadyIssued bool, err error) {
	var body interopIssueRequest
	if err := json.Unmarshal(request, &body); err != nil {
		return "", "", false, errors.Invalid(CodeInvalidInput,
			"the invoicing request could not be read: %v", err)
	}

	out, err := i.w.IssueForOrder(ctx, IssueInput{
		OrderID:      orderID,
		SeriesPrefix: body.SeriesPrefix,
		Buyer:        body.Buyer,
		Metadata:     body.Metadata,
	})
	if err != nil {
		return "", "", false, err
	}

	return out.InvoiceID, out.Number, out.AlreadyIssued, nil
}

// InvoiceOfOrder returns the identity of the document bound to the order.
//
// It answers with an identity and not with the document: a client that wants
// the document reads it from the invoice module's own endpoint, where its shape
// already lives.
func (i *Interop) InvoiceOfOrder(
	ctx context.Context, orderID string,
) (invoiceID, number, status string, err error) {
	return i.w.InvoiceOfOrder(ctx, orderID)
}

// interopAmendRequest is the body [Interop.IssueAmendment] accepts.
type interopAmendRequest struct {
	// SeriesPrefix is the letters of the series to take the number from.
	SeriesPrefix string `json:"series_prefix"`
	// Act is the order journal's entry to document.
	Act ActRef `json:"act"`
	// Rows, when given, put the act's amount on the rows they name.
	Rows []NamedRow `json:"rows"`
	// Metadata is free structured context for the document.
	Metadata map[string]any `json:"metadata"`
}

// interopAmendment is the answer of [Interop.IssueAmendment].
type interopAmendment struct {
	InvoiceID     string           `json:"invoice_id"`
	Number        string           `json:"number"`
	AlreadyIssued bool             `json:"already_issued"`
	Documents     []IssuedDocument `json:"documents"`
}

// IssueAmendment documents one act after the order's sale, or returns the
// documents it has (ADR 0406), as a JSON object of {invoice_id, number,
// already_issued, documents}: the act's document, an exchange's sale, and
// every document of the act, each {invoice_id, number, kind, issued} saying
// whether this call issued it (ADR 0432). already_issued is true when the call
// issued nothing, as on [Interop.IssueForOrder].
func (i *Interop) IssueAmendment(
	ctx context.Context, orderID string, request json.RawMessage,
) (json.RawMessage, error) {
	var body interopAmendRequest
	if err := json.Unmarshal(request, &body); err != nil {
		return nil, errors.Invalid(CodeInvalidInput,
			"the amendment request could not be read: %v", err)
	}

	out, err := i.w.IssueAmendment(ctx, AmendInput{
		OrderID: orderID, SeriesPrefix: body.SeriesPrefix, Act: body.Act, Rows: body.Rows,
		Metadata: body.Metadata,
	})
	if err != nil {
		return nil, err
	}

	return json.Marshal(interopAmendment(out))
}

// AmendmentsOfOrder lists the order's acts after its sale with the live
// document of each, as a JSON array of {kind, id, occurred_at, amount,
// documentable, document, documents, withdrawn} where document is
// {invoice_id, number, kind, status} or null and documents lists every live
// one (ADR 0432).
func (i *Interop) AmendmentsOfOrder(ctx context.Context, orderID string) (json.RawMessage, error) {
	acts, err := i.w.AmendmentsOfOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}

	return json.Marshal(acts)
}
