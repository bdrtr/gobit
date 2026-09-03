package adminui

import (
	"net/http"
	"sync/atomic"

	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// Ring makes the panel's guard middleware BINDABLE LATER.
//
// # Why it is needed
//
// Middleware must be installed while the router is built — the router refuses,
// with a panic, to accept middleware after routes are registered. The panel, on
// the other hand, resolves the identity service and the read layer from the
// container, and those are born DURING module bootstrap. The two moments are
// not the same.
//
// This type bridges the gap and is the same pattern the framework already
// established for its authenticator: installed while the router is built,
// filled in via [Ring.Bind] once the panel is ready.
//
// # A request that arrives before binding
//
// It is REJECTED (ADR 0007's identity line). An unprotected admin surface must
// fail loudly closed rather than stay quietly open. A setup that forgets to bind
// sees the panel never open on the first request — it does not keep running with
// an open panel.
//
// Safe for concurrent use: binding happens once, reading on every request.
type Ring struct {
	// value always holds a panelHolder; atomic.Value wants a single concrete
	// type, and storing the pointer directly would make the nil case ambiguous
	// on read.
	value atomic.Value
}

// panelHolder wraps the panel in a single concrete type for atomic.Value.
type panelHolder struct {
	inner *UI
}

// Bind installs the real panel.
func (r *Ring) Bind(u *UI) {
	r.value.Store(panelHolder{inner: u})
}

// panel returns the bound panel, or an error when unbound.
func (r *Ring) panel() (*UI, error) {
	holder, ok := r.value.Load().(panelHolder)
	if !ok || holder.inner == nil {
		return nil, errors.Unauthorized(CodeNotBound, "admin panel is not bound yet")
	}
	return holder.inner, nil
}

// Protect is the panel tree's identity ring; see [UI.Protect] for the rationale.
func (r *Ring) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		panel, err := r.panel()
		if err != nil {
			corehttp.WriteError(req.Context(), w, err)
			return
		}
		panel.Protect(next).ServeHTTP(w, req)
	})
}

// CheckOrigin is CSRF's second layer; see [UI.CheckOrigin] for the rationale.
func (r *Ring) CheckOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		panel, err := r.panel()
		if err != nil {
			corehttp.WriteError(req.Context(), w, err)
			return
		}
		panel.CheckOrigin(next).ServeHTTP(w, req)
	})
}
