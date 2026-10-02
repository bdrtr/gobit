package adminui

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The API keys screen (ADR 0350): the auth module's keys, the newest first,
// each with its token only as redacted, the privileges it carries and when
// it was last used, read for an operator who may read the users; one holding
// admin revokes a key, which stays listed with when and by whom.

// APIKeysPath lists the keys, and APIKeyRevokePath revokes one.
const (
	APIKeysPath      = URLPrefix + "/api-keys"
	APIKeyRevokePath = APIKeysPath + "/{id}/revoke"
)

// apiKeysLabel is what the section is called on screen.
const apiKeysLabel = "API keys"

// The screen's parameters: the revoked tab, and the key a revocation
// returns from.
const (
	paramKeyStatus = "status"
	paramRevoked   = "revoked"
)

// keyTabs are the screen's tabs: the keys still accepted first, which is
// what an operator comes to watch and what a screen asked for no tab lists,
// then the revoked ones, as the auth module's surface names them, then every
// one, which the surface names with no tab at all.
var keyTabs = []string{"open", "revoked", keysAll}

// keysAll is the tab of every key.
const keysAll = "all"

// keysPerPage is the list's page size, the other lists'.
const keysPerPage = 25

// KeyLister is the narrow surface the screen reads through: the auth
// module's.
type KeyLister interface {
	// APIKeysJSON lists the keys, the newest first, a page at a time, with
	// how many there are: those still accepted when revoked is "open", those
	// revoked when it is "revoked", and every one when it is empty.
	APIKeysJSON(ctx context.Context, revoked string, limit, offset int32) (json.RawMessage, int64, error)
}

// KeyRevoker is the narrow surface a key is revoked through.
type KeyRevoker interface {
	// RevokeAPIKey revokes the key in the operator's name.
	RevokeAPIKey(ctx context.Context, id, revokedBy string) error
}

// keyRow is one key as the surface sends it; the json tags are the contract
// with that surface, exercised end to end.
type keyRow struct {
	ID         string     `json:"id"`
	Type       string     `json:"type"`
	Title      string     `json:"title"`
	Redacted   string     `json:"redacted"`
	Scopes     []string   `json:"scopes"`
	CreatedBy  string     `json:"created_by"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	RevokedBy  string     `json:"revoked_by"`
	CreatedAt  time.Time  `json:"created_at"`
}

// listAPIKeys renders the keys in the chosen tab.
func (u *UI) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	u.renderAPIKeys(w, r, http.StatusOK, r.URL.Query().Get(paramKeyStatus), "")
}

// renderAPIKeys lists the keys in the tab with a refused revocation's
// reason.
func (u *UI) renderAPIKeys(w http.ResponseWriter, r *http.Request, code int, tab, refused string) {
	u.renderAPIKeysTyped(w, r, code, tab, refused, nil)
}

// revokeAPIKey revokes the key in the path in the operator's name and
// returns to the tab it was pressed on, which says so; a refusal, a key
// revoked meanwhile included, comes back on that tab (ADR 0350).
func (u *UI) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	revoker, ok := u.users.(KeyRevoker)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "API keys unavailable",
			"The auth module's panel surface cannot revoke a key in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	principal, _ := corehttp.PrincipalFromContext(r.Context())
	id, tab := chi.URLParam(r, "id"), r.PostFormValue(paramKeyStatus)
	err := revoker.RevokeAPIKey(r.Context(), id, principal.ID)
	switch {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w,
			APIKeysPath+"?"+url.Values{paramKeyStatus: {tab}, paramRevoked: {id}}.Encode())
	case errors.IsConflict(err) || errors.IsNotFound(err) || errors.IsInvalid(err):
		u.renderAPIKeys(w, r, http.StatusUnprocessableEntity, tab, messageFor(err))
	default:
		u.unexpectedFailure(w, r, err, "The key could not be revoked")
	}
}

// renderAPIKeysTyped lists the keys in the tab with a refusal's reason and
// what was typed in the key form, which an operator holding admin is offered
// with the sales channels a publishable key is attached to (ADR 0351). An
// operator who may revoke or make holds admin, and with it the privilege to
// read the keys.
func (u *UI) renderAPIKeysTyped(w http.ResponseWriter, r *http.Request, code int, tab, refused string, typed url.Values) {
	lister, ok := u.users.(KeyLister)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "API keys unavailable",
			"The auth module's panel surface cannot list the keys in this installation.")
		return
	}

	if !slices.Contains(keyTabs, tab) {
		tab = keyTabs[0]
	}
	page := pageNumber(r.URL.Query().Get("page"))
	offset := (page - 1) * keysPerPage
	if offset > math.MaxInt32 {
		offset = math.MaxInt32
	}
	revoked := tab
	if tab == keysAll {
		revoked = ""
	}
	raw, total, err := lister.APIKeysJSON(r.Context(), revoked, keysPerPage, int32(offset))
	var rows []keyRow
	if err == nil {
		err = json.Unmarshal(raw, &rows)
	}
	if err != nil {
		u.unexpectedFailure(w, r, err, "The keys could not be read")
		return
	}

	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, canRevoke := u.users.(KeyRevoker)
	_, canMake := u.users.(KeyMaker)
	canMake = canMake && principal.HasScope(scopeAdmin)
	var channels []channelOption
	if canMake {
		channels, _ = u.channelsOf(r)
	}
	data := map[string]any{
		canCreateKey:  canMake,
		typedKey:      typed,
		privilegesKey: privilegeChoices(nil, typed[formScope]),
		"Channels":    channels,
		"KeyTypes":    keyTypes,
		titleKey:      apiKeysLabel,
		"Keys":        rows,
		statusKey:     tab,
		statusesKey:   keyTabs,
		totalKey:      total,
		canReviseKey:  canRevoke && principal.HasScope(scopeAdmin),
		"Revoked":     r.URL.Query().Get(paramRevoked),
		refusedKey:    refused,
	}
	addPaging(data, page, int64(page*keysPerPage) < total, APIKeysPath)

	u.templates.render(w, r, code, "api_keys.gohtml", data)
}
