package adminui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// fakeUsers lists the users as scripted, recording what each listing asked
// for.
type fakeUsers struct {
	listing string
	total   int64
	err     error
	asked   []string
}

func (f *fakeUsers) UsersJSON(_ context.Context, email, secondFactor string, limit, offset int32) (json.RawMessage, int64, error) {
	f.asked = append(f.asked, fmt.Sprintf("%s|%s|%d|%d", email, secondFactor, limit, offset))
	return json.RawMessage(f.listing), f.total, f.err
}

// usersPanel is a panel whose users are read through the surface given.
func usersPanel(t *testing.T, users UserLister) *UI {
	t.Helper()

	panel := newCatalogPanel(t, &fakeCatalog{})
	panel.users = users
	panel.scopes = builtInScopes()

	return panel
}

// TestTheUsersScreenListsThePrivilegesAndTheSecondFactor is ADR 0345: every
// user is listed when no tab is chosen, each with their e-mail, name, the
// privileges they hold or none, whether they have proven an authenticator,
// and since when; a tab lists those without or with one a page at a time; a
// user is found by their e-mail, trimmed, in every tab; and the screen is in
// the menu and asks to read the users.
func TestTheUsersScreenListsThePrivilegesAndTheSecondFactor(t *testing.T) {
	t.Parallel()

	users := &fakeUsers{total: 27, listing: `[{"id":"usr_ada","email":"ada@example.test","first_name":"Ada",
		"last_name":"Lovelace","scopes":["order:read","order:write"],"second_factor":true,
		"created_at":"2026-05-02T08:00:00Z"},{"id":"usr_bob","email":"bob@example.test","first_name":"",
		"last_name":"","scopes":[],"second_factor":false,"created_at":"2026-05-01T08:00:00Z"}]`}
	panel := usersPanel(t, users)

	rec := campaignsRequest(panel, http.MethodGet, UsersPath, nil, scopeAuthRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	_, ada, _ := strings.Cut(body, "<td>ada@example.test</td>")
	ada, bob, _ := strings.Cut(ada, "<td>bob@example.test</td>")
	for _, want := range []string{"<td>Ada Lovelace</td>", "<td>order:read, order:write</td>", "<td>proven</td>",
		"<td>2026-05-02</td>"} {
		assert.Contains(t, ada, want, "Ada's row")
	}
	for _, want := range []string{`<td><span class="muted">none</span></td>`,
		`<td><span class="pill">missing</span></td>`, "<td>2026-05-01</td>"} {
		assert.Contains(t, bob, want, "Bob's row")
	}
	for _, want := range []string{
		"27 in all.",
		`href="` + UsersPath + `?second_factor=" aria-current="page">all</a>`,
		`href="` + UsersPath + `?second_factor=missing">without a second factor</a>`,
		`href="` + UsersPath + `?second_factor=proven">with a second factor</a>`,
		`href="` + UsersPath + `?second_factor=&amp;email=&amp;page=2">Next</a>`,
		`href="` + UsersPath + `"`,
	} {
		assert.Contains(t, body, want)
	}
	assert.Equal(t, []string{"||25|0"}, users.asked, "every user, the first page")

	users.asked = nil
	rec = campaignsRequest(panel, http.MethodGet, UsersPath+"?second_factor=missing&page=2", nil, scopeAuthRead)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"|missing|25|25"}, users.asked)
	assert.Contains(t, rec.Body.String(), "27 without a second factor.")
	assert.Contains(t, rec.Body.String(), `?second_factor=missing" aria-current="page">`)
	assert.Contains(t, rec.Body.String(), `?second_factor=missing&amp;email=&amp;page=1">Previous</a>`)
	assert.NotContains(t, rec.Body.String(), ">Next</a>", "the second page of 27 is the last")

	users.asked = nil
	campaignsRequest(panel, http.MethodGet, UsersPath+"?second_factor=maybe", nil, scopeAuthRead)
	campaignsRequest(panel, http.MethodGet, UsersPath+"?second_factor=proven&email=+ada%40example.test+", nil,
		scopeAuthRead)
	assert.Equal(t, []string{"||25|0", "ada@example.test||25|0"}, users.asked,
		"an unknown tab is every user, and an e-mail is searched in every tab")
	rec = campaignsRequest(panel, http.MethodGet, UsersPath+"?email=ada%40example.test", nil, scopeAuthRead)
	assert.Contains(t, rec.Body.String(), "27 for ada@example.test.")

	rec = campaignsRequest(panel, http.MethodGet, UsersPath, nil, scopeOrderRead)
	assert.Equal(t, http.StatusForbidden, rec.Code, "an operator who may not read the users")
	rec = campaignsRequest(usersPanel(t, nil), http.MethodGet, UsersPath, nil, scopeAuthRead)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "an installation with no auth surface")
	users.err = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodGet, UsersPath, nil, scopeAuthRead)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
