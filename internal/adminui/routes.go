package adminui

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// Routes binds the panel's paths to the router.
//
// Paths are registered in FULL; no prefix is mounted. The rule matches the
// modules' and rests on the same reason: whoever mounts first owns that whole
// subtree and collides with anyone else using the same prefix.
//
// # The guard is NOT installed here
//
// The panel's identity ring goes into the guard stack in the composition root,
// not in this method. The router refuses, with a panic, to accept middleware
// after routes are registered, and the health endpoints are registered while
// the router is built — so installing it from here is IMPOSSIBLE. The split is
// written down in ADR 0011.
func (u *UI) Routes(r chi.Router) {
	// Every panel route goes inside ONE group so the policy is installed once
	// (ADR 0155). chi's Group inlines the parent's middleware and starts its own
	// stack, which is what makes Use legal here even though the router already
	// carries routes — the restriction in the note above is about the PARENT.
	r.Group(u.routes)
}

// routes binds the panel's paths onto the group that carries the policy.
//
// # Why the privilege is NOT a group middleware
//
// The content policy is installed once on the group above, and the privilege
// cannot be: it differs per screen, and a middleware on the group runs BEFORE chi
// resolves the pattern, so it would have nothing to look the screen's scope up
// by. The path is therefore named twice on each line — once to bind and once to
// ask the table — which is a line a reader can get wrong. Two walks of the router
// in internal/app are what catch it: one proves every route refuses an operator
// with no privilege, the other that each route demands the privilege its OWN path
// is listed under.
func (u *UI) routes(r chi.Router) {
	r.Use(withSecurityHeaders)

	r.Get(StylesheetPath, u.needs(StylesheetPath, u.serveStylesheet))
	r.Get(ReviewsScriptPath, u.needs(ReviewsScriptPath, u.serveReviewsScript))
	r.Get(ReviewsPath, u.needs(ReviewsPath, u.showReviews))
	r.Get(LoginPath, u.needs(LoginPath, u.showLogin))
	r.Post(LoginPath, u.needs(LoginPath, u.submitLogin))
	r.Post(LogoutPath, u.needs(LogoutPath, u.submitLogout))
	r.Get(URLPrefix, u.needs(URLPrefix, u.home))
	r.Get(ProductsPath, u.needs(ProductsPath, u.listProducts))
	r.Get(ProductPath, u.needs(ProductPath, u.showProduct))
	r.Get(ProductEditPath, u.needs(ProductEditPath, u.editProduct))
	r.Post(ProductEditPath, u.needs(ProductEditPath, u.submitProductEdit))
	r.Get(VariantPath, u.needs(VariantPath, u.showVariant))
	r.Post(VariantPricePath, u.needs(VariantPricePath, u.submitVariantPrice))
	r.Post(VariantStockPath, u.needs(VariantStockPath, u.submitVariantStock))
	r.Get(OrdersPath, u.needs(OrdersPath, u.listOrders))
	r.Get(OrderPath, u.needs(OrderPath, u.showOrder))
	r.Get(SalesPath, u.needs(SalesPath, u.listSales))
	r.Get(CustomersPath, u.needs(CustomersPath, u.listCustomers))
	r.Get(CustomerPath, u.needs(CustomerPath, u.showCustomer))
	r.Get(InventoryPath, u.needs(InventoryPath, u.listInventory))

	// The registered screens come LAST, after every path the panel ships, so a
	// plugin cannot shadow one by registration order — and it could not anyway:
	// a collision is refused before the panel is built (see [validatePages]).
	u.pageRoutes(r)
}

// home sends the operator to the first screen they can open.
//
// # Why not a fixed redirect
//
// It used to go straight to the catalog, and that was correct while every signed
// in operator could open every screen. Once a screen names a privilege, a fixed
// target means an operator granted only the orders is answered 403 by the panel's
// own front door — after a successful sign-in, because the login's fallback
// target is this path.
//
// # Why not a dashboard
//
// The reason has not changed: a dashboard would need numbers, every number is a
// read the panel does not yet make, and a page of empty boxes suggests the data
// is missing rather than that the screen was never written.
//
// # When NOTHING is open
//
// 403 with the panel's page. An operator whose grants open no screen has a
// misconfigured account, and saying so once at the door is kinder than an empty
// menu that looks like an outage.
func (u *UI) home(w http.ResponseWriter, r *http.Request) {
	principal, _ := corehttp.PrincipalFromContext(r.Context())

	items := allowedItems(u.menu(), u.scopes, principal)
	if len(items) == 0 {
		u.errorPage(w, r, http.StatusForbidden, "Not permitted",
			"This account carries no privilege that opens a panel screen. An "+
				"administrator can grant one.")

		return
	}

	corehttp.WriteRedirect(r.Context(), w, items[0].Path)
}

// menu is the panel's sections followed by the screens plugins registered.
//
// It is the same concatenation the layout draws, and it exists as a method so
// that [UI.home] and the frame cannot disagree about what the menu contains —
// the door would otherwise send an operator to a screen the menu does not list,
// or refuse them one it does.
func (u *UI) menu() []navItem {
	return append(sections(), navItemsOf(u.pages)...)
}
