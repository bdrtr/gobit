package adminui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// fakeUserAccounts lists the users as fakeUsers does, reads one as scripted
// and changes their privileges, recording each change.
type fakeUserAccounts struct {
	fakeUsers
	user    string
	readErr error
	revised []string
	err     error
}

func (f *fakeUserAccounts) UserJSON(context.Context, string) (json.RawMessage, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return json.RawMessage(f.user), nil
}

func (f *fakeUserAccounts) ReviseUserScopes(_ context.Context, id string, read, next []string) error {
	f.revised = append(f.revised, fmt.Sprintf("%s|%q|%q", id, read, next))
	return f.err
}

// TestAUsersPrivilegesAreChangedOnTheirPage is ADR 0347: a reader of the
// users is shown who the user is and the privileges they hold; an operator
// holding admin is offered every privilege a screen asks for, admin and any
// other the user holds, ticked as held and carrying them as drawn; the
// surface is asked to change them from those to the ones ticked, none
// ticked being none, and the page says so; a refusal comes back with what
// was ticked.
func TestAUsersPrivilegesAreChangedOnTheirPage(t *testing.T) {
	t.Parallel()

	accounts := &fakeUserAccounts{user: `{"id":"usr_ada","email":"ada@example.test","first_name":"Ada",
		"last_name":"Lovelace","scopes":["order:read","b2b:approve"],"second_factor":false,
		"created_at":"2026-05-02T08:00:00Z"}`}
	panel := usersPanel(t, accounts)
	page := UsersPath + "/usr_ada"

	rec := campaignsRequest(panel, http.MethodGet, page, nil, scopeAuthRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	for _, want := range []string{
		"<h1>ada@example.test</h1>", "Ada Lovelace", "since 2026-05-02", `<span class="pill">missing</span>`,
		"<p>order:read, b2b:approve</p>",
	} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, body, "/scopes", "a reader changes nothing")

	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopeAdmin)
	_, form, found := strings.Cut(rec.Body.String(), `action="`+page+`/scopes"`)
	require.True(t, found, "an operator holding admin is offered the form")
	form, _, _ = strings.Cut(form, "</form>")
	for _, want := range []string{
		`name="read_scope" value="order:read"`, `name="read_scope" value="b2b:approve"`,
		`value="order:read" checked> order:read`, `value="b2b:approve" checked> b2b:approve`,
		`value="admin"> admin`, `value="invoice:read"> invoice:read`, `value="customer:write"> customer:write`,
	} {
		assert.Contains(t, form, want)
	}
	assert.Equal(t, 1, strings.Count(form, `value="order:read" checked`), "each privilege once")

	rec = campaignsRequest(panel, http.MethodPost, page+"/scopes", url.Values{
		formReadScope: {"order:read", "b2b:approve"}, formScope: {"order:read", "invoice:read"},
	}, scopeAdmin)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, page+"?written=1", rec.Header().Get("Location"))
	landed := campaignsRequest(panel, http.MethodGet, rec.Header().Get("Location"), nil, scopeAuthRead)
	assert.Contains(t, landed.Body.String(), "The privileges were written.")
	rec = campaignsRequest(panel, http.MethodPost, page+"/scopes", url.Values{formReadScope: {"order:read"}}, scopeAdmin)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, []string{
		`usr_ada|["order:read" "b2b:approve"]|["order:read" "invoice:read"]`,
		`usr_ada|["order:read"]|[]`,
	}, accounts.revised, "none ticked is none")

	accounts.err = errors.Conflict("auth_last_administrator",
		"user usr_ada is the last who holds admin; give it to another user first")
	rec = campaignsRequest(panel, http.MethodPost, page+"/scopes", url.Values{
		formReadScope: {"order:read", "b2b:approve"}, formScope: {"invoice:read"},
	}, scopeAdmin)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body = rec.Body.String()
	assert.Contains(t, body, "give it to another user first")
	assert.Contains(t, body, `value="invoice:read" checked>`, "what was ticked")
	assert.Contains(t, body, `value="order:read"> order:read`, "and what was not")
	assert.Contains(t, body, `name="read_scope" value="b2b:approve"`, "the held privileges as they are now")
	accounts.err = errors.Forbidden("auth_scope_escalation", "the \"admin\" scope cannot be granted")
	rec = campaignsRequest(panel, http.MethodPost, page+"/scopes", url.Values{formScope: {"admin"}}, scopeAdmin)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "an escalation is said on the page")
	accounts.err = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, page+"/scopes", url.Values{formScope: {"admin"}}, scopeAdmin)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	rec = campaignsRequest(panel, http.MethodPost, page+"/scopes", url.Values{formScope: {"admin"}}, scopeAuthRead)
	assert.Equal(t, http.StatusForbidden, rec.Code, "a reader of the users may not change privileges")

	accounts.listing = `[{"id":"usr_ada","email":"ada@example.test","scopes":[],"created_at":"2026-05-02T08:00:00Z"}]`
	rec = campaignsRequest(panel, http.MethodGet, UsersPath, nil, scopeAuthRead)
	assert.Contains(t, rec.Body.String(), `<a href="`+UsersPath+`/usr_ada">`, "the Users screen links the page")
	accounts.readErr = errors.NotFound("auth_user_not_found", "user not found: usr_9")
	rec = campaignsRequest(panel, http.MethodGet, UsersPath+"/usr_9", nil, scopeAuthRead)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "There is no user usr_9.")

	lister := usersPanel(t, &fakeUsers{})
	assert.Equal(t, http.StatusServiceUnavailable, campaignsRequest(lister, http.MethodGet, page, nil, scopeAuthRead).Code)
	assert.Equal(t, http.StatusServiceUnavailable,
		campaignsRequest(lister, http.MethodPost, page+"/scopes", url.Values{formScope: {"admin"}}, scopeAdmin).Code)
}
