package service

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
)

// Interop is the module's cross-module surface.
//
// It carries only PRIMITIVE and stdlib types, so a consumer — the invoicing
// flow — can declare the interface on its own side without importing this
// module (ADR 0001/0006). The rich [Service] stays for callers inside the
// module.
//
// # Why it exists at all
//
// The first attempt gave the module no interop surface, on the argument that
// the flow was its only consumer and a contract with one consumer can always be
// narrowed later. That argument is about API design and the rule here is about
// DEPENDENCY DIRECTION: a workflow may not import a module in either direction
// (ADR 0006), and the arch test says so. The narrow surface is not a
// concession, it is the mechanism.
type Interop struct {
	svc *Service
}

// NewInterop builds the surface over the given service.
func NewInterop(svc *Service) *Interop { return &Interop{svc: svc} }

// interopIssue is the document [Interop.IssueJSON] accepts.
//
// It is the same shape as [IssueInput] and it is written out again rather than
// shared, because this is a CONTRACT with another package while IssueInput is
// this package's own type: a field renamed in one has to be a decision about
// the other, not an accident.
type interopIssue struct {
	SeriesPrefix  string             `json:"series_prefix"`
	Kind          string             `json:"kind"`
	CurrencyCode  string             `json:"currency_code"`
	Seller        interopParty       `json:"seller"`
	Buyer         interopParty       `json:"buyer"`
	Lines         []interopIssueLine `json:"lines"`
	Subtotal      int64              `json:"subtotal"`
	DiscountTotal int64              `json:"discount_total"`
	TaxTotal      int64              `json:"tax_total"`
	Total         int64              `json:"total"`
	Metadata      map[string]any     `json:"metadata"`
}

// interopParty is one side of the document on the way in.
type interopParty struct {
	Name        string `json:"name"`
	TaxNumber   string `json:"tax_number"`
	TaxOffice   string `json:"tax_office"`
	Email       string `json:"email"`
	Address     string `json:"address"`
	CountryCode string `json:"country_code"`
}

// interopIssueLine is one row on the way in.
type interopIssueLine struct {
	Description   string `json:"description"`
	Quantity      int64  `json:"quantity"`
	UnitPrice     int64  `json:"unit_price"`
	Subtotal      int64  `json:"subtotal"`
	DiscountTotal int64  `json:"discount_total"`
	TaxRateBps    int32  `json:"tax_rate_bps"`
	TaxTotal      int64  `json:"tax_total"`
	Total         int64  `json:"total"`
}

// toParty converts an incoming party.
func (p interopParty) toParty() models.Party {
	return models.Party{
		Name:        p.Name,
		TaxNumber:   p.TaxNumber,
		TaxOffice:   p.TaxOffice,
		Email:       p.Email,
		Address:     p.Address,
		CountryCode: p.CountryCode,
	}
}

// IssueJSON brings a document into being and returns its identity.
//
// Only the id and the number cross back. The document itself is one read away,
// and returning it here would put its whole shape into a contract for the sake
// of a caller that wants to know what number it got.
func (i *Interop) IssueJSON(
	ctx context.Context, document json.RawMessage,
) (invoiceID, number string, err error) {
	var body interopIssue
	if err := json.Unmarshal(document, &body); err != nil {
		return "", "", errors.Invalid(CodeInvalidInput,
			"the document could not be read: %v", err)
	}

	lines := make([]LineInput, 0, len(body.Lines))
	for i := range body.Lines {
		lines = append(lines, LineInput{
			Description:   body.Lines[i].Description,
			Quantity:      body.Lines[i].Quantity,
			UnitPrice:     body.Lines[i].UnitPrice,
			Subtotal:      body.Lines[i].Subtotal,
			DiscountTotal: body.Lines[i].DiscountTotal,
			TaxRateBps:    body.Lines[i].TaxRateBps,
			TaxTotal:      body.Lines[i].TaxTotal,
			Total:         body.Lines[i].Total,
		})
	}

	issued, err := i.svc.Issue(ctx, IssueInput{
		SeriesPrefix:  body.SeriesPrefix,
		Kind:          models.Kind(body.Kind),
		CurrencyCode:  body.CurrencyCode,
		Seller:        body.Seller.toParty(),
		Buyer:         body.Buyer.toParty(),
		Lines:         lines,
		Subtotal:      body.Subtotal,
		DiscountTotal: body.DiscountTotal,
		TaxTotal:      body.TaxTotal,
		Total:         body.Total,
		Metadata:      body.Metadata,
	})
	if err != nil {
		return "", "", err
	}

	return issued.ID, issued.Number, nil
}

// invoiceIdentity is what a caller holding an id needs to know about a document
// without asking for the document.
type invoiceIdentity struct {
	// ID and Number identify it.
	ID     string `json:"id"`
	Number string `json:"number"`
	// Status is where it stands.
	Status string `json:"status"`
}

// InvoiceIdentityJSON returns the document's id, number and status.
//
// # Why not the whole document
//
// The consumer is a flow answering "which document does this order have", and
// the answer to that is an identity. Returning the whole document would put its
// entire shape into a cross-module contract — every field of a legal document,
// permanently, for a caller that wanted three of them. A client that needs the
// document reads it from this module's own endpoint, which is where the shape
// already lives.
func (i *Interop) InvoiceIdentityJSON(ctx context.Context, id string) (json.RawMessage, error) {
	invoice, err := i.svc.GetInvoice(ctx, id)
	if err != nil {
		return nil, err
	}

	return json.Marshal(invoiceIdentity{
		ID:     invoice.ID,
		Number: invoice.Number,
		Status: invoice.Status.String(),
	})
}
