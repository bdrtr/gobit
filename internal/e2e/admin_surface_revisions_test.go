//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authapi "github.com/bdrtr/gobit/internal/modules/auth/api"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
)

// acceptInvitation sends the invitation's token with a password, unauthenticated.
func acceptInvitation(t *testing.T, token, password string) (status int, body string) {
	t.Helper()

	sent, err := json.Marshal(map[string]string{"token": token, "password": password})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, authapi.AcceptInvitationPath, strings.NewReader(string(sent)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	testRouter.ServeHTTP(rec, req)

	return rec.Code, rec.Body.String()
}

// invitedUser creates a user with no password, invites them and answers the
// mailed token.
func invitedUser(t *testing.T) (id, email, token string) {
	t.Helper()
	ctx := t.Context()

	email = fmt.Sprintf("e2e-withdrawn-%d@example.com", fixtureCounter.Add(1))
	user, err := authSvc.CreateUser(ctx, authsvc.CreateUserInput{Email: email, Scopes: []string{"order:read"}}, "")
	require.NoError(t, err)
	require.NoError(t, authSvc.InviteUser(ctx, user.ID, adminID))
	sent := notificationSpy.notificationsTo(email)
	require.NotEmpty(t, sent)
	token = sent[len(sent)-1].Data["token"]
	require.NotEmpty(t, token)

	return user.ID, email, token
}

// TestAPasswordSetElsewhereVoidsTheMailedInvitation is ADR 0453 end to end: a
// password an operator writes spends the invitation that was mailed, and an
// invitation withdrawn over the API is not accepted; withdrawing one that is
// not pending is 404.
func TestAPasswordSetElsewhereVoidsTheMailedInvitation(t *testing.T) {
	admin := obtainToken(t, adminEmail, adminPassword)

	id, email, token := invitedUser(t)
	rec := tokenedRequest(t, http.MethodPost, "/admin/v1/users/"+id+"/password", admin,
		`{"password":"the-operator-handed-this-42"}`)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	code, body := acceptInvitation(t, token, "a-stranger-resets-it-42")
	assert.Equal(t, http.StatusUnprocessableEntity, code, body)
	assert.Contains(t, body, authsvc.CodeInvitationNotUsable)
	assert.NotEmpty(t, obtainToken(t, email, "the-operator-handed-this-42"), "the password set stands")

	id, _, token = invitedUser(t)
	rec = tokenedRequest(t, http.MethodDelete, "/admin/v1/users/"+id+"/invitations", admin, "")
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	code, body = acceptInvitation(t, token, "too-late-for-this-one-42")
	assert.Equal(t, http.StatusUnprocessableEntity, code, body)
	rec = tokenedRequest(t, http.MethodDelete, "/admin/v1/users/"+id+"/invitations", admin, "")
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), authsvc.CodeInvitationNotPending)
}

// TestThePersonSignedInIsNamed is ADR 0454 end to end: a person holding no
// auth:read reads their own address and name from /auth/me and still not
// another user's record.
func TestThePersonSignedInIsNamed(t *testing.T) {
	ctx := t.Context()
	email := fmt.Sprintf("e2e-named-%d@example.com", fixtureCounter.Add(1))
	user, err := authSvc.CreateUser(ctx, authsvc.CreateUserInput{
		Email: email, FirstName: "Ada", LastName: "Lovelace", Scopes: []string{"order:read"},
	}, "a-staff-member-chose-42")
	require.NoError(t, err)
	token := obtainToken(t, email, "a-staff-member-chose-42")

	rec := tokenedRequest(t, http.MethodGet, "/admin/v1/auth/me", token, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var me struct {
		Data struct {
			ID        string `json:"id"`
			Email     string `json:"email"`
			FirstName string `json:"first_name"`
			LastName  string `json:"last_name"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &me))
	assert.Equal(t, user.ID, me.Data.ID)
	assert.Equal(t, email, me.Data.Email)
	assert.Equal(t, "Ada", me.Data.FirstName)
	assert.Equal(t, "Lovelace", me.Data.LastName)

	rec = tokenedRequest(t, http.MethodGet, "/admin/v1/users/"+adminID, token, "")
	assert.Equal(t, http.StatusForbidden, rec.Code, "another user's record still asks auth:read")
}

// TestASecretKeyGainsAPrivilegeWithoutANewSecret is ADR 0455 end to end: a
// key revised over the API holds the new privilege on its next request, with
// the secret it already had, and a revision from a stale read is 409.
func TestASecretKeyGainsAPrivilegeWithoutANewSecret(t *testing.T) {
	ctx := t.Context()
	key, secret, err := authSvc.CreateAPIKey(ctx, authsvc.CreateAPIKeyInput{
		Type: models.APIKeySecret, Title: "e2e backbone key", Scopes: []string{"order:read"}, CreatedBy: adminID,
	})
	require.NoError(t, err)

	rec := tokenedRequest(t, http.MethodGet, "/admin/v1/users", secret, "")
	require.Equal(t, http.StatusForbidden, rec.Code, "the key does not read users yet")

	admin := obtainToken(t, adminEmail, adminPassword)
	rec = tokenedRequest(t, http.MethodPut, "/admin/v1/api-keys/"+key.ID+"/scopes", admin,
		`{"scopes_read":["order:read"],"scopes":["order:read","auth:read"]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = tokenedRequest(t, http.MethodGet, "/admin/v1/users", secret, "")
	assert.Equal(t, http.StatusOK, rec.Code, "the same secret reads users now: %s", rec.Body.String())

	rec = tokenedRequest(t, http.MethodPut, "/admin/v1/api-keys/"+key.ID+"/scopes", admin,
		`{"scopes_read":["order:read"],"scopes":["order:read"]}`)
	assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), authsvc.CodeAPIKeyScopesRevised)
}
