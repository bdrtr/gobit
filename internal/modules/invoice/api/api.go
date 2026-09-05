// Package api is the invoice module's HTTP surface.
//
// # Only the admin surface
//
// There is no storefront endpoint. A document is a record between the shop and
// the tax authority; what a customer receives is a copy the shop sends them,
// not a resource they browse. Opening a store endpoint would also mean deciding
// how one customer is kept from reading another's document — a question the
// storefront has no identity to answer (ADR 0008).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// codeInvalidRequest is returned when a body or a parameter cannot be read.
const codeInvalidRequest = "invoice_invalid_request"

// The scopes that open this module's endpoints.
const (
	// ScopeRead opens the GET endpoints.
	ScopeRead = "invoice:read"
	// ScopeWrite opens the POST endpoints: issuing a document and moving one.
	//
	// Issuing and moving share one scope rather than splitting into
	// "invoice:issue" and "invoice:move". The split would be a promise the
	// module cannot keep: canceling a document is as consequential as issuing
	// one, so an operator trusted with the second is trusted with the first,
	// and a scope nobody would ever grant separately is a scope that only
	// makes the grant table longer.
	ScopeWrite = "invoice:write"
)

// The endpoint paths.
const (
	// pathAdminInvoices is the listing and the issue endpoint.
	pathAdminInvoices = "/admin/v1/invoices"
	// pathAdminInvoice is the single-document endpoint.
	pathAdminInvoice = "/admin/v1/invoices/{id}"
	// pathAdminStatus is the status move endpoint.
	pathAdminStatus = "/admin/v1/invoices/{id}/status"
	// pathAdminInvoiceSerie is the series listing.
	pathAdminInvoiceSerie = "/admin/v1/invoice-series"
)

// maxBodyBytes bounds the request body.
//
// A document with a few hundred lines is large; a body larger than this is
// either a mistake or an attempt to make the server allocate.
const maxBodyBytes = 1 << 20

// Invoices is the surface the handler needs from the service.
//
// It is declared HERE, on the consumer's side, so the handler depends on the
// four methods it calls rather than on the whole service.
type Invoices interface {
	Issue(ctx context.Context, in service.IssueInput) (models.Invoice, error)
	GetInvoice(ctx context.Context, id string) (models.Invoice, error)
	ListInvoices(ctx context.Context, filter models.Filter) (service.Page, error)
	MoveStatus(ctx context.Context, id string, in service.MoveInput) (models.Invoice, error)
	ListSeries(ctx context.Context) ([]models.Series, error)
}

// Handler serves the invoice endpoints.
type Handler struct {
	svc Invoices
}

// New builds the handler.
func New(svc Invoices) *Handler { return &Handler{svc: svc} }

// Routes mounts the module's endpoints on the router.
func (h *Handler) Routes(r chi.Router) {
	r.With(corehttp.RequireScope(ScopeWrite)).Post(pathAdminInvoices, h.adminIssue)
	r.With(corehttp.RequireScope(ScopeRead)).Get(pathAdminInvoices, h.adminList)
	r.With(corehttp.RequireScope(ScopeRead)).Get(pathAdminInvoice, h.adminGet)
	r.With(corehttp.RequireScope(ScopeWrite)).Post(pathAdminStatus, h.adminMoveStatus)
	r.With(corehttp.RequireScope(ScopeRead)).Get(pathAdminInvoiceSerie, h.adminListSeries)
}

// listEnvelope is the envelope of paginated responses.
type listEnvelope struct {
	// Data holds the records on the page.
	Data any `json:"data"`
	// Count is the number of ALL records matching the filter.
	Count int64 `json:"count"`
	// Offset is the number of skipped records.
	Offset int64 `json:"offset"`
	// Limit is the applied page size.
	Limit int64 `json:"limit"`
	// NextCursor is the opaque position to send back as "after" for the next
	// page; it is ABSENT when this page is the last one.
	NextCursor string `json:"next_cursor,omitempty"`
}

// itemEnvelope is the envelope of single responses.
type itemEnvelope struct {
	Data any `json:"data"`
}

// partyDTO is one side of the document.
type partyDTO struct {
	Name        string `json:"name"`
	TaxNumber   string `json:"tax_number"`
	TaxOffice   string `json:"tax_office"`
	Email       string `json:"email"`
	Address     string `json:"address"`
	CountryCode string `json:"country_code"`
}

// lineDTO is one row of the document.
type lineDTO struct {
	ID            string `json:"id"`
	Position      int32  `json:"position"`
	Description   string `json:"description"`
	Quantity      int64  `json:"quantity"`
	UnitPrice     int64  `json:"unit_price"`
	Subtotal      int64  `json:"subtotal"`
	DiscountTotal int64  `json:"discount_total"`
	TaxRateBps    int32  `json:"tax_rate_bps"`
	TaxTotal      int64  `json:"tax_total"`
	Total         int64  `json:"total"`
}

// invoiceDTO is the external representation of the document.
type invoiceDTO struct {
	ID            string         `json:"id"`
	Number        string         `json:"number"`
	SeriesID      string         `json:"series_id"`
	Kind          string         `json:"kind"`
	Status        string         `json:"status"`
	CurrencyCode  string         `json:"currency_code"`
	Seller        partyDTO       `json:"seller"`
	Buyer         partyDTO       `json:"buyer"`
	Subtotal      int64          `json:"subtotal"`
	DiscountTotal int64          `json:"discount_total"`
	TaxTotal      int64          `json:"tax_total"`
	Total         int64          `json:"total"`
	IssuedAt      time.Time      `json:"issued_at"`
	ProviderID    string         `json:"provider_id"`
	ExternalID    string         `json:"external_id"`
	StatusReason  string         `json:"status_reason"`
	Lines         []lineDTO      `json:"lines"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// seriesDTO is the external representation of a series.
type seriesDTO struct {
	ID         string    `json:"id"`
	Prefix     string    `json:"prefix"`
	Year       int32     `json:"year"`
	LastNumber int64     `json:"last_number"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// toPartyDTO converts a party.
func toPartyDTO(p models.Party) partyDTO {
	return partyDTO{
		Name:        p.Name,
		TaxNumber:   p.TaxNumber,
		TaxOffice:   p.TaxOffice,
		Email:       p.Email,
		Address:     p.Address,
		CountryCode: p.CountryCode,
	}
}

// toInvoiceDTO converts a document.
func toInvoiceDTO(in models.Invoice) invoiceDTO {
	lines := make([]lineDTO, 0, len(in.Lines))
	for i := range in.Lines {
		lines = append(lines, lineDTO{
			ID:            in.Lines[i].ID,
			Position:      in.Lines[i].Position,
			Description:   in.Lines[i].Description,
			Quantity:      in.Lines[i].Quantity,
			UnitPrice:     in.Lines[i].UnitPrice,
			Subtotal:      in.Lines[i].Subtotal,
			DiscountTotal: in.Lines[i].DiscountTotal,
			TaxRateBps:    in.Lines[i].TaxRateBps,
			TaxTotal:      in.Lines[i].TaxTotal,
			Total:         in.Lines[i].Total,
		})
	}

	return invoiceDTO{
		ID:            in.ID,
		Number:        in.Number,
		SeriesID:      in.SeriesID,
		Kind:          in.Kind.String(),
		Status:        in.Status.String(),
		CurrencyCode:  in.CurrencyCode,
		Seller:        toPartyDTO(in.Seller),
		Buyer:         toPartyDTO(in.Buyer),
		Subtotal:      in.Subtotal,
		DiscountTotal: in.DiscountTotal,
		TaxTotal:      in.TaxTotal,
		Total:         in.Total,
		IssuedAt:      in.IssuedAt,
		ProviderID:    in.ProviderID,
		ExternalID:    in.ExternalID,
		StatusReason:  in.StatusReason,
		Lines:         lines,
		Metadata:      in.Metadata,
		CreatedAt:     in.CreatedAt,
		UpdatedAt:     in.UpdatedAt,
	}
}

// toSeriesDTO converts a series.
func toSeriesDTO(s models.Series) seriesDTO {
	return seriesDTO{
		ID:         s.ID,
		Prefix:     s.Prefix,
		Year:       s.Year,
		LastNumber: s.LastNumber,
		CreatedAt:  s.CreatedAt,
		UpdatedAt:  s.UpdatedAt,
	}
}

// writeItem writes a single record inside its envelope.
func writeItem(w http.ResponseWriter, r *http.Request, status int, data any) {
	corehttp.WriteJSON(r.Context(), w, status, itemEnvelope{Data: data})
}

// decode reads the request body.
//
// An unknown field is REJECTED: a client that writes "seriesPrefix" learns what
// it did instead of watching the field be silently ignored.
func decode(r *http.Request, into any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(into); err != nil {
		if errors.Is(err, io.EOF) {
			return coreerrors.Invalid(codeInvalidRequest, "the request body cannot be empty")
		}

		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"the request body could not be read")
	}

	return nil
}

// intParam reads a numeric query parameter; an absent one is zero.
func intParam(r *http.Request, name string) (int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}

	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, coreerrors.Invalid(codeInvalidRequest,
			"the %q parameter has to be a whole number, %q was given", name, raw)
	}

	return value, nil
}

// stringParam reads an optional string filter; an absent one is nil.
//
// The empty string and "not given" are kept apart: an empty status filter is a
// value the client sent by mistake, and turning it into "no filter" would hide
// the mistake.
func stringParam(r *http.Request, name string) *string {
	values, ok := r.URL.Query()[name]
	if !ok || len(values) == 0 {
		return nil
	}

	return &values[0]
}

// afterParam reads the cursor of the page being asked for.
//
// An offset alongside it is REFUSED: a cursor and an offset each name a
// position, and honoring both would serve the page N rows past the cursor,
// which neither of them asked for.
func afterParam(r *http.Request, offset int64) (corepage.Cursor, error) {
	raw := r.URL.Query().Get("after")
	if raw == "" {
		return corepage.Cursor{}, nil
	}
	if offset != 0 {
		return corepage.Cursor{}, coreerrors.Invalid(codeInvalidRequest,
			`"after" and "offset" name two different positions; send one of them`)
	}

	return corepage.Decode(service.InvoiceListing, raw)
}

// invoiceID reads the document identifier from the path.
func invoiceID(r *http.Request) string { return chi.URLParam(r, "id") }
