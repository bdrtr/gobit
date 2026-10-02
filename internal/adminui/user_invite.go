package adminui

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// Inviting a user (ADR 0348): the Users screen opens a user without a
// password, with the privileges ticked, and the auth module sends them an
// invitation to set their own; a user's page sends one again. Both are an
// administrator's, as the API's are.

// UserInvitationsPath opens and invites a user, and UserInvitationPath sends
// one an invitation again.
const (
	UserInvitationsPath = UsersPath + "/invitations"
	UserInvitationPath  = UserPath + "/invitation"
)

// The invitation form's fields beside the privileges ticked.
const (
	formInviteEmail     = "email"
	formInviteFirstName = "first_name"
	formInviteLastName  = "last_name"
)

// paramInvited carries into the user's page whether their invitation was
// sent.
const paramInvited = "invited"

// The invitation's outcomes as the user's page says them.
const (
	invitationSent   = "sent"
	invitationUnsent = "unsent"
)

// UserInviter is the narrow surface a user is invited through: the auth
// module's.
type UserInviter interface {
	// InviteUser opens a user without a password and sends them an
	// invitation from the operator; the id comes back whenever they were
	// opened, with the invitation's error when it could not be sent.
	InviteUser(ctx context.Context, invitedBy, email, firstName, lastName string, scopes []string) (string, error)
	// ResendInvitation sends the user a new invitation from the operator.
	ResendInvitation(ctx context.Context, userID, invitedBy string) error
}

// inviteUser opens the user typed and sends them an invitation, and goes to
// their page, which says whether it was sent; a refusal, an e-mail another
// user has included, comes back on the Users screen with what was typed
// (ADR 0348).
func (u *UI) inviteUser(w http.ResponseWriter, r *http.Request) {
	inviter, ok := u.users.(UserInviter)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Users unavailable",
			"The auth module's panel surface cannot invite a user in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	principal, _ := corehttp.PrincipalFromContext(r.Context())
	id, err := inviter.InviteUser(r.Context(), principal.ID,
		strings.TrimSpace(r.PostFormValue(formInviteEmail)), strings.TrimSpace(r.PostFormValue(formInviteFirstName)),
		strings.TrimSpace(r.PostFormValue(formInviteLastName)), r.PostForm[formScope])
	switch {
	case id != "":
		outcome := invitationSent
		if err != nil {
			corehttp.LoggerFromContext(r.Context()).ErrorContext(r.Context(),
				"the panel opened a user whose invitation was not sent", "error", err, "user_id", id)
			outcome = invitationUnsent
		}
		corehttp.WriteRedirect(r.Context(), w,
			UsersPath+"/"+url.PathEscape(id)+"?"+url.Values{paramInvited: {outcome}}.Encode())
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsForbidden(err):
		u.renderUsers(w, r, http.StatusUnprocessableEntity, messageFor(err), r.PostForm)
	default:
		u.unexpectedFailure(w, r, err, "The user could not be invited")
	}
}

// resendInvitation sends the user in the path a new invitation and returns
// to their page, which says so; a refusal comes back on the page (ADR 0348).
func (u *UI) resendInvitation(w http.ResponseWriter, r *http.Request) {
	inviter, ok := u.users.(UserInviter)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Users unavailable",
			"The auth module's panel surface cannot invite a user in this installation.")
		return
	}

	principal, _ := corehttp.PrincipalFromContext(r.Context())
	id := chi.URLParam(r, "id")
	err := inviter.ResendInvitation(r.Context(), id, principal.ID)
	switch {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w,
			UsersPath+"/"+url.PathEscape(id)+"?"+url.Values{paramInvited: {invitationSent}}.Encode())
	case errors.IsInvalid(err) || errors.IsNotFound(err):
		u.renderUser(w, r, http.StatusUnprocessableEntity, id, messageFor(err), nil)
	default:
		u.unexpectedFailure(w, r, err, "The invitation could not be sent")
	}
}
