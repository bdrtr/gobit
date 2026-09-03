package adminui

import (
	"net/http"

	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// loginPage writes the login form with the given status code.
//
// The status is chosen BY THE CALLER: the form on its own is a 200, the same
// form rendered by the guard ring is a 401. This is possible because the HTML
// writer is not bound by the 2xx rule — and it is more honest than a redirect,
// which would erase the failure from the status code.
func (u *UI) loginPage(w http.ResponseWriter, r *http.Request, status int, message string) {
	u.templates.render(w, r, status, "login.gohtml", map[string]any{
		titleKey:    "Sign in",
		"LoginPath": LoginPath,
		errorKey:    message,
		"Email":     r.PostFormValue("email"),
	})
}

// errorPage writes the panel's human-readable error page.
func (u *UI) errorPage(w http.ResponseWriter, r *http.Request, status int, title, message string) {
	u.templates.render(w, r, status, "error.gohtml", map[string]any{
		titleKey:  title,
		"Message": message,
	})
}

// showLogin renders the login page.
func (u *UI) showLogin(w http.ResponseWriter, r *http.Request) {
	u.loginPage(w, r, http.StatusOK, "")
}

// submitLogin parses the form, verifies the credentials and writes the session
// cookie.
//
// # The form never reaches the JSON endpoint
//
// The body is parsed here and handed straight to the identity service's login
// method. The admin endpoints' strict body decoder (JSON that rejects unknown
// fields) therefore never runs, and the panel's freedom to name its form fields
// does not touch the API contract. Account lockout, the error message that does
// not reveal whether an account exists — ALL of those decisions survive, because
// the thing being called is the same service.
//
// # The message is not enriched
//
// The service says "email or password is incorrect" and does not reveal which
// accounts exist; having the panel improve on that would undo the decision.
// An unexpected failure (an unreachable database) is not leaked either: anything
// whose class is neither Unauthorized nor Invalid becomes the panel's own error
// page and the real cause goes to the log — see [UI.unexpectedFailure] for why
// the JSON envelope is wrong on a path a browser navigated to.
func (u *UI) submitLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		u.loginPage(w, r, http.StatusBadRequest, "The form could not be read.")
		return
	}

	token, expiresAt, err := u.session.Login(r.Context(), r.PostFormValue("email"), r.PostFormValue("password"))
	if err != nil {
		if errors.IsUnauthorized(err) || errors.IsInvalid(err) {
			u.loginPage(w, r, http.StatusUnauthorized, "Email or password is incorrect.")
			return
		}
		u.unexpectedFailure(w, r, err, "Sign-in unavailable")
		return
	}

	writeCookie(w, token, expiresAt, u.secureCookie)
	corehttp.WriteRedirect(r.Context(), w, URLPrefix)
}

// submitLogout ends the session.
//
// # Signing out is GLOBAL and the interface does not hide it
//
// The identity service's logout drops ALL of the caller's sessions: signing out
// on a phone also signs the desktop out. Clearing the cookie without calling the
// service would look kinder but would be a lie — the token is stateless, a
// deleted cookie does NOT invalidate it, and a copied token would keep working.
//
// The cookie is cleared even if the service call fails: leaving a dead session
// in the user's browser means leaving them signed in somewhere they believe they
// signed out of.
func (u *UI) submitLogout(w http.ResponseWriter, r *http.Request) {
	principal, ok := corehttp.PrincipalFromContext(r.Context())
	if ok {
		if _, err := u.session.Logout(r.Context(), principal.ID, principal.Kind); err != nil {
			corehttp.LoggerFromContext(r.Context()).WarnContext(r.Context(),
				"panel logout could not drop sessions",
				"error", err, "principal_id", principal.ID)
		}
	}

	clearCookie(w, u.secureCookie)
	corehttp.WriteRedirect(r.Context(), w, LoginPath)
}
