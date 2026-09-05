package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// issueLineRequest is one row of a document being issued.
type issueLineRequest struct {
	Description   string `json:"description"`
	Quantity      int64  `json:"quantity"`
	UnitPrice     int64  `json:"unit_price"`
	Subtotal      int64  `json:"subtotal"`
	DiscountTotal int64  `json:"discount_total"`
	TaxRateBps    int32  `json:"tax_rate_bps"`
	TaxTotal      int64  `json:"tax_total"`
	Total         int64  `json:"total"`
}

// partyRequest is one side of a document being issued.
type partyRequest struct {
	Name        string `json:"name"`
	TaxNumber   string `json:"tax_number"`
	TaxOffice   string `json:"tax_office"`
	Email       string `json:"email"`
	Address     string `json:"address"`
	CountryCode string `json:"country_code"`
}

// issueRequest is the body of an issue.
//
// The totals are part of the body and are CHECKED against the lines rather than
// derived from them: a caller that lost a line would otherwise send a document
// that adds up perfectly and is missing a row.
type issueRequest struct {
	SeriesPrefix  string             `json:"series_prefix"`
	Kind          string             `json:"kind"`
	CurrencyCode  string             `json:"currency_code"`
	Seller        partyRequest       `json:"seller"`
	Buyer         partyRequest       `json:"buyer"`
	Lines         []issueLineRequest `json:"lines"`
	Subtotal      int64              `json:"subtotal"`
	DiscountTotal int64              `json:"discount_total"`
	TaxTotal      int64              `json:"tax_total"`
	Total         int64              `json:"total"`
	Metadata      map[string]any     `json:"metadata"`
}

// statusRequest is the body of a status move.
type statusRequest struct {
	Status     string `json:"status"`
	Reason     string `json:"reason"`
	ProviderID string `json:"provider_id"`
	ExternalID string `json:"external_id"`
}

// toParty converts a request party.
func (p partyRequest) toParty() models.Party {
	return models.Party{
		Name:        p.Name,
		TaxNumber:   p.TaxNumber,
		TaxOffice:   p.TaxOffice,
		Email:       p.Email,
		Address:     p.Address,
		CountryCode: p.CountryCode,
	}
}

// adminIssue brings a document into being (POST /admin/v1/invoices).
//
// # Why the whole document is in the body
//
// The module does not know what an order is (ADR 0001), so it cannot fetch one
// and copy it. The caller sends the finished document; assembling it from an
// order is a workflow's job, and sending it directly is what lets a shop
// invoice something that is not an order at all — a service, a manual sale.
func (h *Handler) adminIssue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body issueRequest
	if err := decode(r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	lines := make([]service.LineInput, 0, len(body.Lines))
	for i := range body.Lines {
		lines = append(lines, service.LineInput{
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

	issued, err := h.svc.Issue(ctx, service.IssueInput{
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
		corehttp.WriteError(ctx, w, err)

		return
	}

	writeItem(w, r, http.StatusCreated, toInvoiceDTO(issued))
}

// adminGet returns one document with its lines (GET /admin/v1/invoices/{id}).
func (h *Handler) adminGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	invoice, err := h.svc.GetInvoice(ctx, invoiceID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	writeItem(w, r, http.StatusOK, toInvoiceDTO(invoice))
}

// adminList pages the documents (GET /admin/v1/invoices).
//
// The lines are NOT in the listing: a page of documents with all their rows
// would be an N+1 and a body nobody needs in that shape. They come with the
// single-document endpoint.
func (h *Handler) adminList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, err := intParam(r, "limit")
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	offset, err := intParam(r, "offset")
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	after, err := afterParam(r, offset)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	page, err := h.svc.ListInvoices(ctx, models.Filter{
		Status: stringParam(r, "status"),
		Kind:   stringParam(r, "kind"),
		Limit:  limit,
		Offset: offset,
		After:  after,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	data := make([]invoiceDTO, 0, len(page.Items))
	for i := range page.Items {
		data = append(data, toInvoiceDTO(page.Items[i]))
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:       data,
		Count:      page.Count,
		Offset:     page.Offset,
		Limit:      page.Limit,
		NextCursor: page.NextCursor,
	})
}

// adminMoveStatus moves a document (POST /admin/v1/invoices/{id}/status).
//
// It is a POST to a sub-path rather than a PATCH on the document, because the
// document itself is IMMUTABLE: what changes is where it stands, and a PATCH
// would suggest the amounts could be edited too.
func (h *Handler) adminMoveStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body statusRequest
	if err := decode(r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	moved, err := h.svc.MoveStatus(ctx, invoiceID(r), service.MoveInput{
		To:         models.Status(body.Status),
		Reason:     body.Reason,
		ProviderID: body.ProviderID,
		ExternalID: body.ExternalID,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	writeItem(w, r, http.StatusOK, toInvoiceDTO(moved))
}

// adminListSeries returns the series (GET /admin/v1/invoice-series).
//
// It exists so an operator can see the numbering itself: which series are open,
// which year they belong to, and how far each has gone. A series opened by a
// typo in the configured prefix shows up here with its counter at 1, which is
// the only place that mistake is visible.
func (h *Handler) adminListSeries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	series, err := h.svc.ListSeries(ctx)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	data := make([]seriesDTO, 0, len(series))
	for i := range series {
		data = append(data, toSeriesDTO(series[i]))
	}

	writeItem(w, r, http.StatusOK, data)
}
