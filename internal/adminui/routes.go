package adminui

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
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
	r.Get(LoginPath, u.showLogin)
	r.Post(LoginPath, u.submitLogin)
	r.Post(LogoutPath, u.submitLogout)
	r.Get(URLPrefix, u.home)
	r.Get(ProductsPath, u.listProducts)
	r.Get(ProductPath, u.showProduct)
	r.Get(ProductEditPath, u.editProduct)
	r.Post(ProductEditPath, u.submitProductEdit)
}

// home is the panel's protected entry point.
//
// It redirects to the catalog rather than rendering a page of its own. A
// dashboard would need numbers, every number is a read the panel does not yet
// make, and a page of empty boxes is worse than no page: it suggests the data
// is missing rather than that the screen was never written.
func (u *UI) home(w http.ResponseWriter, r *http.Request) {
	corehttp.WriteRedirect(r.Context(), w, ProductsPath)
}
