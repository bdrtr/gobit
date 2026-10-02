package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
)

// adminInvoice is one document as the panel lists it (ADR 0343); the json
// tags are the contract with the panel, which cannot import this package.
type adminInvoice struct {
	ID           string    `json:"id"`
	Number       string    `json:"number"`
	Kind         string    `json:"kind"`
	Status       string    `json:"status"`
	StatusReason string    `json:"status_reason"`
	BuyerName    string    `json:"buyer_name"`
	CurrencyCode string    `json:"currency_code"`
	Total        int64     `json:"total"`
	IssuedAt     time.Time `json:"issued_at"`
}

// listedInvoice is the document as the Invoices screen lists it.
func listedInvoice(invoice *models.Invoice) adminInvoice {
	return adminInvoice{
		ID: invoice.ID, Number: invoice.Number, Kind: string(invoice.Kind), Status: string(invoice.Status),
		StatusReason: invoice.StatusReason, BuyerName: invoice.Buyer.Name, CurrencyCode: invoice.CurrencyCode,
		Total: invoice.Total, IssuedAt: invoice.IssuedAt,
	}
}

// InvoicesJSON lists the documents in the status, or in every status when it
// is empty, the latest first, a page at a time, with how many there are (ADR
// 0343).
func (a *AdminSurface) InvoicesJSON(
	ctx context.Context, status string, limit, offset int32,
) (json.RawMessage, int64, error) {
	filter := models.Filter{Limit: int64(limit), Offset: int64(offset)}
	if status != "" {
		if !models.Status(status).Valid() {
			return nil, 0, errors.Invalid(CodeInvalidInput, "unknown invoice status: %q", status)
		}
		filter.Status = &status
	}
	page, err := a.svc.ListInvoices(ctx, filter)
	if err != nil {
		return nil, 0, err
	}

	out := make([]adminInvoice, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, listedInvoice(&page.Items[i]))
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, 0, errors.Wrap(err, errors.KindInternal, codeAdminEncodeFailed,
			"the invoices could not be encoded")
	}

	return body, page.Count, nil
}
