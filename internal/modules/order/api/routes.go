package api

import (
	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// Scope vocabulary: the scopes the admin endpoints of order ask for.
//
// The distinction is over READING/WRITING, not over the resource. Per-resource
// scopes such as "order_returns:write" grow the list but produce no new
// decision that could be made today: there is no need yet for an identity that
// can open a return but cannot cancel the order, and a scope name defined for a
// need that does not exist is a name whose purpose is unknown on the day it is
// first granted.
const (
	// ScopeRead is the scope the READ endpoints of the order admin surface ask
	// for.
	//
	// It suffices for reading orders and return/exchange/claim records; it
	// opens no status transition. It does not have to be granted separately to
	// fully privileged identities: a caller carrying corehttp.ScopeAdmin
	// satisfies this one as well (see corehttp.Principal.HasScope).
	ScopeRead = "order:read"

	// ScopeWrite is the scope the WRITE endpoints of the order admin surface
	// ask for.
	//
	// Status transitions (cancel, complete, archive) and opening an after-sales
	// record ask for it. Most of the transitions are IRREVERSIBLE — a canceled
	// order is not reopened — which is why separating it from the read scope is
	// not a formality, it is the limit of the damage.
	ScopeWrite = "order:write"
)

// Routes binds the module's store and admin endpoints to the router.
//
// The endpoints are registered with their FULL PATH; no sub-router
// (chi.Route/Mount) IS OPENED for "/store/v1" or "/admin/v1". The reason is
// concrete — the registry calls the Routes of every module on the SAME router,
// and chi refuses being mounted a second time on the same pattern with a panic.
// Had the first module mounted "/store/v1", the second module would have
// brought the server down at start-up.
//
// The order CREATION endpoint is deliberately absent; for the rationale see the
// package documentation.
//
// # PROTECTION
//
// The admin endpoints are protected by two layers and both of
// them are necessary:
//
//  1. IDENTITY — corehttp.RequireAdmin is attached on the side that builds the
//     router (see corehttp.APIGuards); it is not this module's job.
//  2. SCOPE — the endpoints are marked HERE, endpoint by endpoint, with
//     corehttp.RequireScope.
//
// Without the second layer, authentication would stand in for authorization: an
// admin user whose scopes had been left EMPTY could log in and cancel an order
// whose payment had been captured. Identity answers the question "who", not the
// question "what can they do".
func (h *Handler) Routes(r chi.Router) {
	read := r.With(corehttp.RequireScope(ScopeRead))
	write := r.With(corehttp.RequireScope(ScopeWrite))

	// --- Store API (customer, READ ONLY) ---
	//
	// No scope IS ADDED to the storefront surface: the identity there is the
	// publishable key and that key by definition carries no scope. Verifying
	// that the order BELONGS TO THE CUSTOMER is separate work and is still not
	// done; this is a deliberate gap, not a hidden assumption.
	r.Get("/store/v1/orders/{id}", h.storeGetOrder)

	// --- Admin API (administration) ---
	read.Get("/admin/v1/orders", h.adminListOrders)
	read.Get("/admin/v1/orders/{id}", h.adminGetOrder)
	read.Get("/admin/v1/orders/{id}/payment", h.adminGetOrderPayment)

	// Status transitions are POSTs and have no bodies (except cancel): a
	// transition is not a field update, it is an ACTION, and it must not look
	// like a PATCH applied to the resource itself.
	write.Post("/admin/v1/orders/{id}/cancel", h.adminCancelOrder)
	write.Post("/admin/v1/orders/{id}/complete", h.adminCompleteOrder)
	write.Post("/admin/v1/orders/{id}/archive", h.adminArchiveOrder)

	// After-sales records: in Phase 6 only create + read + list. Status
	// transitions (return received, exchange completed …) are the next phase's
	// work.
	read.Get("/admin/v1/orders/{id}/returns", h.adminListReturns)
	write.Post("/admin/v1/orders/{id}/returns", h.adminCreateReturn)
	read.Get("/admin/v1/orders/{id}/returns/{returnId}", h.adminGetReturn)
	read.Get("/admin/v1/orders/{id}/exchanges", h.adminListExchanges)
	write.Post("/admin/v1/orders/{id}/exchanges", h.adminCreateExchange)
	read.Get("/admin/v1/orders/{id}/exchanges/{exchangeId}", h.adminGetExchange)
	read.Get("/admin/v1/orders/{id}/claims", h.adminListClaims)
	write.Post("/admin/v1/orders/{id}/claims", h.adminCreateClaim)
	read.Get("/admin/v1/orders/{id}/claims/{claimId}", h.adminGetClaim)
}
