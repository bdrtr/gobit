package api

import (
	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// StoreCartsPath is the storefront's cart-creation endpoint.
//
// It is a constant because the composition root has to name it: this path is
// EXEMPT from the idempotency ring, and the reason is written where the
// exemption is declared (internal/app/setup.go). Spelling it there by hand would
// let the two drift, and the drift would restore a cross-shopper leak.
const StoreCartsPath = "/store/v1/carts"

// The scope vocabulary: the scopes cart's admin endpoints ask for.
//
// The names follow the same pattern in ALL modules ("<module>:read" /
// "<module>:write"). Every module inventing its own word would mean the person
// handing out scopes having to memorize a separate vocabulary per module; and
// the mistake made with a vocabulary nobody memorizes always falls the same
// way — too much is granted.
const (
	// ScopeRead is the scope the READ endpoints on cart's admin surface ask for.
	//
	// It is enough to list carts and to read them one by one. It does not have to
	// be granted separately to fully privileged identities: a caller carrying
	// corehttp.ScopeAdmin satisfies this one too (see corehttp.Principal.HasScope).
	ScopeRead = "cart:read"

	// ScopeWrite is the scope the WRITE endpoints on cart's admin surface ask for.
	//
	// It opens NO route today, because cart's /admin/v1 surface is read only
	// (see [Handler.Routes]). It is published all the same: because the
	// vocabulary is identical across modules, the day a write endpoint is added
	// to the admin side the scope's name will not be invented ON THAT DAY.
	// Picking the name on that day would mean the scope lists that have long
	// since been handed out silently falling short.
	ScopeWrite = "cart:write"
)

// Routes binds the module's store and admin endpoints to the router.
//
// The endpoints are registered with their FULL PATH; no sub-router
// (chi.Route/Mount) is OPENED for "/store/v1" or "/admin/v1". The reason is
// concrete: the registry calls the Routes of every module on the SAME router and
// chi rejects being mounted a second time on the same pattern with a panic. Had
// the first module mounted "/store/v1", the second module would bring the server
// down at startup.
//
// # GUARDING
//
// The admin endpoints have two layers and both of them are necessary:
//
//  1. IDENTITY — corehttp.RequireAdmin. It is attached NOT in this module but on
//     the side that builds the router (see corehttp.APIGuards).
//  2. SCOPE — HERE, endpoint by endpoint with corehttp.RequireScope. The read
//     endpoints ask for [ScopeRead].
//
// Without the second layer, authentication would stand in for authorization: an
// admin user whose scopes have been deliberately emptied is a valid identity too
// and could read every customer's cart, email addresses included, with
// GET /admin/v1/carts.
//
// No scope is ADDED to the store endpoints: the store surface's identity is the
// publishable key and that key by definition CARRIES no scope.
func (h *Handler) Routes(r chi.Router) {
	readOnly := r.With(corehttp.RequireScope(ScopeRead))

	// --- Store API (customer) ---
	r.Post(StoreCartsPath, h.storeCreateCart)
	r.Get("/store/v1/carts/{id}", h.storeGetCart)
	r.Post("/store/v1/carts/{id}", h.storeUpdateCart)
	r.Delete("/store/v1/carts/{id}", h.storeDeleteCart)

	r.Post("/store/v1/carts/{id}/line-items", h.storeAddLineItem)
	r.Patch("/store/v1/carts/{id}/line-items/{line_item_id}", h.storeUpdateLineItem)
	r.Delete("/store/v1/carts/{id}/line-items/{line_item_id}", h.storeRemoveLineItem)

	r.Put("/store/v1/carts/{id}/shipping-address", h.storeSetShippingAddress)
	r.Put("/store/v1/carts/{id}/billing-address", h.storeSetBillingAddress)

	r.Post("/store/v1/carts/{id}/shipping-methods", h.storeAddShippingMethod)
	r.Delete("/store/v1/carts/{id}/shipping-methods/{shipping_method_id}", h.storeRemoveShippingMethod)

	// The endpoint that turns the cart into an order. The owner of the cart's
	// endpoints is this module, therefore the endpoint that CLOSES the cart is
	// here as well; the composition root only sets the flow up and leaves it to
	// the container (see [Handler.storeCompleteCart]).
	r.Post("/store/v1/carts/{id}/complete", h.storeCompleteCart)

	// --- Admin API (administration, READ ONLY) ---
	readOnly.Get("/admin/v1/carts", h.adminListCarts)
	readOnly.Get("/admin/v1/carts/{id}", h.adminGetCart)
}
