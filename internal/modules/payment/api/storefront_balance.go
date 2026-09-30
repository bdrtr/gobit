package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// The storefront reads of a customer's own balances (ADR 0253).
//
// A shopper reads what store credit and how many points they hold, and nothing
// else: the balance and not the history, because a history row carries the
// operator's reason and reference, which are written for the shop and not for
// the customer. Whose balance is read is the path's customer, and the request
// has to PROVE it (corehttp.ProvenCustomer): with no customer identity bound
// the answer is a refusal, as it is for the address book (ADR 0043), since a
// balance has no correct anonymous reader.
const (
	pathStoreOwnStoreCredit = "/store/v1/customers/{id}/store-credit/balance"
	pathStoreOwnLoyalty     = "/store/v1/customers/{id}/loyalty-points/balance"
)

// WithIdentity gives the handler the customer identity the storefront balance
// reads ask; without one they refuse.
//
// It is a separate step rather than a parameter of [New] because only the
// storefront balances ask it, and every other test of this package builds a
// handler that never will.
func (h *Handler) WithIdentity(identity corehttp.Identity) *Handler {
	h.identity = identity

	return h
}

// ownStoreCreditBalance returns what the proven customer can spend
// (GET /store/v1/customers/{id}/store-credit/balance).
func (h *Handler) ownStoreCreditBalance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customerID, err := corehttp.ProvenCustomer(h.identity, r, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	currency := r.URL.Query().Get(paramCurrencyCode)
	balance, err := h.svc.StoreCreditBalance(ctx, customerID, currency)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: storeCreditBalanceDTO{
		CustomerID:   customerID,
		CurrencyCode: strings.ToUpper(strings.TrimSpace(currency)),
		Balance:      balance,
	}})
}

// ownLoyaltyBalance returns how many points the proven customer holds
// (GET /store/v1/customers/{id}/loyalty-points/balance).
func (h *Handler) ownLoyaltyBalance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customerID, err := corehttp.ProvenCustomer(h.identity, r, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	currency := r.URL.Query().Get(paramCurrencyCode)
	points, err := h.svc.LoyaltyBalance(ctx, customerID, currency)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: loyaltyBalanceDTO{
		CustomerID:   customerID,
		CurrencyCode: strings.ToUpper(strings.TrimSpace(currency)),
		Points:       points,
	}})
}
