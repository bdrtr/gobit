package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/auth/api"
)

// This file tests the HTTP surface of the logout endpoint.
//
// That the session really drops is proven in the service layer (see the logout
// test of the service package); what is proven here is that the handler DOES
// NOT MAKE the decision but PASSES to the service what it takes to make it: the
// identity itself and its KIND.

// logoutRouter builds a router with an authenticated identity of the given
// kind.
func logoutRouter(t *testing.T, kind string) (chi.Router, *fakeAuth) {
	t.Helper()

	svc := &fakeAuth{}
	r := chi.NewRouter()
	r.Use(withPrincipalOfKind(kind))
	api.New(svc).Routes(r)

	return r, svc
}

// TestLogoutPassesTheCallerIdentityToTheService proves that logout takes WHOSE
// session it closes from the core.
//
// Had the identity been read from the client's body, the endpoint would have
// been the way to close somebody else's session: a user with no scope at all
// would write the admin's identity and throw them out.
func TestLogoutPassesTheCallerIdentityToTheService(t *testing.T) {
	r, svc := logoutRouter(t, principalKindUser)

	rec := request(t, r, http.MethodPost, logoutPath, "")

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, principalTestID, svc.lastLogoutPrincipalID,
		"the identity passed to the service has to be the authenticated caller's identity")
	assert.Equal(t, principalKindUser, svc.lastLogoutPrincipalKind,
		"the KIND of the identity has to pass too; only this way can the service tell an api key apart")
}

// TestLogoutResponseReportsTheWholesaleRevocation proves that the response body
// carries the contract.
//
// Had the endpoint returned a bodyless 204, a client thinking "I logged out of
// this device" could not have learned from the response that its other devices
// dropped too; the revocation moment is carried only in the body as well and
// lets the client eliminate the token in its hands without trial and error.
func TestLogoutResponseReportsTheWholesaleRevocation(t *testing.T) {
	r, _ := logoutRouter(t, principalKindUser)

	rec := request(t, r, http.MethodPost, logoutPath, "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var envelope struct {
		Data struct {
			AllSessions bool      `json:"all_sessions"`
			RevokedAt   time.Time `json:"revoked_at"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "body: %s", rec.Body.String())

	assert.True(t, envelope.Data.AllSessions,
		"that the revocation is WHOLESALE has to be readable from the response")
	assert.Equal(t, logoutMoment, envelope.Data.RevokedAt.UTC(),
		"the revocation moment has to be carried exactly as it came from the service")
}

// TestAPIKeyLogoutRejectionStatusComesFromTheService proves that the handler
// DOES NOT PICK the status code.
//
// The decision that a key cannot log out is the service's decision and comes
// back as a typed error; the handler hands it to corehttp.WriteError. Had the
// handler written its own status code, the endpoint would silently keep
// returning the wrong code when the service's classification changed.
func TestAPIKeyLogoutRejectionStatusComesFromTheService(t *testing.T) {
	r, svc := logoutRouter(t, principalKindAPIKey)
	svc.logoutErr = coreerrors.Invalid("auth_no_session",
		"an api key has no session that could be closed")

	rec := request(t, r, http.MethodPost, logoutPath, "")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"a typed error has to map to a status code; body: %s", rec.Body.String())
	assert.Equal(t, "auth_no_session", errorCode(t, rec))
	assert.Equal(t, principalKindAPIKey, svc.lastLogoutPrincipalKind,
		"for the rejection to be possible the kind has to have reached the service")
}

// TestLogoutWithoutIdentityReturns401 proves that a request with no identity
// cannot log out.
//
// The endpoint asks for no scope but it does ask for IDENTITY: a request
// without an identity cannot say whose session to close. In production
// corehttp.RequireAdmin cuts this off; here the handler's own gate is tested,
// because that middleware might one day exempt this path by mistake.
func TestLogoutWithoutIdentityReturns401(t *testing.T) {
	r, svc := anonymousRouter(t)

	rec := request(t, r, http.MethodPost, logoutPath, "")

	assert.Equal(t, http.StatusUnauthorized, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, corehttp.CodeUnauthenticated, errorCode(t, rec))
	assert.Zero(t, svc.callCount, "a request without an identity must NOT reach the service at all")
}
