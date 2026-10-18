package api_test

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/auth/api"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// TestThePersonSignedInReadsTheirOwnName is ADR 0454: /auth/me answers a
// signed-in person's own address and name, asking no privilege, and an API
// key's answer carries none of the three.
func TestThePersonSignedInReadsTheirOwnName(t *testing.T) {
	t.Parallel()

	r, svc := scopedRouter(t)
	svc.user = models.User{Email: "ada@example.test", FirstName: "Ada", LastName: "Lovelace"}

	rec := request(t, r, http.MethodGet, "/admin/v1/auth/me", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"data":{"id":"usr_test","kind":"user","scopes":[],"second_factor_owed":false,
		"email":"ada@example.test","first_name":"Ada","last_name":"Lovelace"}}`, rec.Body.String())

	key := &fakeAuth{user: svc.user}
	keyRouter := chi.NewRouter()
	keyRouter.Use(withPrincipalOfKind(principalKindAPIKey, "order:read"))
	api.New(key).Routes(keyRouter)
	rec = request(t, keyRouter, http.MethodGet, "/admin/v1/auth/me", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "email", "a key has no address of a person")
	assert.Zero(t, key.callCount, "a key's identity reads no user record")
}

// TestAnInvitationIsWithdrawnInTheCallersName is ADR 0453: the route names
// the user from its path and the withdrawer from the proven identity, and
// demands the privilege that writes a user.
func TestAnInvitationIsWithdrawnInTheCallersName(t *testing.T) {
	t.Parallel()

	r, svc := scopedRouter(t, models.ScopeAdmin)
	rec := request(t, r, http.MethodDelete, "/admin/v1/users/user_9/invitations", "")
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	assert.Equal(t, "user_9", svc.withdrawnUser)
	assert.Equal(t, principalTestID, svc.withdrawnBy)

	narrow, svc := scopedRouter(t, api.ScopeRead)
	rec = request(t, narrow, http.MethodDelete, "/admin/v1/users/user_9/invitations", "")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Zero(t, svc.callCount)
}

// TestAKeysPrivilegesAreRevisedFromTheBody is ADR 0455: the route passes the
// privileges read and the new ones on as sent, answers the key record, and
// demands admin, the privilege that writes privilege.
func TestAKeysPrivilegesAreRevisedFromTheBody(t *testing.T) {
	t.Parallel()

	r, svc := scopedRouter(t, models.ScopeAdmin)
	rec := request(t, r, http.MethodPut, "/admin/v1/api-keys/apikey_1/scopes",
		`{"scopes_read":["order:read"],"scopes":["order:read","product:read"]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "apikey_1", svc.revisedKey)
	assert.Equal(t, []string{"order:read"}, svc.revisedRead)
	assert.Equal(t, []string{"order:read", "product:read"}, svc.revisedNext)
	assert.Contains(t, rec.Body.String(), `"scopes":["order:read","product:read"]`)

	rec = request(t, r, http.MethodPut, "/admin/v1/api-keys/apikey_1/scopes", `{"scopes":["a"],"title":"x"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "an unknown field is refused: %s", rec.Body.String())

	narrow, svc := scopedRouter(t, api.ScopeRead)
	rec = request(t, narrow, http.MethodPut, "/admin/v1/api-keys/apikey_1/scopes", `{"scopes":["a"]}`)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Zero(t, svc.callCount)
}
