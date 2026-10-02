package adminui

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// fakeInviter reads and changes users as fakeUserAccounts does and invites
// them as scripted, recording each invitation.
type fakeInviter struct {
	fakeUserAccounts
	opened    string
	inviteErr error
	invited   []string
	resendErr error
}

func (f *fakeInviter) InviteUser(
	_ context.Context, invitedBy, email, firstName, lastName string, scopes []string,
) (string, error) {
	f.invited = append(f.invited, fmt.Sprintf("%s|%s|%s|%s|%q", invitedBy, email, firstName, lastName, scopes))
	return f.opened, f.inviteErr
}

func (f *fakeInviter) ResendInvitation(_ context.Context, userID, invitedBy string) error {
	f.invited = append(f.invited, "again|"+userID+"|"+invitedBy)
	return f.resendErr
}

// TestAUserIsInvitedFromTheUsersScreen is ADR 0348: an operator holding
// admin is offered the form, a reader of the users is not; the surface is
// asked to open the user typed, trimmed, with the privileges ticked, from
// the operator, and the user's page says the invitation was sent, or that
// the user was opened and it was not; a refusal comes back on the screen
// with what was typed; and the user's page sends the invitation again.
func TestAUserIsInvitedFromTheUsersScreen(t *testing.T) {
	t.Parallel()

	inviter := &fakeInviter{opened: "usr_new"}
	inviter.listing = `[]`
	inviter.user = `{"id":"usr_new","email":"new@example.test","scopes":[],"created_at":"2026-10-02T08:00:00Z"}`
	panel := usersPanel(t, inviter)

	rec := campaignsRequest(panel, http.MethodGet, UsersPath, nil, scopeAdmin)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	_, form, found := strings.Cut(rec.Body.String(), `action="`+UsersPath+`/invitations"`)
	require.True(t, found, "an operator holding admin is offered the form")
	form, _, _ = strings.Cut(form, "</form>")
	assert.Contains(t, form, `value="order:read"> order:read`, "no privilege ticked to begin with")
	assert.NotContains(t, form, " checked")
	rec = campaignsRequest(panel, http.MethodGet, UsersPath, nil, scopeAuthRead)
	assert.NotContains(t, rec.Body.String(), "/invitations", "a reader of the users invites nobody")

	rec = campaignsRequest(panel, http.MethodPost, UsersPath+"/invitations", url.Values{
		formInviteEmail: {" new@example.test "}, formInviteFirstName: {" Ada "}, formInviteLastName: {" Lovelace "},
		formScope: {"order:read", "invoice:read"},
	}, scopeAdmin)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, UsersPath+"/usr_new?invited=sent", rec.Header().Get("Location"))
	landed := campaignsRequest(panel, http.MethodGet, rec.Header().Get("Location"), nil, scopeAdmin)
	assert.Contains(t, landed.Body.String(), "An invitation was sent to new@example.test.")
	rec = campaignsRequest(panel, http.MethodPost, UsersPath+"/invitations", url.Values{
		formInviteEmail: {"bare@example.test"},
	}, scopeAdmin)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, []string{
		`user_1|new@example.test|Ada|Lovelace|["order:read" "invoice:read"]`,
		`user_1|bare@example.test|||[]`,
	}, inviter.invited, "from the operator, trimmed, with what was ticked")

	inviter.inviteErr = errors.Unavailable("notification_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, UsersPath+"/invitations", url.Values{
		formInviteEmail: {"late@example.test"},
	}, scopeAdmin)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, UsersPath+"/usr_new?invited=unsent", rec.Header().Get("Location"))
	landed = campaignsRequest(panel, http.MethodGet, rec.Header().Get("Location"), nil, scopeAdmin)
	assert.Contains(t, landed.Body.String(), "The user was opened, but the invitation could not be sent")

	inviter.opened, inviter.inviteErr = "", errors.Conflict("auth_email_taken", "the email is already in use")
	rec = campaignsRequest(panel, http.MethodPost, UsersPath+"/invitations", url.Values{
		formInviteEmail: {"taken@example.test"}, formInviteFirstName: {"Typed"}, formScope: {"invoice:read"},
	}, scopeAdmin)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "the email is already in use")
	assert.Contains(t, body, "<details open>", "the refused form is open")
	assert.Contains(t, body, `name="email" value="taken@example.test"`, "with what was typed")
	assert.Contains(t, body, `name="first_name" value="Typed"`)
	assert.Contains(t, body, `value="invoice:read" checked>`)
	inviter.inviteErr = errors.Forbidden("auth_scope_escalation", "the \"admin\" scope cannot be granted")
	rec = campaignsRequest(panel, http.MethodPost, UsersPath+"/invitations", url.Values{formScope: {"admin"}}, scopeAdmin)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	inviter.inviteErr = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, UsersPath+"/invitations", url.Values{}, scopeAdmin)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	rec = campaignsRequest(panel, http.MethodPost, UsersPath+"/invitations", url.Values{}, scopeAuthRead)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	page := UsersPath + "/usr_new"
	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopeAdmin)
	assert.Contains(t, rec.Body.String(), `action="`+page+`/invitation"`, "the page sends the invitation again")
	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopeAuthRead)
	assert.NotContains(t, rec.Body.String(), "/invitation", "for an operator holding admin alone")
	inviter.invited = nil
	rec = campaignsRequest(panel, http.MethodPost, page+"/invitation", nil, scopeAdmin)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, page+"?invited=sent", rec.Header().Get("Location"))
	assert.Equal(t, []string{"again|usr_new|user_1"}, inviter.invited)
	inviter.resendErr = errors.Invalid("auth_invalid_input", "the inviting user's identifier is not valid")
	rec = campaignsRequest(panel, http.MethodPost, page+"/invitation", nil, scopeAdmin)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "the inviting user&#39;s identifier is not valid")
	inviter.resendErr = errors.Unavailable("notification_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, page+"/invitation", nil, scopeAdmin)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	plain := usersPanel(t, &fakeUserAccounts{fakeUsers: fakeUsers{listing: `[]`}})
	assert.NotContains(t, campaignsRequest(plain, http.MethodGet, UsersPath, nil, scopeAdmin).Body.String(),
		"/invitations", "a surface that cannot invite offers no form")
	assert.Equal(t, http.StatusServiceUnavailable,
		campaignsRequest(plain, http.MethodPost, UsersPath+"/invitations", url.Values{}, scopeAdmin).Code)
	assert.Equal(t, http.StatusServiceUnavailable,
		campaignsRequest(plain, http.MethodPost, page+"/invitation", nil, scopeAdmin).Code)
}
