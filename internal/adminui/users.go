package adminui

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// The Users screen (ADR 0345): the shop's users, the newest first, each with
// the privileges they hold and whether they have proven an authenticator,
// read through the auth module's panel surface for an operator who may read
// them. Its tabs list those with and without a proven authenticator, which
// is what an installation requiring one is rolled out by (ADR 0265).

// UsersPath lists the users.
const UsersPath = URLPrefix + "/users"

// usersLabel is what the section is called on screen.
const usersLabel = "Users"

// The screen's parameters: the second-factor tab and the e-mail searched
// for.
const (
	paramSecondFactor = "second_factor"
	paramUserEmail    = "email"
)

// secondFactorTabs are the screen's tabs, as the auth module's surface
// names them: every user first, then those who have not proven an
// authenticator, then those who have.
var secondFactorTabs = []string{"", "missing", "proven"}

// usersPerPage is the list's page size, the other lists'.
const usersPerPage = 25

// UserLister is the narrow surface the screen reads through: the auth
// module's.
type UserLister interface {
	// UsersJSON lists the users, the newest first, a page at a time, with how
	// many there are: the one with the e-mail when it is given, and those who
	// have or have not proven an authenticator when secondFactor is "proven"
	// or "missing".
	UsersJSON(ctx context.Context, email, secondFactor string, limit, offset int32) (json.RawMessage, int64, error)
}

// userRow is one user as the surface sends them; the json tags are the
// contract with that surface, exercised end to end.
type userRow struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	FirstName    string    `json:"first_name"`
	LastName     string    `json:"last_name"`
	Scopes       []string  `json:"scopes"`
	SecondFactor bool      `json:"second_factor"`
	CreatedAt    time.Time `json:"created_at"`
}

// Name is the user's two name fields joined.
func (u userRow) Name() string { return joinName(u.FirstName, u.LastName) }

// listUsers renders the users in the chosen tab, or the one searched for.
func (u *UI) listUsers(w http.ResponseWriter, r *http.Request) {
	u.renderUsers(w, r, http.StatusOK, "", nil)
}

// renderUsers lists the users with a refused invitation's reason and what
// was typed in it (ADR 0348). An operator who may invite holds admin, and
// with it the privilege to read the users.
func (u *UI) renderUsers(w http.ResponseWriter, r *http.Request, code int, refused string, typed url.Values) {
	if u.users == nil {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Users unavailable",
			"The auth module's panel surface cannot list the users in this installation.")
		return
	}

	query := r.URL.Query()
	tab := query.Get(paramSecondFactor)
	if !slices.Contains(secondFactorTabs, tab) {
		tab = secondFactorTabs[0]
	}
	email := strings.TrimSpace(query.Get(paramUserEmail))
	listed := tab
	if email != "" {
		listed = ""
	}
	page := pageNumber(query.Get("page"))
	offset := (page - 1) * usersPerPage
	if offset > math.MaxInt32 {
		offset = math.MaxInt32
	}

	raw, total, err := u.users.UsersJSON(r.Context(), email, listed, usersPerPage, int32(offset))
	var rows []userRow
	if err == nil {
		err = json.Unmarshal(raw, &rows)
	}
	if err != nil {
		u.unexpectedFailure(w, r, err, "The users could not be read")
		return
	}

	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, canInvite := u.users.(UserInviter)
	data := map[string]any{
		titleKey:     usersLabel,
		"Users":      rows,
		statusKey:    tab,
		statusesKey:  secondFactorTabs,
		emailKey:     email,
		totalKey:     total,
		canCreateKey: canInvite && principal.HasScope(scopeAdmin),
		"Privileges": privilegeChoices(nil, typed[formScope]),
		typedKey:     typed,
		refusedKey:   refused,
		"Removed":    query.Get(paramRemoved) != "",
	}
	addPaging(data, page, int64(page*usersPerPage) < total, UsersPath)

	u.templates.render(w, r, code, "users.gohtml", data)
}
