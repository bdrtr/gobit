package adminui

import (
	"net/http"

	"github.com/go-chi/chi/v5"
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
}

// home is the panel's protected entry point.
//
// TODAY it is a placeholder: catalog screens arrive in the next round. It is
// protected nonetheless — an unidentified request never reaches it, the guard
// ring returns the login page with a 401.
func (u *UI) home(w http.ResponseWriter, r *http.Request) {
	u.errorPage(w, r, http.StatusOK, "Admin",
		"The panel is under construction. Catalog screens arrive next round.")
}
