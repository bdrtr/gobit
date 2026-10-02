package adminui

import (
	"context"
	"net/http"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// Making an API key (ADR 0351): the API keys screen makes a secret key with
// the privileges ticked, or a publishable one attached to the sales channels
// ticked, for an operator holding admin, and shows its token once, on the
// answer to the form and nowhere else.

// The key form's fields beside the privileges ticked.
const (
	formKeyType    = "type"
	formKeyTitle   = "title"
	formKeyChannel = "channel"
)

// keyTypes are the types a key is made as, the auth module's.
var keyTypes = []string{"secret", "publishable"}

// KeyMaker is the narrow surface a key is made through: the auth module's.
type KeyMaker interface {
	// MakeAPIKey makes a key in the operator's name and returns its id and
	// its token, the token's only copy.
	MakeAPIKey(ctx context.Context, createdBy, keyType, title string, scopes, channelIDs []string) (id, token string, err error)
}

// makeAPIKey makes the key the form describes and shows its token once; the
// answer is not stored by the browser, and a refusal comes back on the
// screen with what was typed (ADR 0351).
func (u *UI) makeAPIKey(w http.ResponseWriter, r *http.Request) {
	maker, ok := u.users.(KeyMaker)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "API keys unavailable",
			"The auth module's panel surface cannot make a key in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	principal, _ := corehttp.PrincipalFromContext(r.Context())
	title := strings.TrimSpace(r.PostFormValue(formKeyTitle))
	id, token, err := maker.MakeAPIKey(r.Context(), principal.ID, r.PostFormValue(formKeyType), title,
		r.PostForm[formScope], r.PostForm[formKeyChannel])
	switch {
	case err == nil:
		w.Header().Set("Cache-Control", "no-store")
		u.templates.render(w, r, http.StatusOK, "api_key_made.gohtml", map[string]any{
			titleKey: "Key " + title, "ID": id, "Token": token, pathKey: APIKeysPath,
		})
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsForbidden(err) || errors.IsNotFound(err):
		u.renderAPIKeysTyped(w, r, http.StatusUnprocessableEntity, keyTabs[0], messageFor(err), r.PostForm)
	default:
		u.unexpectedFailure(w, r, err, "The key could not be made")
	}
}
