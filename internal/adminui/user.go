package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// A user's page (ADR 0347): who they are, whether they have proven an
// authenticator, and the privileges they hold, changed from the ones the
// page was drawn with by an operator who holds admin, the privilege the
// auth module's API writes privileges under.

// UserPath shows one user, and UserScopesPath changes their privileges.
const (
	UserPath       = UsersPath + "/{id}"
	UserScopesPath = UserPath + "/scopes"
)

// scopeAdmin is the auth module's write privilege: what is written there is
// privilege itself, so the admin API asks for admin rather than a narrower
// one (its ScopeWrite).
const scopeAdmin = "admin"

// The privileges form's fields: each privilege the page was drawn with, and
// each one ticked.
const (
	formReadScope = "read_scope"
	formScope     = "scope"
)

// UserReader is the narrow surface a user is read through: the auth
// module's.
type UserReader interface {
	// UserJSON returns the user as the Users screen lists them.
	UserJSON(ctx context.Context, id string) (json.RawMessage, error)
}

// ScopeReviser is the narrow surface a user's privileges are changed
// through.
type ScopeReviser interface {
	// ReviseUserScopes writes the user's privileges from the ones read, and
	// refuses when another writer changed them since.
	ReviseUserScopes(ctx context.Context, id string, read, next []string) error
}

// privilegeChoice is one privilege the form offers, ticked or not.
type privilegeChoice struct {
	Scope   string
	Checked bool
}

// privilegeChoices are the privileges the form offers: every privilege a
// panel screen asks for, admin among them since this form asks for it, and
// any other the user holds, in order, each ticked when it is among those
// given.
func privilegeChoices(held, ticked []string) []privilegeChoice {
	scopes := make([]string, 0, len(held))
	for _, scope := range builtInScopes() {
		scopes = append(scopes, scope)
	}
	scopes = append(scopes, held...)
	slices.Sort(scopes)
	scopes = slices.Compact(scopes)

	choices := make([]privilegeChoice, 0, len(scopes))
	for _, scope := range scopes {
		if scope != "" {
			choices = append(choices, privilegeChoice{Scope: scope, Checked: slices.Contains(ticked, scope)})
		}
	}

	return choices
}

// showUser renders the user in the path.
func (u *UI) showUser(w http.ResponseWriter, r *http.Request) {
	u.renderUser(w, r, http.StatusOK, chi.URLParam(r, "id"), "", nil)
}

// reviseUserScopes writes the privileges ticked for the user in the path
// from the ones the page was drawn with and returns to the page, which says
// so; a refusal, privileges changed since or the last administrator's admin
// included, comes back on the page with what was ticked (ADR 0347).
func (u *UI) reviseUserScopes(w http.ResponseWriter, r *http.Request) {
	reviser, ok := u.users.(ScopeReviser)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Users unavailable",
			"The auth module's panel surface cannot change a user's privileges in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	id, next := chi.URLParam(r, "id"), r.PostForm[formScope]
	err := reviser.ReviseUserScopes(r.Context(), id, r.PostForm[formReadScope], next)
	switch {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w, UsersPath+"/"+url.PathEscape(id)+"?"+url.Values{paramWritten: {"1"}}.Encode())
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err) || errors.IsForbidden(err):
		u.renderUser(w, r, http.StatusUnprocessableEntity, id, messageFor(err), next)
	default:
		u.unexpectedFailure(w, r, err, "The privileges could not be changed")
	}
}

// renderUser draws the user with a refused change's reason and what was
// ticked in it. An operator who may change privileges holds admin, and with
// it the privilege to read the user.
func (u *UI) renderUser(w http.ResponseWriter, r *http.Request, code int, id, refused string, ticked []string) {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	reader, ok := u.users.(UserReader)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Users unavailable",
			"The auth module's panel surface cannot show a user in this installation.")
		return
	}

	raw, err := reader.UserJSON(r.Context(), id)
	var user userRow
	if err == nil {
		err = json.Unmarshal(raw, &user)
	}
	switch {
	case errors.IsNotFound(err):
		u.errorPage(w, r, http.StatusNotFound, "Not found", "There is no user "+id+".")
		return
	case err != nil:
		u.unexpectedFailure(w, r, err, "The user could not be read")
		return
	}

	if ticked == nil {
		ticked = user.Scopes
	}
	_, canRevise := u.users.(ScopeReviser)
	_, canInvite := u.users.(UserInviter)
	data := map[string]any{
		"Invited":    r.URL.Query().Get(paramInvited),
		"CanInvite":  canInvite && principal.HasScope(scopeAdmin),
		titleKey:     user.Email,
		"User":       user,
		"Privileges": privilegeChoices(user.Scopes, ticked),
		canReviseKey: canRevise && principal.HasScope(scopeAdmin),
		writtenKey:   r.URL.Query().Get(paramWritten),
		refusedKey:   refused,
		pathKey:      UsersPath,
	}

	u.templates.render(w, r, code, "user.gohtml", data)
}
