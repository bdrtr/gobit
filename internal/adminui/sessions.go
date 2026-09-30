package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// A person's own sessions, in the panel (ADR 0268).
//
// Like the second factor, the screen is open to every session: closing one's
// own sessions is not a privilege, and an operator whose privileges were taken
// away must still be able to close the one a lost laptop holds.

// The sessions screen and its two forms.
const (
	// SessionsPath is the screen.
	SessionsPath = URLPrefix + "/sessions"
	// SessionsRevokePath closes the session its form names.
	SessionsRevokePath = SessionsPath + "/revoke"
	// SessionsRevokeOthersPath closes every session but the current one.
	SessionsRevokeOthersPath = SessionsPath + "/revoke-others"
)

// sessionsLabel is the screen's name in the menu.
const sessionsLabel = "Sessions"

// SessionAdmin is a person's own sessions, in primitives.
type SessionAdmin interface {
	// SessionsJSON returns the person's open sessions, newest first, with the
	// one named current marked; see [sessionRow] for the fields.
	SessionsJSON(ctx context.Context, userID, currentSessionID string) (json.RawMessage, error)
	// CloseSession closes one of the person's own sessions.
	CloseSession(ctx context.Context, userID, sessionID string) error
	// CloseOtherSessions closes every session of the person but the current one.
	CloseOtherSessions(ctx context.Context, userID, currentSessionID string) (int64, error)
}

// sessionRow is one open session as the screen prints it. The json tags are
// the contract with the auth module's surface, which this package cannot
// import; the end-to-end test in internal/app reads it through the real one.
type sessionRow struct {
	ID        string    `json:"session_id"`
	CreatedAt time.Time `json:"signed_in_at"`
	ExpiresAt time.Time `json:"ends_at"`
	Current   bool      `json:"current"`
	// Browser is what the browser said it was at the sign-in; empty for a
	// session opened before it was kept (ADR 0276).
	Browser string `json:"browser"`
}

// showSessions draws the person's open sessions.
func (u *UI) showSessions(w http.ResponseWriter, r *http.Request) {
	u.renderSessions(w, r, http.StatusOK, "")
}

// renderSessions draws the screen with a sentence when an act was refused.
func (u *UI) renderSessions(w http.ResponseWriter, r *http.Request, status int, message string) {
	principal, ok := u.sessionHolder(w, r)
	if !ok {
		return
	}

	raw, err := u.sessions.SessionsJSON(r.Context(), principal.ID, principal.SessionID)
	if err != nil {
		u.unexpectedFailure(w, r, err, "The sessions could not be read")
		return
	}
	var rows []sessionRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		u.unexpectedFailure(w, r, err, "The sessions could not be read")
		return
	}
	others := 0
	for i := range rows {
		if !rows[i].Current {
			others++
		}
	}

	u.templates.render(w, r, status, "sessions.gohtml", map[string]any{
		titleKey:           sessionsLabel,
		errorKey:           message,
		"Sessions":         rows,
		"Others":           others,
		"RevokePath":       SessionsRevokePath,
		"RevokeOthersPath": SessionsRevokeOthersPath,
	})
}

// submitSessionRevoke closes the session the form names.
func (u *UI) submitSessionRevoke(w http.ResponseWriter, r *http.Request) {
	principal, ok := u.sessionHolder(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	if err := u.sessions.CloseSession(r.Context(), principal.ID, r.PostFormValue("id")); err != nil {
		if errors.IsNotFound(err) {
			u.renderSessions(w, r, http.StatusNotFound, "That session is not open any more.")
			return
		}
		u.unexpectedFailure(w, r, err, "The session could not be closed")
		return
	}

	corehttp.WriteRedirect(r.Context(), w, SessionsPath)
}

// submitSessionsRevokeOthers closes every session but the current one.
func (u *UI) submitSessionsRevokeOthers(w http.ResponseWriter, r *http.Request) {
	principal, ok := u.sessionHolder(w, r)
	if !ok {
		return
	}

	if _, err := u.sessions.CloseOtherSessions(r.Context(), principal.ID, principal.SessionID); err != nil {
		u.unexpectedFailure(w, r, err, "The other sessions could not be closed")
		return
	}

	corehttp.WriteRedirect(r.Context(), w, SessionsPath)
}

// sessionHolder is the person the session proved, or a page saying why there
// is none.
func (u *UI) sessionHolder(w http.ResponseWriter, r *http.Request) (corehttp.Principal, bool) {
	if u.sessions == nil {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Sessions unavailable",
			"The identity module's session surface is not registered in this installation.")
		return corehttp.Principal{}, false
	}

	principal, ok := corehttp.PrincipalFromContext(r.Context())
	if !ok || principal.ID == "" {
		u.errorPage(w, r, http.StatusUnauthorized, "Not signed in", "Sign in to see your sessions.")
		return corehttp.Principal{}, false
	}

	return principal, true
}
