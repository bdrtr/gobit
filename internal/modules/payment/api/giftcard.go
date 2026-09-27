package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// The admin endpoints for gift cards (ADR 0208): issue a card, read it, list
// the cards, read a card's history. A card is spent at the storefront through
// the gift_card provider, with its code in the payment's data; there is no
// storefront endpoint that reads a card, because answering "what does this code
// hold" to anybody who asks would help a guesser more than a shopper.
const (
	pathAdminGiftCards       = "/admin/v1/gift-cards"
	pathAdminGiftCard        = "/admin/v1/gift-cards/{id}"
	pathAdminGiftCardEntries = "/admin/v1/gift-cards/{id}/entries"
	pathAdminGiftCardCode    = "/admin/v1/gift-cards/{id}/code"
	pathAdminGiftCardDisable = "/admin/v1/gift-cards/{id}/disable"
)

// issueGiftCardRequest is the body that issues a card.
type issueGiftCardRequest struct {
	// CurrencyCode is the one currency the card holds; required.
	CurrencyCode string `json:"currency_code"`
	// Amount is the balance the card is issued with (minor unit); positive.
	Amount int64 `json:"amount"`
	// Reason is why the card is issued; required.
	Reason string `json:"reason"`
	// ExpiresAt is the moment the card stops paying, which has to be ahead;
	// omitted, the installation's validity decides it (ADR 0214).
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// disableGiftCardRequest is the body that closes a card.
type disableGiftCardRequest struct {
	// Reason is why the card is closed; required.
	Reason string `json:"reason"`
}

// giftCardDTO is a card as the admin reads it.
type giftCardDTO struct {
	ID string `json:"id"`
	// Code is the card's code, on the issue's answer ONLY: the shop keeps a
	// digest of it and cannot show it again.
	Code string `json:"code,omitempty"`
	// CodeTail is the code's last four characters, to tell cards apart.
	CodeTail     string `json:"code_tail"`
	CurrencyCode string `json:"currency_code"`
	Balance      int64  `json:"balance"`
	Reason       string `json:"reason"`
	// Source is "issued" by an operator or "sold" on an order (ADR 0210).
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
	// CodeChangedAt is when the card's code was last replaced.
	CodeChangedAt *time.Time `json:"code_changed_at,omitempty"`
	// DisabledAt is when an operator closed the card, and DisableReason why
	// (ADR 0213).
	DisabledAt    *time.Time `json:"disabled_at,omitempty"`
	DisableReason string     `json:"disable_reason,omitempty"`
	// ExpiresAt is when the card stops paying; absent if never (ADR 0214).
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// giftCardEntryDTO is a row of a card's history. Amount is SIGNED, so the rows
// sum to the balance.
type giftCardEntryDTO struct {
	ID         string    `json:"id"`
	GiftCardID string    `json:"gift_card_id"`
	Amount     int64     `json:"amount"`
	Kind       string    `json:"kind"`
	Reference  string    `json:"reference,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// issueGiftCard issues a card (POST /admin/v1/gift-cards).
func (h *Handler) issueGiftCard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body issueGiftCardRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	issued, err := h.svc.IssueGiftCard(ctx, service.IssueGiftCardInput{
		CurrencyCode: body.CurrencyCode, Amount: body.Amount, Reason: body.Reason, ExpiresAt: body.ExpiresAt,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	out := toGiftCardDTO(service.GiftCardWithBalance{Card: issued.Card, Balance: issued.Balance})
	out.Code = issued.Code
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: out})
}

// getGiftCard reads a card (GET /admin/v1/gift-cards/{id}).
func (h *Handler) getGiftCard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	card, err := h.svc.GetGiftCard(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toGiftCardDTO(card)})
}

// listGiftCards pages the cards (GET /admin/v1/gift-cards).
func (h *Handler) listGiftCards(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	cards, total, err := h.svc.ListGiftCards(ctx, page)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	out := make([]giftCardDTO, 0, len(cards))
	for i := range cards {
		out = append(out, toGiftCardDTO(cards[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{Data: out, Count: total, Offset: page.Offset, Limit: page.Limit})
}

// listGiftCardEntries pages a card's history (GET /admin/v1/gift-cards/{id}/entries).
func (h *Handler) listGiftCardEntries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	entries, total, err := h.svc.ListGiftCardEntries(ctx, chi.URLParam(r, "id"), page)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	out := make([]giftCardEntryDTO, 0, len(entries))
	for _, entry := range entries {
		out = append(out, giftCardEntryDTO{
			ID: entry.ID, GiftCardID: entry.GiftCardID, Amount: entry.Amount,
			Kind: entry.Kind.String(), Reference: entry.Reference, CreatedAt: entry.CreatedAt,
		})
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{Data: out, Count: total, Offset: page.Offset, Limit: page.Limit})
}

// replaceGiftCardCode gives a card a new code (POST /admin/v1/gift-cards/{id}/code).
func (h *Handler) replaceGiftCardCode(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	replaced, err := h.svc.ReplaceGiftCardCode(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	out := toGiftCardDTO(service.GiftCardWithBalance{Card: replaced.Card, Balance: replaced.Balance})
	out.Code = replaced.Code
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: out})
}

// disableGiftCard closes a card (POST /admin/v1/gift-cards/{id}/disable).
func (h *Handler) disableGiftCard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body disableGiftCardRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	closed, err := h.svc.DisableGiftCard(ctx, chi.URLParam(r, "id"), body.Reason)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toGiftCardDTO(closed)})
}

// toGiftCardDTO turns a card into its outward shape, without its code.
func toGiftCardDTO(in service.GiftCardWithBalance) giftCardDTO {
	return giftCardDTO{
		ID: in.Card.ID, CodeTail: in.Card.CodeTail, CurrencyCode: in.Card.CurrencyCode,
		Balance: in.Balance, Reason: in.Card.Reason, Source: string(in.Card.Source),
		CreatedAt: in.Card.CreatedAt, CodeChangedAt: in.Card.CodeChangedAt,
		DisabledAt: in.Card.DisabledAt, DisableReason: in.Card.DisableReason, ExpiresAt: in.Card.ExpiresAt,
	}
}

// describeGiftCards describes the gift card endpoints.
func describeGiftCards(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathAdminGiftCards, openapi.Operation{
		Summary: "Issues a gift card.",
		Description: "The card holds the amount in one currency, and the answer carries its " +
			"code ONCE: the shop keeps only a digest of it and cannot show it again, so the " +
			"code is handed to its holder from this answer. Whoever presents the code pays " +
			"with the card, at the storefront through the gift_card provider with " +
			"{\"code\": \"...\"} as the payment's data. The reason is required. expires_at, " +
			"when given, has to be ahead; omitted, the installation's validity decides it, and " +
			"a card whose moment has come pays nothing and is closed by the expiry job (ADR 0214). " +
			amountNote,
		RequestBody: d.RequestBody(issueGiftCardRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The card, with its code", d.Item(giftCardDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminGiftCards, openapi.Operation{
		Summary:     "Pages through the gift cards, newest first.",
		Description: "Each card carries its balance, open holds subtracted, and never its code. " + amountNote,
		Parameters:  pagingParameters(),
		Responses: map[string]any{
			"200": openapi.Response("A page of cards", d.List(giftCardDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminGiftCard, openapi.Operation{
		Summary:     "Reads a gift card and what it holds.",
		Description: "The balance is the sum of the card's history, open holds subtracted. " + amountNote,
		Responses: map[string]any{
			"200": openapi.Response("The card", d.Item(giftCardDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathAdminGiftCardCode, openapi.Operation{
		Summary: "Gives a gift card a new code.",
		Description: "The old code stops opening the card; the balance and the history stay with " +
			"it, and the answer carries the new code ONCE. It is how a code that never reached its " +
			"holder is recovered — a sold card's code is mailed once and kept nowhere (ADR 0210). " +
			amountNote,
		Responses: map[string]any{
			"200": openapi.Response("The card, with its new code", d.Item(giftCardDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathAdminGiftCardDisable, openapi.Operation{
		Summary: "Closes a gift card.",
		Description: "A closed card pays nothing and takes no refund, and what it still held is " +
			"voided in the same step, so its balance is zero (ADR 0213). The reason is required. " +
			"A card a payment still holds part of is refused with 409 payment_gift_card_held; " +
			"closing a closed card changes nothing. There is no reopening. " + amountNote,
		RequestBody: d.RequestBody(disableGiftCardRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The closed card", d.Item(giftCardDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminGiftCardEntries, openapi.Operation{
		Summary: "Pages through a gift card's history, newest first.",
		Description: "The amount is SIGNED — a hold is negative, the issue and the returns are " +
			"positive — so the rows sum to the balance. " + amountNote,
		Parameters: pagingParameters(),
		Responses: map[string]any{
			"200": openapi.Response("A page of the card's history", d.List(giftCardEntryDTO{})),
		},
	})
}
