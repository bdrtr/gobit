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

// MarkerName is the name of the cookie that remembers a session EXISTED.
//
// # It carries no credential, and that is the whole point
//
// Its value is a constant. It authenticates nothing, it is never read by the
// guard to decide access, and stealing it wins an attacker the knowledge that
// somebody once signed in — which the login page itself already implies.
//
// # The defect it repairs, which ADR 0031 measured and accepted
//
// The session cookie's lifetime is deliberately tied to the token's expiry, so
// a NATURALLY expired session sends no cookie at all. [UI.Protect] then takes
// its empty-token branch, which is the same branch a first-time visitor takes,
// and the honest thing to print there is nothing. The sentence an operator
// most needs — "your session has expired" — sat on the branch reached when a
// cookie exists but its token does not, and the cookie design makes that branch
// unreachable in the ordinary case.
//
// A marker that OUTLIVES the token separates the two states without weakening
// either: the credential still dies on the deadline, and the panel can still
// tell "you were here" from "you have never been here".
//
// # Why not simply let the session cookie outlive its token
//
// Because that is the "logged in but nothing opens" failure [writeCookie]
// names: the browser would keep sending a dead token, every request would fail
// in the guard, and the user would believe they were signed in. Splitting the
// two cookies keeps the credential's lifetime honest and gives the MESSAGE its
// own, longer one.
const MarkerName = "gobit_admin_seen"

// markerValue is the only value the marker ever carries.
const markerValue = "1"

// markerGrace is how long the marker outlives the token it explains.
//
// It is bounded rather than permanent because the marker's job is to explain a
// lapse that JUST happened. A week is long enough that an operator who signs in
// on Monday and returns after a holiday weekend still gets the sentence, and
// short enough that the panel does not tell somebody who last used it in another
// season that their session "has expired".
const markerGrace = 7 * 24 * time.Hour

// writeMarker records that a session existed.
//
// Path, HttpOnly, Secure and SameSite are copied from [writeCookie] on purpose.
// HttpOnly is not protecting a secret here — there is no secret — it is keeping
// the two cookies from drifting into two different sets of flags, which is how
// [clearCookie]'s godoc says a cookie survives the deletion meant for it.
func writeMarker(w http.ResponseWriter, expiresAt time.Time, secure bool) {
	//nolint:gosec // G124: see writeCookie; Secure follows the same decision.
	http.SetCookie(w, &http.Cookie{
		Name:     MarkerName,
		Value:    markerValue,
		Path:     URLPrefix,
		Expires:  expiresAt.Add(markerGrace),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// clearMarker drops the marker.
//
// It is called in two places and they mean different things. A deliberate sign
// out clears it because "you signed out" is not "your session expired", and
// printing the second after the first would be a lie the panel tells itself.
// The guard clears it after PRINTING the sentence, because the message is about
// a transition: a reload of the login page should show the form, not repeat an
// expiry that has already been explained.
func clearMarker(w http.ResponseWriter, secure bool) {
	//nolint:gosec // G124: see writeCookie.
	http.SetCookie(w, &http.Cookie{
		Name:     MarkerName,
		Value:    "",
		Path:     URLPrefix,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// hasMarker reports whether a session existed on this browser.
//
// The VALUE is compared and not merely the presence of the cookie: a cookie
// whose value is empty is what a browser sends for a cookie somebody deleted
// badly, and treating that as "a session existed" would print the expiry
// sentence at a moment nothing expired.
func hasMarker(r *http.Request) bool {
	cookie, err := r.Cookie(MarkerName)
	if err != nil {
		return false
	}
	return cookie.Value == markerValue
}
