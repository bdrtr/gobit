package adminui

import (
	"context"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// Removing a user (ADR 0349): a user's page deletes them and their login
// identities for an operator holding admin, the last administrator kept
// (ADR 0346); an operator's own page offers no removal, and one sent for
// them is refused.

// UserRemovePath removes the user.
const UserRemovePath = UserPath + "/remove"

// paramRemoved carries into the Users screen that a user was removed.
const paramRemoved = "removed"

// codeRemoveSelf refuses an operator removing themselves here.
const codeRemoveSelf = "panel_remove_self"

// UserRemover is the narrow surface a user is removed through: the auth
// module's.
type UserRemover interface {
	// RemoveUser deletes the user, refusing the last administrator.
	RemoveUser(ctx context.Context, id string) error
}

// removeUser deletes the user in the path and goes to the Users screen,
// which says so; a refusal, the last administrator or the operator
// themselves included, comes back on the user's page (ADR 0349).
func (u *UI) removeUser(w http.ResponseWriter, r *http.Request) {
	remover, ok := u.users.(UserRemover)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Users unavailable",
			"The auth module's panel surface cannot remove a user in this installation.")
		return
	}

	principal, _ := corehttp.PrincipalFromContext(r.Context())
	id := chi.URLParam(r, "id")
	var err error = errors.Conflict(codeRemoveSelf,
		"you cannot remove yourself here; another administrator can, or the API")
	if id != principal.ID {
		err = remover.RemoveUser(r.Context(), id)
	}
	switch {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w, UsersPath+"?"+url.Values{paramRemoved: {"1"}}.Encode())
	case errors.IsConflict(err) || errors.IsInvalid(err):
		u.renderUser(w, r, http.StatusUnprocessableEntity, id, messageFor(err), nil)
	case errors.IsNotFound(err):
		u.errorPage(w, r, http.StatusNotFound, "Not found", "There is no user "+id+".")
	default:
		u.unexpectedFailure(w, r, err, "The user could not be removed")
	}
}
