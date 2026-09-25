package api

import (
	"net/http"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// The order journal endpoint (ADR 0188).

// pathAdminOrderJournal is the journal's one address.
const pathAdminOrderJournal = "/admin/v1/order-journal"

// orderJournalDTO is the body of the journal answer.
type orderJournalDTO struct {
	From         time.Time `json:"from"`
	To           time.Time `json:"to"`
	CurrencyCode string    `json:"currency_code,omitempty"`
	// Entries are in time order, and each one's debits equal its credits.
	Entries []models.JournalEntry `json:"entries"`
	// Balances sum the entries per currency and account.
	Balances []models.JournalBalance `json:"balances"`
}

// adminOrderJournal returns the module's books over a window
// (GET /admin/v1/order-journal).
func (h *Handler) adminOrderJournal(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	from, err := journalMoment(r, "from")
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	to, err := journalMoment(r, "to")
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	journal, err := h.svc.Journal(ctx, service.JournalQuery{
		From: from, To: to, CurrencyCode: r.URL.Query().Get("currency_code"),
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: orderJournalDTO{
		From: journal.From, To: journal.To, CurrencyCode: journal.CurrencyCode,
		Entries: journal.Entries, Balances: journal.Balances,
	}})
}

// journalMoment reads one end of the window, which is required and RFC 3339.
func journalMoment(r *http.Request, name string) (time.Time, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return time.Time{}, coreerrors.Invalid(codeInvalidRequest,
			"%q is required: the journal reads a window, and an open end would read the whole history", name)
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"%q has to be an RFC 3339 moment, and %q is not", name, raw)
	}

	return parsed, nil
}

// describeOrderJournal describes the journal endpoint.
func describeOrderJournal(d *openapi.Doc) {
	from := queryParameter("from", typeString, "The window's start, included (RFC 3339).")
	from.Required = true
	to := queryParameter("to", typeString, "The window's end, excluded (RFC 3339).")
	to.Required = true
	currency := queryParameter("currency_code", typeString,
		"Reads one currency's orders only; without it every currency is read, each entry and "+
			"balance in its own.")

	d.Describe(http.MethodGet, pathAdminOrderJournal, openapi.Operation{
		Summary: "Reads the order module's facts as double-entry journal entries.",
		Description: "Every entry is DERIVED from a record the module keeps and never deletes. " +
			"An order placed debits receivable with its total and sales_discounts with its " +
			"discount, and credits sales with its subtotal, tax_payable with its tax and " +
			"shipping with its shipping; an order canceled is the same lines the other way; a " +
			"credit line debits credit_allowances and credits receivable. A refund that names " +
			"one of the order's returns debits sales_returns, and one that names a claim " +
			"debits claim_allowances, each against receivable, at the refund's amount and " +
			"moment. A zero amount writes no line, and an order of nothing is no entry. " +
			"\n\n" +
			"receivable is the payment module's account too (GET /admin/v1/payment-journal): " +
			"a capture credits it and a refund debits it, so the two journals together close " +
			"an order that was paid, returned, canceled or written off. A line cancellation is " +
			"not an entry, because it records a quantity and no amount; its money arrives as a " +
			"credit line or a refund. An exchange's difference lives on a collection of its " +
			"own and is outside these books. " +
			"\n\n" +
			"Amounts are integers in the currency's minor unit. A window wider than 93 days, " +
			"or holding more than 10000 facts, is refused rather than cut.",
		Parameters: []openapi.Parameter{from, to, currency},
		Responses: map[string]any{
			"200": openapi.Response("The journal over the window", d.Item(orderJournalDTO{})),
		},
	})
}
