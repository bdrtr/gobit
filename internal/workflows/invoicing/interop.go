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
// The two parties travel as JSON rather than as a dozen string arguments: they
// are printed fields with fixed meanings, and a positional signature of twelve
// strings is one a caller gets wrong silently.
type interopIssueRequest struct {
	// SeriesPrefix is the letters of the series to take the number from.
	SeriesPrefix string `json:"series_prefix"`
	// Seller and Buyer are the two sides as they are to be printed.
	Seller Party `json:"seller"`
	Buyer  Party `json:"buyer"`
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
		Seller:       body.Seller,
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
