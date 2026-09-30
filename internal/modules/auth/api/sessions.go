package api

import (
	"net/http"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// CodeSessionsNotAPerson is a session asked of an API key: a key is a machine,
// it signs in to nothing and has no session to list or close.
const CodeSessionsNotAPerson = "auth_sessions_not_a_person"

// The caller's own sessions (ADR 0267).
const (
	// SessionsPath lists the caller's open sessions.
	SessionsPath = "/admin/v1/auth/sessions"
	// SessionRevokePath closes one of them.
	SessionRevokePath = "/admin/v1/auth/sessions/{id}/revoke"
	// SessionsRevokeOthersPath closes all but the one the request is made with.
	SessionsRevokeOthersPath = "/admin/v1/auth/sessions/revoke-others"
)

// sessionDTO is one of the caller's open sessions.
type sessionDTO struct {
	// ID names the session; a session token carries it.
	ID string `json:"id"`
	// CreatedAt is when the sign-in opened it.
	CreatedAt time.Time `json:"created_at"`
	// ExpiresAt is when its token stops being accepted.
	ExpiresAt time.Time `json:"expires_at"`
	// Current reports that this is the session the request was made with.
	Current bool `json:"current"`
	// UserAgent is what the browser said it was at the sign-in; empty for a
	// session opened before it was kept (ADR 0276).
	UserAgent string `json:"user_agent"`
}

// revokedSessionsDTO says how many sessions a revocation closed.
type revokedSessionsDTO struct {
	// Revoked is the number of sessions closed.
	Revoked int64 `json:"revoked"`
}

// adminListSessions lists the caller's open sessions
// (GET /admin/v1/auth/sessions).
//
// Like the second factor, it is about the caller and names no user: an
// endpoint that took an id would let one administrator read when another signs
// in. A key is a machine with no sessions, and is refused.
func (h *Handler) adminListSessions(w http.ResponseWriter, r *http.Request) {
	principal, ok := sessionHolder(w, r)
	if !ok {
		return
	}

	sessions, err := h.svc.ListSessions(r.Context(), principal)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	out := make([]sessionDTO, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, toSessionDTO(session))
	}
	writeItems(w, r, out)
}

// adminRevokeSession closes one of the caller's sessions
// (POST /admin/v1/auth/sessions/{id}/revoke), the current one included.
func (h *Handler) adminRevokeSession(w http.ResponseWriter, r *http.Request) {
	principal, ok := sessionHolder(w, r)
	if !ok {
		return
	}

	if err := h.svc.CloseSession(r.Context(), principal.ID, pathParam(r, paramID)); err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	corehttp.WriteJSON(r.Context(), w, http.StatusNoContent, nil)
}

// adminRevokeOtherSessions closes every session of the caller but the one the
// request is made with (POST /admin/v1/auth/sessions/revoke-others): the
// answer to a laptop left signed in somewhere, from the phone in hand.
func (h *Handler) adminRevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	principal, ok := sessionHolder(w, r)
	if !ok {
		return
	}

	revoked, err := h.svc.CloseOtherSessions(r.Context(), principal)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, revokedSessionsDTO{Revoked: revoked})
}

// toSessionDTO converts a session into its response body.
func toSessionDTO(s service.SessionView) sessionDTO {
	return sessionDTO{
		ID: s.ID, CreatedAt: s.CreatedAt, ExpiresAt: s.ExpiresAt, Current: s.Current, UserAgent: s.UserAgent,
	}
}

// sessionHolder is the person the request proved, or a refusal: a key has no
// sessions.
func sessionHolder(w http.ResponseWriter, r *http.Request) (corehttp.Principal, bool) {
	ctx := r.Context()

	principal, ok := corehttp.PrincipalFromContext(ctx)
	if !ok || principal.ID == "" {
		corehttp.WriteError(ctx, w, coreerrors.Internal(CodeInviterUnknown,
			"the caller could not be identified, so there are no sessions to act on"))
		return corehttp.Principal{}, false
	}
	if principal.Kind != service.PrincipalKindUser {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(CodeSessionsNotAPerson,
			"sessions belong to a person who signed in, and this request was made with an API key"))
		return corehttp.Principal{}, false
	}

	return principal, true
}
