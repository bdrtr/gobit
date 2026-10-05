package service

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
)

// statusOrder is every status, in the order a document moves through them,
// which is the order the invoice page offers the moves in.
var statusOrder = []models.Status{
	models.StatusIssued, models.StatusSent, models.StatusAccepted, models.StatusRejected, models.StatusCanceled,
}

// adminParty is one side of a document as the invoice page shows it.
type adminParty struct {
	Name        string `json:"name"`
	TaxNumber   string `json:"tax_number"`
	TaxOffice   string `json:"tax_office"`
	Email       string `json:"email"`
	Address     string `json:"address"`
	CountryCode string `json:"country_code"`
}

// adminLine is one row of a document as the invoice page shows it.
type adminLine struct {
	Description   string `json:"description"`
	Quantity      int64  `json:"quantity"`
	UnitPrice     int64  `json:"unit_price"`
	Subtotal      int64  `json:"subtotal"`
	DiscountTotal int64  `json:"discount_total"`
	TaxRateBps    int32  `json:"tax_rate_bps"`
	TaxTotal      int64  `json:"tax_total"`
	Total         int64  `json:"total"`
}

// adminDocument is a document as the invoice page shows it (ADR 0344): what
// the Invoices screen lists, its parties, rows and totals, and the statuses
// it may move to; the json tags are the contract with the panel, which
// cannot import this package.
type adminDocument struct {
	adminInvoice
	Seller           adminParty  `json:"seller"`
	Buyer            adminParty  `json:"buyer"`
	Subtotal         int64       `json:"subtotal"`
	DiscountTotal    int64       `json:"discount_total"`
	TaxTotal         int64       `json:"tax_total"`
	PricesIncludeTax bool        `json:"prices_include_tax"`
	ProviderID       string      `json:"provider_id"`
	ExternalID       string      `json:"external_id"`
	Lines            []adminLine `json:"lines"`
	Moves            []string    `json:"moves"`
	// Amends is the sale document this one amends and why (ADR 0406); absent
	// on a document that amends nothing.
	Amends *adminAmended `json:"amends,omitempty"`
}

// adminAmended names the sale a document amends.
type adminAmended struct {
	ID     string `json:"id"`
	Number string `json:"number"`
	Reason string `json:"reason"`
}

// partyOf is the party as the page shows it.
func partyOf(p models.Party) adminParty {
	return adminParty{
		Name: p.Name, TaxNumber: p.TaxNumber, TaxOffice: p.TaxOffice, Email: p.Email, Address: p.Address,
		CountryCode: p.CountryCode,
	}
}

// InvoiceJSON returns the document with its rows and the statuses it may
// move to (ADR 0344).
func (a *AdminSurface) InvoiceJSON(ctx context.Context, id string) (json.RawMessage, error) {
	invoice, err := a.svc.GetInvoice(ctx, id)
	if err != nil {
		return nil, err
	}

	document := adminDocument{
		adminInvoice: listedInvoice(&invoice),
		Seller:       partyOf(invoice.Seller), Buyer: partyOf(invoice.Buyer),
		Subtotal: invoice.Subtotal, DiscountTotal: invoice.DiscountTotal, TaxTotal: invoice.TaxTotal,
		PricesIncludeTax: invoice.PricesIncludeTax, ProviderID: invoice.ProviderID, ExternalID: invoice.ExternalID,
		Lines: make([]adminLine, 0, len(invoice.Lines)), Moves: []string{},
	}
	for i := range invoice.Lines {
		line := &invoice.Lines[i]
		document.Lines = append(document.Lines, adminLine{
			Description: line.Description, Quantity: line.Quantity, UnitPrice: line.UnitPrice,
			Subtotal: line.Subtotal, DiscountTotal: line.DiscountTotal, TaxRateBps: line.TaxRateBps,
			TaxTotal: line.TaxTotal, Total: line.Total,
		})
	}
	for _, next := range statusOrder {
		if invoice.Status.CanMoveTo(next) {
			document.Moves = append(document.Moves, string(next))
		}
	}
	if invoice.AmendsInvoiceID != "" {
		sale, err := a.svc.GetInvoice(ctx, invoice.AmendsInvoiceID)
		if err != nil {
			return nil, err
		}
		document.Amends = &adminAmended{ID: sale.ID, Number: sale.Number, Reason: invoice.AmendmentReason.String()}
	}
	body, err := json.Marshal(document)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeAdminEncodeFailed,
			"the invoice could not be encoded")
	}

	return body, nil
}

// MoveInvoice moves the document from the status the operator read it in to
// the next one, with why, and refuses when it moved since (ADR 0344).
func (a *AdminSurface) MoveInvoice(ctx context.Context, id, readStatus, to, reason string) error {
	_, err := a.svc.MoveStatus(ctx, id, MoveInput{
		From: models.Status(readStatus), To: models.Status(to), Reason: reason,
	})

	return err
}
