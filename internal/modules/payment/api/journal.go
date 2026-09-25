package api

import (
	"net/http"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// The payment journal endpoint (ADR 0186): the module's movements as balanced
// debit and credit lines, over a window, for an accountant's books.

const (
	// pathAdminPaymentJournal is the journal's one address.
	pathAdminPaymentJournal = "/admin/v1/payment-journal"

	paramJournalFrom = "from"
	paramJournalTo   = "to"
)

// journalDTO is the body of the journal answer.
type journalDTO struct {
	From         time.Time `json:"from"`
	To           time.Time `json:"to"`
	CurrencyCode string    `json:"currency_code,omitempty"`
	// Entries are in time order, and each one's debits equal its credits.
	Entries []models.JournalEntry `json:"entries"`
	// Balances sum the entries per currency, account and provider: a trial
	// balance whose debits equal its credits in every currency.
	Balances []models.JournalBalance `json:"balances"`
}

// paymentJournal returns the module's books over a window
// (GET /admin/v1/payment-journal).
func (h *Handler) paymentJournal(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	from, err := journalTime(r, paramJournalFrom)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	to, err := journalTime(r, paramJournalTo)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	journal, err := h.svc.Journal(ctx, service.JournalQuery{
		From: from, To: to, CurrencyCode: r.URL.Query().Get(paramCurrencyCode),
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: journalDTO{
		From: journal.From, To: journal.To, CurrencyCode: journal.CurrencyCode,
		Entries: journal.Entries, Balances: journal.Balances,
	}})
}

// journalTime reads one end of the window, which is required and RFC 3339.
func journalTime(r *http.Request, name string) (time.Time, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return time.Time{}, coreerrors.Invalid(service.CodeInvalidInput,
			"%q is required: the journal reads a window, and an open end would read the whole history", name)
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, coreerrors.Invalid(service.CodeInvalidInput,
			"%q has to be an RFC 3339 time: %q", name, raw)
	}

	return parsed, nil
}

// describeJournal describes the journal endpoint.
func describeJournal(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathAdminPaymentJournal, openapi.Operation{
		Summary: "Reads the payment module's movements as double-entry journal entries.",
		Description: "Every entry is DERIVED from a row the module already keeps and never " +
			"deletes, so the journal cannot disagree with the payments it describes: a " +
			"capture debits the account the money came from (a provider's clearing, or the " +
			"store credit or loyalty points the customer spent) and credits receivable; a " +
			"refund does the reverse; a store credit grant debits store_credit_granted and " +
			"credits store_credit; a loyalty earn debits loyalty_granted and credits " +
			"loyalty, and a reverse does the reverse. A hold and a release are a tender's " +
			"own mechanics and move no money, so they are not entries. " +
			"\n\n" +
			"Each entry's debits equal its credits, and so do the balances' in every " +
			"currency. receivable runs negative on these books alone: its debit side, the " +
			"order that created the obligation, is the order module's to publish. " +
			"\n\n" +
			"Amounts are integers in the currency's minor unit, and a loyalty point is " +
			"worth one of them. A window wider than 93 days, or holding more than 10000 " +
			"movements, is refused rather than cut.",
		Parameters: []openapi.Parameter{
			{
				Name: paramJournalFrom, In: inQuery, Required: true,
				Schema:      map[string]any{schemaType: typeString, "format": "date-time"},
				Description: "The window's start, included (RFC 3339).",
			},
			{
				Name: paramJournalTo, In: inQuery, Required: true,
				Schema:      map[string]any{schemaType: typeString, "format": "date-time"},
				Description: "The window's end, excluded (RFC 3339).",
			},
			{
				Name: paramCurrencyCode, In: inQuery,
				Schema:      map[string]any{schemaType: typeString},
				Description: "Reads one currency's movements only; without it every currency is read, each entry and balance in its own.",
			},
		},
		Responses: map[string]any{
			"200": openapi.Response("The journal over the window", d.Item(journalDTO{})),
		},
	})
}
