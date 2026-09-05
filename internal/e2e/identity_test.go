//go:build integration

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	authapi "github.com/bdrtr/gobit/internal/modules/auth/api"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
)

// This file proves the plan's Phase 8 DoD:
//
//	"An unauthorized request gets 401; admin login -> access to a protected
//	 endpoint with the token; store API access without a publishable key is
//	 refused."
//
// # Why it goes through HTTP
//
// Phase 8's claim is not a SERVICE claim but a TRANSPORT claim: the guard
// middleware must be wired onto the right paths, in the right order and with
// the right exemption. A test that called the service directly would stay green
// even if the middleware had never been wired in at all — that was exactly the
// situation before Phase 8.
//
// The router is built with the SAME guard stack as production (see e2e_test.go,
// corehttp.APIGuards); the protection this test proves is the very one that
// runs in production.

// adminRequest makes an admin request with the given Authorization header.
//
// When the header is empty it is not added at all: "no header" and "empty
// header" are different situations, and the 401 claim targets the first one.
func adminRequest(t *testing.T, method, path, authorization string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, http.NoBody)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}

	rec := httptest.NewRecorder()
	testRouter.ServeHTTP(rec, req)

	return rec
}

// magazaIstegi makes a store request with the given publishable key.
func magazaIstegi(t *testing.T, path, key string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	if key != "" {
		req.Header.Set(corehttp.PublishableKeyHeader, key)
	}

	rec := httptest.NewRecorder()
	testRouter.ServeHTTP(rec, req)

	return rec
}

// loginRequest calls the login endpoint and returns the response recorder.
func loginRequest(t *testing.T, email, password string) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(map[string]string{"email": email, "password": password})
	require.NoError(t, err, "the login body could not be encoded")

	req := httptest.NewRequest(http.MethodPost, authapi.LoginPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	testRouter.ServeHTTP(rec, req)

	return rec
}

// jetonAl extracts the session token from a successful login.
func jetonAl(t *testing.T, email, password string) string {
	t.Helper()

	rec := loginRequest(t, email, password)
	require.Equal(t, http.StatusOK, rec.Code,
		"login should return 200; body: %s", rec.Body.String())

	var envelope struct {
		Data struct {
			Token     string `json:"token"`
			TokenType string `json:"token_type"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope),
		"the login response could not be decoded; body: %s", rec.Body.String())
	require.NotEmpty(t, envelope.Data.Token, "login should return a token")
	require.Equal(t, "Bearer", envelope.Data.TokenType,
		"the client must learn from the response which scheme to use")

	return envelope.Data.Token
}

// readIdentity reads the authenticated identity from the /admin/v1/auth/me
// endpoint.
func readIdentity(t *testing.T, authorization string) principalView {
	t.Helper()

	rec := adminRequest(t, http.MethodGet, "/admin/v1/auth/me", authorization)
	require.Equal(t, http.StatusOK, rec.Code,
		"the identity endpoint should return 200; body: %s", rec.Body.String())

	var envelope struct {
		Data principalView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope),
		"the identity response could not be decoded; body: %s", rec.Body.String())

	return envelope.Data
}

// principalView is the test-side counterpart of the /admin/v1/auth/me response.
type principalView struct {
	ID              string   `json:"id"`
	Kind            string   `json:"kind"`
	Scopes          []string `json:"scopes"`
	SalesChannelIDs []string `json:"sales_channel_ids"`
}

// TestUnauthenticatedAdminRequestIsRejected verifies that every admin request
// without an identity returns 401.
//
// The table is NOT limited to read endpoints: the real risk is on the write
// ones. Had the guard not been wired, DELETE /admin/v1/products/{id} would work
// with no header at all and the catalog could be deleted.
func TestUnauthenticatedAdminRequestIsRejected(t *testing.T) {
	tests := map[string]struct {
		method string
		path   string
	}{
		"identity endpoint":    {http.MethodGet, "/admin/v1/auth/me"},
		"user list":            {http.MethodGet, "/admin/v1/users"},
		"key creation":         {http.MethodPost, "/admin/v1/api-keys"},
		"product deletion":     {http.MethodDelete, "/admin/v1/products/prod_missing"},
		"order list":           {http.MethodGet, "/admin/v1/orders"},
		"sales channel list":   {http.MethodGet, "/admin/v1/sales-channels"},
		"undefined admin path": {http.MethodGet, "/admin/v1/no-such-endpoint"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := adminRequest(t, tt.method, tt.path, "")

			assert.Equal(t, http.StatusUnauthorized, rec.Code,
				"an admin request without an identity should return 401; body: %s", rec.Body.String())
			assert.Equal(t, "Bearer", rec.Header().Get("WWW-Authenticate"),
				"RFC 9110: a 401 must announce which scheme is expected")
		})
	}
}

// TestUndefinedAdminPathIsGuardedToo verifies that the guard runs BEFORE route
// matching.
//
// It is a separate test because its claim is a different one: had an undefined
// path returned 404, an attacker could map which admin endpoints exist just by
// looking at the status code. A 401 does not tell an existing path apart from a
// missing one.
func TestUndefinedAdminPathIsGuardedToo(t *testing.T) {
	existing := adminRequest(t, http.MethodGet, "/admin/v1/users", "")
	missing := adminRequest(t, http.MethodGet, "/admin/v1/definitely-not-here", "")

	assert.Equal(t, existing.Code, missing.Code,
		"an existing and a missing admin path must return the same status, otherwise the endpoint map leaks")
	assert.Equal(t, http.StatusUnauthorized, missing.Code)
}

// TestAdminLoginReachesProtectedEndpointWithToken is the second leg of the
// Phase 8 DoD: login -> token -> access to a protected endpoint.
func TestAdminLoginReachesProtectedEndpointWithToken(t *testing.T) {
	token := jetonAl(t, adminEmail, adminPassword)

	identity := readIdentity(t, "Bearer "+token)

	assert.Equal(t, adminID, identity.ID, "the token must carry the user who logged in")
	assert.Equal(t, authsvc.PrincipalKindUser, identity.Kind)
	assert.Contains(t, identity.Scopes, corehttp.ScopeAdmin,
		"the default admin user should be fully privileged")

	// The token must be valid not only on the identity endpoint but on a real
	// admin endpoint too: had the identity endpoint followed a path independent
	// of the guard, the test would have stayed blind.
	rec := adminRequest(t, http.MethodGet, "/admin/v1/users", "Bearer "+token)
	assert.Equal(t, http.StatusOK, rec.Code,
		"the user list should be readable with the token; body: %s", rec.Body.String())
}

// TestSecretKeyReachesTheAdminSurface verifies that a non-human caller (an
// integration) can work without obtaining a token.
func TestSecretKeyReachesTheAdminSurface(t *testing.T) {
	identity := readIdentity(t, "Bearer "+secretKey)

	assert.Equal(t, authsvc.PrincipalKindAPIKey, identity.Kind)
	assert.Contains(t, identity.Scopes, corehttp.ScopeAdmin)
	assert.Empty(t, identity.SalesChannelIDs,
		"a secret key carries no sales channel; the channel binding belongs to the publishable key")
}

// TestInvalidCredentialsAreRejected shows that the identity is really
// VERIFIED: a stub that only checks "is there a header" cannot pass this
// table.
func TestInvalidCredentialsAreRejected(t *testing.T) {
	validToken := jetonAl(t, adminEmail, adminPassword)

	tests := map[string]string{
		"empty scheme":                   "Bearer",
		"wrong scheme":                   "Basic " + secretKey,
		"made-up token":                  "Bearer made.up.token.string",
		"token with a broken signature":  "Bearer " + validToken + "broken",
		"made-up secret key":             "Bearer sk_" + "0123456789abcdef0123456789abcdef",
		"publishable key":                "Bearer " + publishableKey,
		"bare credential with no scheme": secretKey,
	}

	for name, authorization := range tests {
		t.Run(name, func(t *testing.T) {
			rec := adminRequest(t, http.MethodGet, "/admin/v1/auth/me", authorization)

			assert.Equal(t, http.StatusUnauthorized, rec.Code,
				"an invalid credential should return 401; body: %s", rec.Body.String())
		})
	}
}

// TestStoreRequestWithoutPublishableKeyIsRejected is the third leg of the
// Phase 8 DoD.
func TestStoreRequestWithoutPublishableKeyIsRejected(t *testing.T) {
	withoutKey := magazaIstegi(t, "/store/v1/products", "")
	assert.Equal(t, http.StatusUnauthorized, withoutKey.Code,
		"a store request without a publishable key must be rejected; body: %s", withoutKey.Body.String())

	withKey := magazaIstegi(t, "/store/v1/products", publishableKey)
	assert.Equal(t, http.StatusOK, withKey.Code,
		"a store request with the publishable key should pass; body: %s", withKey.Body.String())
}

// TestSecretKeyDoesNotPassInTheStoreHeader verifies that the key types cannot
// swap places.
//
// The claim is security, not convenience: could the secret key be carried in a
// header that is visible in the browser, admin authority would be embedded
// inside the storefront code.
func TestSecretKeyDoesNotPassInTheStoreHeader(t *testing.T) {
	rec := magazaIstegi(t, "/store/v1/products", secretKey)

	assert.Equal(t, http.StatusUnauthorized, rec.Code,
		"the secret key must not be accepted in the store header; body: %s", rec.Body.String())
}

// TestStoreIdentityCarriesTheSalesChannel verifies that the publishable key's
// job is not authority but CONTEXT: the request is bound to a sales channel.
func TestStoreIdentityCarriesTheSalesChannel(t *testing.T) {
	identity := readIdentity(t, "Bearer "+secretKey)
	require.Empty(t, identity.SalesChannelIDs)

	// The store identity cannot be read from an admin endpoint (the publishable
	// key does not pass there), so the authenticator is asked directly — this is
	// the very identity the guard middleware puts into the context.
	principal, err := testAuthn.AuthenticateStore(context.Background(), publishableKey)
	require.NoError(t, err, "the publishable key should produce a store identity")

	assert.Equal(t, authsvc.PrincipalKindAPIKey, principal.Kind)
	assert.Empty(t, principal.Scopes,
		"a publishable key CARRIES NO AUTHORITY; if it did, it would be an admin identity placed in the browser")
	assert.Equal(t, []string{testChannelID}, principal.SalesChannelIDs)
}

// TestRevokedKeyIsRejected verifies that revocation takes effect IMMEDIATELY.
//
// The key's record is not deleted, it is marked "revoked"; if the verification
// path does not read that mark, a revoked key would keep working forever.
func TestRevokedKeyIsRejected(t *testing.T) {
	ctx := context.Background()

	key, plaintext, err := authSvc.CreateAPIKey(ctx, authsvc.CreateAPIKeyInput{
		Type:      models.APIKeySecret,
		Title:     "key to be revoked",
		CreatedBy: adminID,
	})
	require.NoError(t, err, "the key could not be created")

	before := adminRequest(t, http.MethodGet, "/admin/v1/auth/me", "Bearer "+plaintext)
	require.Equal(t, http.StatusOK, before.Code,
		"a fresh key should work; body: %s", before.Body.String())

	_, err = authSvc.RevokeAPIKey(ctx, key.ID, adminID)
	require.NoError(t, err, "the key could not be revoked")

	after := adminRequest(t, http.MethodGet, "/admin/v1/auth/me", "Bearer "+plaintext)
	assert.Equal(t, http.StatusUnauthorized, after.Code,
		"a revoked key must be rejected; body: %s", after.Body.String())
}

// TestLoginEndpointStaysExemptFromTheGuard verifies that the guard does not
// cover the login endpoint.
//
// Had it covered it, nobody could log in and the system would lock itself out —
// losing the exemption is not a silent failure but a full-screen one.
func TestLoginEndpointStaysExemptFromTheGuard(t *testing.T) {
	// 200 with the correct password: had the endpoint gone through
	// authentication, we would never have got here.
	succeeded := loginRequest(t, adminEmail, adminPassword)
	assert.Equal(t, http.StatusOK, succeeded.Code,
		"the login endpoint must work without asking for an identity; body: %s", succeeded.Body.String())

	// A wrong password returns 401 as well, but that is the SERVICE's decision;
	// had the middleware blocked it, the body would have been different.
	wrong := loginRequest(t, adminEmail, "wrong-password")
	assert.Equal(t, http.StatusUnauthorized, wrong.Code)
}

// TestLoginFailureDoesNotLeakUserEnumeration verifies that a non-existent
// e-mail and a wrong password produce the SAME answer.
//
// Were there a difference, an attacker could learn one by one which e-mails are
// registered; that is the first step of a targeted phishing attack.
func TestLoginFailureDoesNotLeakUserEnumeration(t *testing.T) {
	wrongPassword := loginRequest(t, adminEmail, "definitely-wrong")
	unknownUser := loginRequest(t, "no-such-person@gobit.test", "definitely-wrong")

	require.Equal(t, http.StatusUnauthorized, wrongPassword.Code)
	require.Equal(t, http.StatusUnauthorized, unknownUser.Code)

	// request_id is DIFFERENT on every request and must be; the comparison
	// strips it out. The fields that carry the leak risk are the code and the
	// message.
	assert.Equal(t, errorSummary(t, wrongPassword), errorSummary(t, unknownUser),
		"the two error bodies must be indistinguishable")
}

// errorSummary extracts the code and the message from an error response.
func errorSummary(t *testing.T, rec *httptest.ResponseRecorder) [2]string {
	t.Helper()

	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope),
		"the error response could not be decoded; body: %s", rec.Body.String())

	return [2]string{envelope.Error.Code, envelope.Error.Message}
}

// TestHealthEndpointsStayUnguarded verifies that the path the orchestrator sees
// does not ask for an identity.
//
// Had the guard stack covered /health too, the readiness probe would get a 401,
// Kubernetes would count the process as unhealthy and restart it in an endless
// loop.
func TestHealthEndpointsStayUnguarded(t *testing.T) {
	for _, path := range []string{"/health", "/ready"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
			rec := httptest.NewRecorder()
			testRouter.ServeHTTP(rec, req)

			assert.NotEqual(t, http.StatusUnauthorized, rec.Code,
				"a health endpoint must not ask for an identity; body: %s", rec.Body.String())
		})
	}
}
