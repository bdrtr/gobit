package adminui

import (
	"net/http"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// CodeForeignOrigin reports a cookie-authenticated state change whose Origin is
// not the panel's own.
const CodeForeignOrigin = "admin_ui_foreign_origin"

// authorizationHeader is the header [corehttp.RequireAdmin] reads.
//
// The panel does not call that function; it puts a credential where it will be
// found, which is why the name is spelled here rather than imported. Spelling
// it wrong would make the panel's own requests arrive unauthenticated, and the
// panel would answer 401 to a signed-in operator — so
// [TestTheAPISessionHeaderIsTheOneTheCoreReads] pins it.
const authorizationHeader = "Authorization"

// APISession lets the panel's own browser session reach `/admin/v1`.
//
// # What it is for
//
// ADR 0030 decided the panel becomes a client of the admin API. The API
// authenticates from an `Authorization` header ([corehttp.RequireAdmin]) and a
// browser cannot send one from a page it merely loaded — the token is HttpOnly
// and never enters JavaScript (ADR 0011, unchanged). So the credential is moved
// from where the browser CAN put it to where the core LOOKS for it, on the way
// in, by the one component that owns the cookie.
//
// # It never overrides a header that is already there
//
// An API client sending `Authorization` is left exactly as it was. That is not
// politeness: it is what keeps this change from touching anybody outside the
// panel, and it is also the security boundary — see below.
//
// # The origin check applies ONLY to cookie-authenticated state changes
//
// This is the whole design and it is worth being exact about, because getting
// it wrong in either direction is a defect:
//
//   - Applying the check to EVERY `/admin/v1` request would break every
//     non-browser client. `curl`, a server-to-server integration and a CI job
//     send no `Origin` header, and [sameOrigin] refuses a request without one —
//     deliberately, since the attacker is the one who omits it. Those clients
//     were never CSRF-able to begin with: nothing makes a browser attach a
//     bearer token to a cross-site request.
//   - Applying it to NONE would be the hole ADR 0030 opens and does not close.
//     Once the cookie reaches `/admin/v1`, a page on a compromised subdomain can
//     make the browser POST there with the session attached — the case
//     `SameSite=Strict` does not cover, because it treats "site" at the
//     registrable-domain level.
//
// So the rule is the credential's own: a request that becomes authenticated
// BECAUSE OF A COOKIE gets the browser-shaped defense, and a request that
// arrives with its own header does not need one.
//
// # Why the refusal is JSON here and HTML in [UI.CheckOrigin]
//
// The same check answers two audiences. Under the panel prefix the client is a
// browser following a form, and an error PAGE is what it can show; under the
// API prefix the client is the panel's own script, and an HTML body would be
// decoded as JSON and reported as a parse failure — which is the wrong sentence
// on the screen and sends whoever reads it looking in the wrong place.
func (u *UI) APISession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(authorizationHeader) != "" {
			next.ServeHTTP(w, r)

			return
		}

		cookie, err := r.Cookie(CookieName)
		if err != nil || cookie.Value == "" {
			// No cookie and no header: nothing to promote, and refusing here
			// would answer a plain unauthenticated request with a CSRF message.
			// RequireAdmin says "authentication is required", which is true.
			next.ServeHTTP(w, r)

			return
		}

		if isStateChanging(r.Method) && !sameOrigin(r) {
			corehttp.WriteError(r.Context(), w, errors.Forbidden(CodeForeignOrigin,
				"this request appears to come from another site"))

			return
		}

		// The header is set on a CLONE of the request rather than on the one
		// the caller holds: a middleware that mutated the incoming headers
		// would leave the credential visible to everything upstream of it,
		// including anything that logs them.
		promoted := r.Clone(r.Context())
		// "Bearer" with a capital, which is what a client writes; the parser
		// lower-cases the scheme on the way in ([corehttp.SchemeBearer]), so
		// either spelling authenticates and this is the one an operator would
		// recognize in a proxy log.
		promoted.Header.Set(authorizationHeader, "Bearer "+cookie.Value)

		next.ServeHTTP(w, promoted)
	})
}

// isStateChanging reports whether the method can change anything.
func isStateChanging(method string) bool {
	for _, changing := range stateChangingMethods {
		if method == changing {
			return true
		}
	}

	return false
}
