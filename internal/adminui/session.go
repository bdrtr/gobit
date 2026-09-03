package adminui

import (
	"net/http"
	"time"
)

// CookieName is the panel session's cookie name.
const CookieName = "gobit_admin_session"

// writeCookie stores the session token in a cookie.
//
// # The cookie is valid ONLY inside the panel tree
//
// Path is pinned to the panel prefix, and this is the backbone of the design.
// The admin API's present CSRF immunity comes not from a defense but from the
// token living in a header the browser never attaches BY ITSELF. Were the
// cookie also sent to the API prefix, that immunity would vanish and EVERY
// admin endpoint would enter a new attack surface.
//
// # Flags
//
//   - HttpOnly: the token never touches JavaScript. An XSS in the panel could
//     manipulate the page but could NOT EXFILTRATE the token.
//   - SameSite=Strict: cookies are not attached to cross-site requests; this is
//     CSRF's first and cheapest defense. It is NOT sufficient alone (it does not
//     cover subdomain takeover), and the second layer is [UI.CheckOrigin].
//   - Secure: on in shared environments. Local development runs over plain HTTP,
//     so it is off there; the distinction comes from the framework's already
//     written decision about "the only environment where secrets and TLS
//     requirements are relaxed".
//
// The lifetime is tied to the TOKEN's own expiry: a cookie outliving its token
// would make every request fail in the guard while the user believes they are
// signed in — "logged in but nothing opens".
func writeCookie(w http.ResponseWriter, token string, expiresAt time.Time, secure bool) {
	//nolint:gosec // G124: Secure is a parameter, not a literal, and the linter can
	// only prove a literal. The value comes from the framework's shared-environment
	// decision; a hard-coded true would make the panel unreachable over local HTTP,
	// and the flag would then be turned off somewhere far less visible.
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     URLPrefix,
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// clearCookie drops the session cookie.
//
// It is written with the SAME path and flags: browsers match cookies by name
// and path, so a cookie deleted under a different path is NOT deleted and the
// user stays signed in while believing they signed out.
func clearCookie(w http.ResponseWriter, secure bool) {
	//nolint:gosec // G124: see writeCookie. Deletion must carry the SAME flags as
	// the write, so it inherits the same conditional Secure.
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     URLPrefix,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// readCookie returns the session token on the request, or an empty string.
func readCookie(r *http.Request) string {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}
