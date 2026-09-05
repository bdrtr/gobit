package invoicing

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// Invoices is the part of the invoice module this flow uses.
//
// It is declared HERE, on the consumer's side, and carries only primitives and
// JSON. That is not a style preference: a workflow may not import a module in
// EITHER direction (ADR 0006), and the arch test enforces it — the first
// version of this file imported the invoice module's types and was refused.
type Invoices interface {
	// IssueJSON brings a document into being and returns its identity.
	IssueJSON(ctx context.Context, document json.RawMessage) (invoiceID, number string, err error)
	// InvoiceIdentityJSON returns the document's id, number and status.
	InvoiceIdentityJSON(ctx context.Context, id string) (json.RawMessage, error)
}

// shippingDescription is what a shipping line is called on the document.
//
// It is a constant rather than a parameter because it is printed text with no
// decision in it; a shop that wants other wording issues the document itself
// with its own lines.
const shippingDescription = "Shipping"

// kindSale is the document kind a sale produces.
//
// The value is the invoice module's and is REPEATED here as a literal, the same
// way the flows repeat container names: reaching into the module for a constant
// would tie this package to it at compile time for the sake of a string.
const kindSale = "sale"

// Party is one side of the document as this flow carries it.
type Party struct {
	// Name is the legal name printed on the document.
	Name string `json:"name"`
	// TaxNumber is the VKN/TCKN or its equivalent; it may be empty.
	TaxNumber string `json:"tax_number"`
	// TaxOffice is the tax office the number belongs to; it may be empty.
	TaxOffice string `json:"tax_office"`
	// Email is where the document is sent; it may be empty.
	Email string `json:"email"`
	// Address is the printed address, already formatted into lines.
	Address string `json:"address"`
	// CountryCode is the ISO 3166-1 alpha-2 country code.
	CountryCode string `json:"country_code"`
}

// IssueInput is the request to invoice an order.
//
// The two parties come from the CALLER and the lines come from the order, and
// the split is not arbitrary. The seller's legal details are the shop's own
// configuration, which lives in no module here. The buyer's — the VKN or TCKN
// and the tax office — are not in this repository's customer model at all: a
// shop collects them at checkout as its own fields. A framework that guessed
// them would produce a document that is wrong in the one way a document must
// not be.
type IssueInput struct {
	// OrderID is the sale to invoice.
	OrderID string
	// SeriesPrefix is the letters of the series to take the number from.
	SeriesPrefix string
	// Seller is the shop, as it is to be printed.
	Seller Party
	// Buyer is the customer, as they are to be printed.
	//
	// An empty Email is filled in from the order; everything else is taken as
	// given, because the order does not know it.
	Buyer Party
	// Metadata is free structured context for the document.
	Metadata map[string]any
}

// IssueResult reports what invoicing an order did.
type IssueResult struct {
	// InvoiceID and Number identify the document.
	InvoiceID string
	Number    string
	// AlreadyIssued reports that the order HAD a document and this call
	// returned it instead of issuing a second one.
	//
	// It is reported rather than hidden because the two outcomes look identical
	// to a caller that only reads the number, and an operator pressing a button
	// twice deserves to be told the second press did nothing.
	AlreadyIssued bool
}

// documentLine is one row of the document this flow assembles.
type documentLine struct {
	Description   string `json:"description"`
	Quantity      int64  `json:"quantity"`
	UnitPrice     int64  `json:"unit_price"`
	Subtotal      int64  `json:"subtotal"`
	DiscountTotal int64  `json:"discount_total"`
	TaxRateBps    int32  `json:"tax_rate_bps"`
	TaxTotal      int64  `json:"tax_total"`
	Total         int64  `json:"total"`
}

// document is the body the invoice module's surface accepts.
type document struct {
	SeriesPrefix  string         `json:"series_prefix"`
	Kind          string         `json:"kind"`
	CurrencyCode  string         `json:"currency_code"`
	Seller        Party          `json:"seller"`
	Buyer         Party          `json:"buyer"`
	Lines         []documentLine `json:"lines"`
	Subtotal      int64          `json:"subtotal"`
	DiscountTotal int64          `json:"discount_total"`
	TaxTotal      int64          `json:"tax_total"`
	Total         int64          `json:"total"`
	Metadata      map[string]any `json:"metadata"`
}

// IssueForOrder issues the document for an order, or returns the one it has.
//
// # Why the existing document is returned instead of a second one
//
// A number is spent for good once it is taken (ADR 0024). A double-click that
// issued a second document would burn a number, put two documents on one sale,
// and leave the shop to cancel one of them — and a cancellation is a permanent
// mark on a record that never should have existed. Reading the link first is
// what makes the button safe to press twice.
//
// # The window this does NOT close
//
// Two calls arriving at the SAME moment can both read no link and both issue.
// The link is written after the document, and there is no transaction spanning
// two modules — by ADR 0001 there could not be.
//
// What happens then is not silent, and that is the part worth knowing. The
// definition is ONE TO ONE, so the second binding is refused by the link
// service as a conflict: the second document exists, it is not bound to the
// order, and the caller is told with both identifiers in the message. A shop is
// left with one document to cancel and everything it needs to find it — which
// is a worse outcome than a lock and a much better one than two documents that
// both look bound.
//
// The window needs two operators pressing at the same instant, against a defect
// that would otherwise happen every time somebody double-clicks. Closing it
// properly needs an idempotency key from the caller, which the admin surface
// already carries (corehttp.Idempotency).
func (w *Workflows) IssueForOrder(ctx context.Context, in IssueInput) (IssueResult, error) {
	if strings.TrimSpace(in.OrderID) == "" {
		return IssueResult{}, errors.Invalid(CodeInvalidInput, "the order id is required")
	}

	existing, found, err := w.existingInvoice(ctx, in.OrderID)
	if err != nil {
		return IssueResult{}, err
	}
	if found {
		return IssueResult{
			InvoiceID:     existing.ID,
			Number:        existing.Number,
			AlreadyIssued: true,
		}, nil
	}

	order, err := w.readOrder(ctx, in.OrderID)
	if err != nil {
		return IssueResult{}, err
	}

	buyer := in.Buyer
	if buyer.Email == "" {
		buyer.Email = order.Email
	}

	body, err := json.Marshal(document{
		SeriesPrefix: in.SeriesPrefix,
		Kind:         kindSale,
		CurrencyCode: order.CurrencyCode,
		Seller:       in.Seller,
		Buyer:        buyer,
		Lines:        order.lines(),
		// The document's subtotal carries the carriage, because carriage
		// reaches it as a LINE; the order keeps the two apart because that is
		// how it prices them. The identity survives the move: the order's
		// total is subtotal - discount + tax + shipping, and the document's is
		// (subtotal + shipping) - discount + tax, which is the same number.
		Subtotal:      order.Subtotal + order.ShippingTotal,
		DiscountTotal: order.DiscountTotal,
		TaxTotal:      order.TaxTotal,
		Total:         order.Total,
		Metadata:      in.Metadata,
	})
	if err != nil {
		return IssueResult{}, errors.Internal(CodeInvalidInput,
			"the document could not be encoded: %v", err)
	}

	invoiceID, number, err := w.invoices.IssueJSON(ctx, body)
	if err != nil {
		return IssueResult{}, err
	}

	// The link is written AFTER the document, and a failure here does not undo
	// it: the number is spent and the document is real. What is lost is the
	// binding, which means the next call cannot find it and would issue a
	// second one — so the failure is logged at ERROR with both identifiers, and
	// the operator can re-link or cancel with everything they need in one line.
	if err := w.links.Create(ctx, LinkOrderInvoice, in.OrderID, invoiceID); err != nil {
		w.log.ErrorContext(ctx,
			"the invoice was issued and the order was NOT bound to it; a second issue would "+
				"spend another number",
			"order_id", in.OrderID, "invoice_id", invoiceID,
			"invoice_number", number, "error", err)

		return IssueResult{}, errors.Wrap(err, errors.KindOf(err), CodeLinkFailed,
			"invoice %s (%s) was issued for order %s but could not be bound to it",
			invoiceID, number, in.OrderID)
	}

	return IssueResult{InvoiceID: invoiceID, Number: number}, nil
}

// InvoiceOfOrder returns the identity of the document bound to the order.
//
// It is the reader that makes the link worth writing: a binding nothing reads
// is the capability-without-a-consumer this repository has a name for.
//
// It answers with an IDENTITY and not with the document. A client that wants
// the document reads it from the invoice module's own endpoint, which is where
// its shape lives; this endpoint answers a question about an ORDER.
func (w *Workflows) InvoiceOfOrder(
	ctx context.Context, orderID string,
) (invoiceID, number, status string, err error) {
	if strings.TrimSpace(orderID) == "" {
		return "", "", "", errors.Invalid(CodeInvalidInput, "the order id is required")
	}

	invoice, found, err := w.existingInvoice(ctx, orderID)
	if err != nil {
		return "", "", "", err
	}
	if !found {
		return "", "", "", errors.NotFound(CodeInvalidInput, "order %s has no invoice", orderID)
	}

	return invoice.ID, invoice.Number, invoice.Status, nil
}

// linkedInvoice is what this flow needs to know about a document it found.
type linkedInvoice struct {
	// ID and Number identify it.
	ID     string `json:"id"`
	Number string `json:"number"`
	// Status is where it stands.
	Status string `json:"status"`
}

// existingInvoice returns the document bound to the order, if there is one.
func (w *Workflows) existingInvoice(
	ctx context.Context, orderID string,
) (linkedInvoice, bool, error) {
	linked, err := w.links.ListMany(ctx, LinkOrderInvoice, []string{orderID})
	if err != nil {
		return linkedInvoice{}, false, errors.Wrap(err, errors.KindOf(err),
			CodeOrderUnreadable, "the invoice of order %s could not be read", orderID)
	}

	ids := linked[orderID]
	if len(ids) == 0 {
		return linkedInvoice{}, false, nil
	}

	// The definition is one to one, so more than one is a data fault rather
	// than a choice. Taking the first keeps a broken link from making the flow
	// issue a document the order already has.
	body, err := w.invoices.InvoiceIdentityJSON(ctx, ids[0])
	if err != nil {
		return linkedInvoice{}, false, err
	}

	var found linkedInvoice
	if err := json.Unmarshal(body, &found); err != nil {
		return linkedInvoice{}, false, errors.Internal(CodeOrderUnreadable,
			"the invoice surface returned a body this flow cannot read (%s): %v", ids[0], err)
	}

	return found, true, nil
}

// invoiceOrder is the order as this flow reads it over the interop surface.
type invoiceOrder struct {
	OrderID       string             `json:"order_id"`
	DisplayID     int64              `json:"display_id"`
	CurrencyCode  string             `json:"currency_code"`
	Email         string             `json:"email"`
	Status        string             `json:"status"`
	Subtotal      int64              `json:"subtotal"`
	DiscountTotal int64              `json:"discount_total"`
	TaxTotal      int64              `json:"tax_total"`
	ShippingTotal int64              `json:"shipping_total"`
	Total         int64              `json:"total"`
	Items         []invoiceOrderItem `json:"items"`
}

// invoiceOrderItem is one line of that order.
type invoiceOrderItem struct {
	Title         string `json:"title"`
	Quantity      int64  `json:"quantity"`
	UnitPrice     int64  `json:"unit_price"`
	Subtotal      int64  `json:"subtotal"`
	DiscountTotal int64  `json:"discount_total"`
	TaxRateBps    int32  `json:"tax_rate_bps"`
	TaxTotal      int64  `json:"tax_total"`
	Total         int64  `json:"total"`
}

// lines turns the order's lines into the document's, adding carriage as a LINE.
//
// # Why carriage becomes a line
//
// The order keeps shipping as a separate total because that is how it is
// priced. A document has no such term: it prints rows and adds them up, and
// what the customer paid for carriage is one of the rows.
//
// The carriage line carries NO tax, because the cart does not tax shipping
// today (workflows/cart/tax.go wires it off). If that changes, the rate has to
// come from the order rather than be assumed here, and this is the line that
// has to change with it.
func (o invoiceOrder) lines() []documentLine {
	lines := make([]documentLine, 0, len(o.Items)+1)

	for i := range o.Items {
		lines = append(lines, documentLine{
			Description:   o.Items[i].Title,
			Quantity:      o.Items[i].Quantity,
			UnitPrice:     o.Items[i].UnitPrice,
			Subtotal:      o.Items[i].Subtotal,
			DiscountTotal: o.Items[i].DiscountTotal,
			TaxRateBps:    o.Items[i].TaxRateBps,
			TaxTotal:      o.Items[i].TaxTotal,
			Total:         o.Items[i].Total,
		})
	}

	if o.ShippingTotal != 0 {
		lines = append(lines, documentLine{
			Description: shippingDescription,
			Quantity:    1,
			UnitPrice:   o.ShippingTotal,
			Subtotal:    o.ShippingTotal,
			Total:       o.ShippingTotal,
		})
	}

	return lines
}

// readOrder reads the order over the interop surface.
func (w *Workflows) readOrder(ctx context.Context, orderID string) (invoiceOrder, error) {
	raw, err := w.orders.OrderInvoiceJSON(ctx, orderID)
	if err != nil {
		return invoiceOrder{}, errors.Wrap(err, errors.KindOf(err), CodeOrderUnreadable,
			"order %s could not be read", orderID)
	}

	var order invoiceOrder
	if err := json.Unmarshal(raw, &order); err != nil {
		return invoiceOrder{}, errors.Internal(CodeOrderUnreadable,
			"the order surface returned a body this flow cannot read (%s): %v", orderID, err)
	}

	if len(order.Items) == 0 {
		return invoiceOrder{}, errors.Conflict(CodeInvalidInput,
			"order %s has no lines, so there is nothing to invoice", orderID)
	}

	return order, nil
}
