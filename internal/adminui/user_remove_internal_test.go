package adminui

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// fakeRemover invites users as fakeInviter does and removes them as
// scripted, recording each removal.
type fakeRemover struct {
	fakeInviter
	removed   []string
	removeErr error
}

func (f *fakeRemover) RemoveUser(_ context.Context, id string) error {
	f.removed = append(f.removed, id)
	return f.removeErr
}

// TestAUserIsRemovedFromTheirPage is ADR 0349: an operator holding admin is
// offered the removal on another user's page, not on their own nor as a
// reader; the surface is asked to remove the user and the Users screen says
// so; the operator removing themselves is refused without asking; and a
// refusal, the last administrator's included, comes back on the page.
func TestAUserIsRemovedFromTheirPage(t *testing.T) {
	t.Parallel()

	remover := &fakeRemover{}
	remover.listing = `[]`
	remover.user = `{"id":"usr_new","email":"new@example.test","scopes":["admin"],"created_at":"2026-10-02T08:00:00Z"}`
	panel := usersPanel(t, remover)
	page := UsersPath + "/usr_new"

	rec := campaignsRequest(panel, http.MethodGet, page, nil, scopeAdmin)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `action="`+page+`/remove"`, "another user's page offers the removal")
	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopeAuthRead)
	assert.NotContains(t, rec.Body.String(), "/remove", "a reader removes nobody")
	remover.user = `{"id":"user_1","email":"me@example.test","scopes":["admin"],"created_at":"2026-10-02T08:00:00Z"}`
	rec = campaignsRequest(panel, http.MethodGet, UsersPath+"/user_1", nil, scopeAdmin)
	assert.NotContains(t, rec.Body.String(), "/remove", "nor is the operator offered their own removal")

	rec = campaignsRequest(panel, http.MethodPost, page+"/remove", nil, scopeAdmin)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, UsersPath+"?removed=1", rec.Header().Get("Location"))
	assert.Equal(t, []string{"usr_new"}, remover.removed)
	landed := campaignsRequest(panel, http.MethodGet, rec.Header().Get("Location"), nil, scopeAuthRead)
	assert.Contains(t, landed.Body.String(), "The user was removed.")
	assert.NotContains(t, campaignsRequest(panel, http.MethodGet, UsersPath, nil, scopeAuthRead).Body.String(),
		"The user was removed.")

	rec = campaignsRequest(panel, http.MethodPost, UsersPath+"/user_1/remove", nil, scopeAdmin)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "you cannot remove yourself here")
	assert.Equal(t, []string{"usr_new"}, remover.removed, "the operator is not removed")

	remover.removeErr = errors.Conflict("auth_last_administrator",
		"user usr_new is the last who holds admin; give it to another user first")
	rec = campaignsRequest(panel, http.MethodPost, page+"/remove", nil, scopeAdmin)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "give it to another user first")
	remover.removeErr = errors.NotFound("auth_user_not_found", "user not found: usr_new")
	rec = campaignsRequest(panel, http.MethodPost, page+"/remove", nil, scopeAdmin)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "There is no user usr_new.")
	remover.removeErr = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, page+"/remove", nil, scopeAdmin)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	rec = campaignsRequest(panel, http.MethodPost, page+"/remove", nil, scopeAuthRead)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	plain := usersPanel(t, &fakeInviter{fakeUserAccounts: fakeUserAccounts{
		user: `{"id":"usr_new","email":"new@example.test","scopes":[],"created_at":"2026-10-02T08:00:00Z"}`,
	}})
	assert.NotContains(t, campaignsRequest(plain, http.MethodGet, page, nil, scopeAdmin).Body.String(), "/remove",
		"a surface that cannot remove offers nothing")
	assert.Equal(t, http.StatusServiceUnavailable,
		campaignsRequest(plain, http.MethodPost, page+"/remove", nil, scopeAdmin).Code)
}
