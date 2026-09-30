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
	// The group remains, and what it carries has changed. The content policy used
	// to be installed here (ADR 0155) and now sits in the composition root's
	// guard stack, scoped to the panel's PREFIX: a route bound on this router
	// after this group — which is what a plugin's own registration did — is
	// outside the group and was answering with no policy at all (ADR 0157, D97).
	//
	// chi's Group inlines the parent's middleware and starts its own stack, which
	// is what makes Use legal here even though the router already carries routes;
	// the restriction in the note above is about the PARENT.
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
	r.Get(StylesheetPath, u.needs(http.MethodGet, StylesheetPath, u.serveStylesheet))
	r.Get(ReviewsScriptPath, u.needs(http.MethodGet, ReviewsScriptPath, u.serveReviewsScript))
	r.Get(ReviewsPath, u.needs(http.MethodGet, ReviewsPath, u.showReviews))
	r.Get(LoginPath, u.needs(http.MethodGet, LoginPath, u.showLogin))
	r.Post(LoginPath, u.needs(http.MethodPost, LoginPath, u.submitLogin))
	r.Post(LogoutPath, u.needs(http.MethodPost, LogoutPath, u.submitLogout))
	r.Get(URLPrefix, u.needs(http.MethodGet, URLPrefix, u.home))
	r.Get(ProductsPath, u.needs(http.MethodGet, ProductsPath, u.listProducts))
	r.Get(ProductPath, u.needs(http.MethodGet, ProductPath, u.showProduct))
	r.Get(ProductEditPath, u.needs(http.MethodGet, ProductEditPath, u.editProduct))
	r.Post(ProductEditPath, u.needs(http.MethodPost, ProductEditPath, u.submitProductEdit))
	r.Get(ProductRelationsPath, u.needs(http.MethodGet, ProductRelationsPath, u.editRelations))
	r.Post(ProductRelationsPath, u.needs(http.MethodPost, ProductRelationsPath, u.submitRelations))
	r.Get(ProductAddOnsPath, u.needs(http.MethodGet, ProductAddOnsPath, u.editAddOns))
	r.Post(ProductAddOnsPath, u.needs(http.MethodPost, ProductAddOnsPath, u.submitAddOns))
	r.Get(VariantPath, u.needs(http.MethodGet, VariantPath, u.showVariant))
	r.Post(VariantPricePath, u.needs(http.MethodPost, VariantPricePath, u.submitVariantPrice))
	r.Post(VariantStockPath, u.needs(http.MethodPost, VariantStockPath, u.submitVariantStock))
	r.Get(VariantBundlePath, u.needs(http.MethodGet, VariantBundlePath, u.editBundle))
	r.Post(VariantBundlePath, u.needs(http.MethodPost, VariantBundlePath, u.submitBundle))
	r.Get(OrdersPath, u.needs(http.MethodGet, OrdersPath, u.listOrders))
	r.Get(OrderPath, u.needs(http.MethodGet, OrderPath, u.showOrder))
	r.Get(SalesPath, u.needs(http.MethodGet, SalesPath, u.listSales))
	r.Get(CustomersPath, u.needs(http.MethodGet, CustomersPath, u.listCustomers))
	r.Get(CustomerPath, u.needs(http.MethodGet, CustomerPath, u.showCustomer))
	r.Get(InventoryPath, u.needs(http.MethodGet, InventoryPath, u.listInventory))
	r.Get(SecondFactorPath, u.needs(http.MethodGet, SecondFactorPath, u.showSecondFactor))
	r.Post(SecondFactorEnrollPath, u.needs(http.MethodPost, SecondFactorEnrollPath, u.submitSecondFactorEnroll))
	r.Post(SecondFactorConfirmPath, u.needs(http.MethodPost, SecondFactorConfirmPath, u.submitSecondFactorConfirm))
	r.Post(SecondFactorRemovePath, u.needs(http.MethodPost, SecondFactorRemovePath, u.submitSecondFactorRemove))

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
// # When no PRIVILEGE opens a screen
//
// The person's own second factor is open to everybody (ADR 0266), so the door
// sends them there: it is the one thing an account holding no privilege can
// still do, it is what an account the installation requires a factor of has to
// do (ADR 0265), and the screen says that no other screen opens. A privileged
// screen is always preferred, so an operator granted anything lands on it.
//
// 403 is left for a menu with nothing in it at all.
func (u *UI) home(w http.ResponseWriter, r *http.Request) {
	principal, _ := corehttp.PrincipalFromContext(r.Context())

	items := allowedItems(u.menu(), u.scopes, principal)
	for _, item := range items {
		if u.scopes[routeKey(http.MethodGet, item.Path)] != "" {
			corehttp.WriteRedirect(r.Context(), w, item.Path)
			return
		}
	}
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
