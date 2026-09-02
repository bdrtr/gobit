package adminui

import (
	"net/http"
	"net/url"
	"slices"
	"strings"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// ExemptPaths lists the full panel paths that do NOT require identity.
//
// The list is short, and its staying short is the real claim: the login page is
// about to establish identity, and so is its submission. No other panel path
// opens without one.
func ExemptPaths() []string {
	return []string{LoginPath}
}

// Protect is the panel tree's identity ring.
//
// # It does NOT touch core
//
// It reads the cookie, asks the SAME authenticator with the "Bearer" scheme and
// puts the result into the context. The framework's header-reading code and its
// admin guard are UNCHANGED; the panel merely takes identity from a different
// carrier inside its own tree. Identity RESOLUTION still happens in one place —
// the panel does not write its own token verification.
//
// # An unidentified request gets the login page with a 401
//
// There is NO redirect. The HTML writer is not bound by the 2xx rule, so the
// page can carry the honest status code; a redirect would erase the failure
// from the status code and risks a redirect loop.
//
// The ring is installed in the composition root, not in the panel's route
// registration: the router refuses (with a panic) to accept middleware after
// routes are registered.
func (u *UI) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := readCookie(r)
		if token == "" {
			u.loginPage(w, r, http.StatusUnauthorized, "")
			return
		}

		principal, err := u.authenticator.AuthenticateAdmin(r.Context(), "Bearer", token)
		if err != nil {
			// The cookie exists but is invalid: expired, or the secret changed.
			// It must be dropped, otherwise the browser keeps sending the same
			// dead token and the login page returns 401 forever.
			clearCookie(w, u.secureCookie)
			u.loginPage(w, r, http.StatusUnauthorized, "Your session has expired. Please sign in again.")
			return
		}

		next.ServeHTTP(w, r.WithContext(corehttp.WithPrincipal(r.Context(), principal)))
	})
}

// stateChangingMethods are the methods a browser can be made to trigger from
// another site.
var stateChangingMethods = []string{
	http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
}

// CheckOrigin is CSRF's SECOND layer.
//
// # Why SameSite is not enough on its own
//
// The cookie carries SameSite=Strict, which keeps it off cross-site requests
// and closes most of CSRF. What stays open is the SUBDOMAIN case: SameSite
// treats "site" at the registrable-domain level, so a compromised subdomain
// counts as the same site and can send the cookie.
//
// The Origin header closes that gap and is STATELESS: it needs no token to be
// generated, stored and printed into templates. A double-submit token was
// deliberately not chosen for this round — it would do the same job with more
// moving parts.
//
// # What happens when the header is ABSENT
//
// The request is REJECTED. Browsers send Origin on state-changing requests; a
// client that does not is not a browser, and the panel's only client is a
// browser. Letting a missing header through would make the check optional for
// exactly the attacker who omits it.
func (u *UI) CheckOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !slices.Contains(stateChangingMethods, r.Method) {
			next.ServeHTTP(w, r)
			return
		}

		if !sameOrigin(r) {
			u.errorPage(w, r, http.StatusForbidden, "Request rejected",
				"This request appears to come from another site.")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// sameOrigin reports whether the request came from the panel's own origin.
//
// The comparison is against the Host header: the panel does not know its own
// public address, and adding a configuration field for it would hand the
// operator one more setting that shuts the panel down entirely when mistyped.
// Behind a reverse proxy, Host is the value the proxy forwards — the same value
// the browser puts in Origin.
func sameOrigin(r *http.Request) bool {
	raw := r.Header.Get("Origin")
	if raw == "" {
		return false
	}

	origin, err := url.Parse(raw)
	if err != nil || origin.Host == "" {
		return false
	}

	return strings.EqualFold(origin.Host, r.Host)
}
