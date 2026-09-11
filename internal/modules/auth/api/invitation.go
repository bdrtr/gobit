package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// acceptInvitationRequest is the body a newly invited colleague sends.
type acceptInvitationRequest struct {
	Token    string `json:"token"`
	Password secret `json:"password"`
}

// adminInviteUser opens an invitation for a user and has it sent
// (POST /admin/v1/users/{id}/invitations).
//
// # What it answers
//
// 204 and nothing else. The send is synchronous — the request fails if the message
// could not go out — so 202 would promise an asynchrony there is none of. The token
// is not in the response and must not be: an
// invitation handed back over the admin API would be an administrator holding a
// colleague's first-password link, which is the thing this flow exists to stop
// (ADR 0137).
func (h *Handler) adminInviteUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	principal, ok := corehttp.PrincipalFromContext(ctx)
	if !ok || principal.ID == "" {
		// The guard ring already refused an unauthenticated request, so reaching
		// here with no principal means the rings are wired wrongly rather than
		// that the caller did something. It is refused rather than recorded as an
		// invitation from nobody.
		corehttp.WriteError(ctx, w, coreerrors.Internal(CodeInviterUnknown,
			"the caller could not be identified, so the invitation has no sender to record"))

		return
	}

	if err := h.svc.InviteUser(ctx, chi.URLParam(r, "id"), principal.ID); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// adminAcceptInvitation spends an invitation and sets the first password
// (POST /admin/v1/auth/accept-invitation).
//
// # It is UNAUTHENTICATED, and that is the point
//
// The person calling it has no account to authenticate with yet; that is what they
// are calling it to get. It sits beside the login endpoint in the exemption the
// composition root passes, and like login it still goes through the audit ring and
// the rate limit.
//
// It answers 204 rather than a session: the person now has a password and an
// ordinary login to make with it, and handing back an admin token from an
// unauthenticated endpoint would be a second way to get one.
func (h *Handler) adminAcceptInvitation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req acceptInvitationRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	if err := h.svc.AcceptInvitation(ctx, req.Token, string(req.Password)); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// CodeInviterUnknown reports an invitation whose sender could not be identified.
const CodeInviterUnknown = "auth_inviter_unknown"
