package api

import (
	"net/http"
	"strings"
	"time"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// The admin endpoints for store credit (ADR 0152).
//
// Three endpoints, three questions: GIVE the credit, READ the balance, READ the
// history. The balance did not go inside the list because the list envelope is
// {data, count, offset, limit}, and a fifth field there would make every client
// that reads that envelope write a branch specific to this one endpoint.
//
// There is NO storefront endpoint, and that is this slice's boundary rather than
// an oversight: a customer reading their own balance needs the customer claim in
// the request to be PROVEN (the comparison in ADR 0057/0125), and that proof is a
// surface this module is not wired to today. ADR 0152 writes it down among the
// things it does not close.

// issueCreditRequest is the body that gives credit.
type issueCreditRequest struct {
	// CustomerID is who receives the credit; it is required.
	CustomerID string `json:"customer_id"`
	// CurrencyCode is the ISO 4217 code; it is required. Credit in one currency
	// is not credit in another.
	CurrencyCode string `json:"currency_code"`
	// Amount is what is given (minor unit) and has to be positive.
	Amount int64 `json:"amount"`
	// Reason is why the credit was given; it is REQUIRED.
	//
	// This is the half a balance column cannot hold: the answer to "why does this
	// customer have 200 lira" will never exist again if it is not in the record
	// the moment it is asked.
	Reason string `json:"reason"`
	// Reference is the identifier of the operator's own record (a ticket, a
	// return); it may be empty.
	Reference string `json:"reference"`
}

// storeCreditEntryDTO is the outward shape of a ledger row.
type storeCreditEntryDTO struct {
	ID           string `json:"id"`
	CustomerID   string `json:"customer_id"`
	CurrencyCode string `json:"currency_code"`
	// Amount is SIGNED: a hold is negative, an issue and the returns are
	// positive. The balance is the sum of these fields and a client can read it
	// the same way.
	Amount    int64     `json:"amount"`
	Kind      string    `json:"kind"`
	Reference string    `json:"reference,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// storeCreditBalanceDTO is the body of the balance answer.
type storeCreditBalanceDTO struct {
	CustomerID   string `json:"customer_id"`
	CurrencyCode string `json:"currency_code"`
	// Balance is what can be spent: the ledger's sum, which is to say with OPEN
	// HOLDS already subtracted. While a cart is waiting at the payment step the
	// balance looks that much lower, and that is the right answer — the money is
	// promised.
	Balance int64 `json:"balance"`
}

// issueStoreCredit puts store credit on a customer's account
// (POST /admin/v1/store-credits).
func (h *Handler) issueStoreCredit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body issueCreditRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	entry, err := h.svc.IssueCredit(ctx, service.IssueCreditInput{
		CustomerID:   body.CustomerID,
		CurrencyCode: body.CurrencyCode,
		Amount:       body.Amount,
		Reason:       body.Reason,
		Reference:    body.Reference,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusCreated,
		singleEnvelope{Data: toStoreCreditEntryDTO(entry)})
}

// storeCreditBalance returns what the customer can spend
// (GET /admin/v1/store-credits/balance).
func (h *Handler) storeCreditBalance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customerID := r.URL.Query().Get("customer_id")
	currency := r.URL.Query().Get("currency_code")

	balance, err := h.svc.StoreCreditBalance(ctx, customerID, currency)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: storeCreditBalanceDTO{
		CustomerID: customerID,
		// The form the service normalizes to is returned, not the one the request
		// wrote: "try" and "TRY" read the same ledger, and the answer has to say
		// which one it read.
		CurrencyCode: strings.ToUpper(strings.TrimSpace(currency)),
		Balance:      balance,
	}})
}

// listStoreCredit pages through a customer's credit history
// (GET /admin/v1/store-credits).
//
// The history is published alongside the balance because the operator's question
// is never only "how much is left" but "why" — and that answer is in the rows.
func (h *Handler) listStoreCredit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	entries, total, err := h.svc.ListStoreCredit(ctx, service.ListStoreCreditInput{
		CustomerID:   r.URL.Query().Get("customer_id"),
		CurrencyCode: r.URL.Query().Get("currency_code"),
		Page:         page,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	out := make([]storeCreditEntryDTO, 0, len(entries))
	for i := range entries {
		out = append(out, toStoreCreditEntryDTO(entries[i]))
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   out,
		Count:  total,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// toStoreCreditEntryDTO turns a ledger row into its outward shape.
func toStoreCreditEntryDTO(entry models.StoreCreditEntry) storeCreditEntryDTO {
	return storeCreditEntryDTO{
		ID:           entry.ID,
		CustomerID:   entry.CustomerID,
		CurrencyCode: entry.CurrencyCode,
		Amount:       entry.Amount,
		Kind:         entry.Kind.String(),
		Reference:    entry.Reference,
		Reason:       entry.Reason,
		CreatedAt:    entry.CreatedAt,
	}
}
