package api

import (
	"net/http"
	"strings"
	"time"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// The admin endpoints for loyalty points (ADR 0164).
//
// Two endpoints, two questions: how many points a customer holds, and why. The
// balance did not go inside the list for the store-credit endpoints' reason —
// the list envelope is {data, count, offset, limit} and a fifth field there
// would make every client that reads that envelope write a branch specific to
// this one endpoint.
//
// There is NO write endpoint. What writes this ledger is money moving, and the
// one function that moves a collection's totals is where it happens; an
// operator-facing adjustment would be a second writer for a number that is
// derived, and it would make the module's own sentence about what a payment
// write means — that it is a MONEY MOVEMENT — false.
//
// There is no storefront endpoint either, and that is this slice's boundary
// rather than an oversight: a customer reading their own balance needs the
// customer claim in the request to be PROVEN, and that proof is a surface this
// module is not wired to today. ADR 0152 wrote it down for store credit and the
// same sentence holds here.

// loyaltyEntryDTO is the outward shape of a ledger row.
type loyaltyEntryDTO struct {
	ID           string `json:"id"`
	CustomerID   string `json:"customer_id"`
	CurrencyCode string `json:"currency_code"`
	// Points is SIGNED: an earn is positive, a reverse is negative. The balance
	// is the sum of these fields and a client can read it the same way.
	//
	// It is NOT money and is not named as if it were: a point is a count the
	// shop decides the worth of, and a field called "balance" here would be read
	// as minor units by everything that reads this module's other answers.
	Points    int64     `json:"points"`
	Kind      string    `json:"kind"`
	Reference string    `json:"reference"`
	CreatedAt time.Time `json:"created_at"`
}

// loyaltyBalanceDTO is the body of the balance answer.
type loyaltyBalanceDTO struct {
	CustomerID   string `json:"customer_id"`
	CurrencyCode string `json:"currency_code"`
	// Points is the ledger's sum: what was earned less what refunds took back.
	Points int64 `json:"points"`
}

// loyaltyPointBalance returns how many points the customer holds
// (GET /admin/v1/loyalty-points/balance).
func (h *Handler) loyaltyPointBalance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customerID := r.URL.Query().Get("customer_id")
	currency := r.URL.Query().Get("currency_code")

	points, err := h.svc.LoyaltyBalance(ctx, customerID, currency)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: loyaltyBalanceDTO{
		CustomerID: customerID,
		// The form the service normalizes to is returned, not the one the request
		// wrote: "try" and "TRY" read the same ledger, and the answer has to say
		// which one it read.
		CurrencyCode: strings.ToUpper(strings.TrimSpace(currency)),
		Points:       points,
	}})
}

// listLoyaltyPoints pages through a customer's point history
// (GET /admin/v1/loyalty-points).
//
// The history is published alongside the balance because the operator's question
// is never only "how many" but "why" — which capture earned them, and which
// refund took them back.
func (h *Handler) listLoyaltyPoints(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	entries, total, err := h.svc.ListLoyalty(ctx, service.ListLoyaltyInput{
		CustomerID:   r.URL.Query().Get("customer_id"),
		CurrencyCode: r.URL.Query().Get("currency_code"),
		Page:         page,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	out := make([]loyaltyEntryDTO, 0, len(entries))
	for i := range entries {
		out = append(out, toLoyaltyEntryDTO(entries[i]))
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   out,
		Count:  total,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// toLoyaltyEntryDTO turns a ledger row into its outward shape.
func toLoyaltyEntryDTO(entry models.LoyaltyEntry) loyaltyEntryDTO {
	return loyaltyEntryDTO{
		ID:           entry.ID,
		CustomerID:   entry.CustomerID,
		CurrencyCode: entry.CurrencyCode,
		Points:       entry.Points,
		Kind:         entry.Kind.String(),
		Reference:    entry.Reference,
		CreatedAt:    entry.CreatedAt,
	}
}
