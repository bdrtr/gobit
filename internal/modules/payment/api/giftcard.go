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
)

// issueGiftCardRequest is the body that issues a card.
type issueGiftCardRequest struct {
	// CurrencyCode is the one currency the card holds; required.
	CurrencyCode string `json:"currency_code"`
	// Amount is the balance the card is issued with (minor unit); positive.
	Amount int64 `json:"amount"`
	// Reason is why the card is issued; required.
	Reason string `json:"reason"`
}

// giftCardDTO is a card as the admin reads it.
type giftCardDTO struct {
	ID string `json:"id"`
	// Code is the card's code, on the issue's answer ONLY: the shop keeps a
	// digest of it and cannot show it again.
	Code string `json:"code,omitempty"`
	// CodeTail is the code's last four characters, to tell cards apart.
	CodeTail     string    `json:"code_tail"`
	CurrencyCode string    `json:"currency_code"`
	Balance      int64     `json:"balance"`
	Reason       string    `json:"reason"`
	CreatedAt    time.Time `json:"created_at"`
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
		CurrencyCode: body.CurrencyCode, Amount: body.Amount, Reason: body.Reason,
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

// toGiftCardDTO turns a card into its outward shape, without its code.
func toGiftCardDTO(in service.GiftCardWithBalance) giftCardDTO {
	return giftCardDTO{
		ID: in.Card.ID, CodeTail: in.Card.CodeTail, CurrencyCode: in.Card.CurrencyCode,
		Balance: in.Balance, Reason: in.Card.Reason, CreatedAt: in.Card.CreatedAt,
	}
}

// describeGiftCards describes the four gift card endpoints.
func describeGiftCards(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathAdminGiftCards, openapi.Operation{
		Summary: "Issues a gift card.",
		Description: "The card holds the amount in one currency, and the answer carries its " +
			"code ONCE: the shop keeps only a digest of it and cannot show it again, so the " +
			"code is handed to its holder from this answer. Whoever presents the code pays " +
			"with the card, at the storefront through the gift_card provider with " +
			"{\"code\": \"...\"} as the payment's data. The reason is required. " + amountNote,
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
