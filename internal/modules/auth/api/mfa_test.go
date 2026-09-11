package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/auth/api"
)

// mfaRouter builds a router whose handlers carry the given issuer.
//
// No principal middleware: each test puts its own identity into the request's
// context, because WHICH KIND of caller made the request is the thing under test.
func mfaRouter(t *testing.T, svc *fakeAuth, issuer string) chi.Router {
	t.Helper()

	r := chi.NewRouter()
	api.New(svc).WithMFAIssuer(issuer).Routes(r)

	return r
}

// mfaRequest sends a request with a PROVEN caller of the given kind.
//
// The principal is put into the context the way the guard ring does, because
// what these endpoints claim is about the caller and nothing else: they take no
// user id, so the only thing that decides who is acted on is what the ring left
// behind.
func mfaRequest(
	t *testing.T, h http.Handler, path, body, principalID, kind string,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if principalID != "" {
		req = req.WithContext(corehttp.WithPrincipal(req.Context(),
			corehttp.Principal{ID: principalID, Kind: kind, Scopes: []string{"admin"}}))
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// TestEnrollingActsOnTheCALLERAndNobodyElse is the endpoint's whole claim.
func TestEnrollingActsOnTheCALLERAndNobodyElse(t *testing.T) {
	t.Parallel()

	svc := &fakeAuth{}
	router := mfaRouter(t, svc, "")

	rec := mfaRequest(t, router, api.MFAEnrolPath, "", "usr_01CALLER0000000000", "user")

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "usr_01CALLER0000000000", svc.mfaEnrolFor,
		"the enrollment must be for whoever the request proved; an endpoint that took "+
			"an id would let one administrator hold another's second factor")

	var response struct {
		Data struct {
			Secret     string `json:"secret"`
			OtpauthURI string `json:"otpauth_uri"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.NotEmpty(t, response.Data.Secret,
		"the secret crosses here ONCE, because an authenticator cannot be given one "+
			"without being shown one")
	assert.NotEmpty(t, response.Data.OtpauthURI)
}

// TestAnAPIKeyCannotEnrolASecondFactor is the refusal that keeps the credential
// meaningful.
func TestAnAPIKeyCannotEnrolASecondFactor(t *testing.T) {
	t.Parallel()

	svc := &fakeAuth{}
	router := mfaRouter(t, svc, "")

	rec := mfaRequest(t, router, api.MFAEnrolPath, "", "key_01MACHINE00000000", "api_key")

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
	assert.Empty(t, svc.mfaEnrolFor,
		"a machine holds no authenticator, so enrolling for one writes a secret whose "+
			"only reader is the database")
	assert.Contains(t, rec.Body.String(), api.CodeMFANotAPerson)
}

// TestConfirmingCarriesTheCodeAndTheCaller pins what reaches the service.
func TestConfirmingCarriesTheCodeAndTheCaller(t *testing.T) {
	t.Parallel()

	svc := &fakeAuth{}
	router := mfaRouter(t, svc, "")

	rec := mfaRequest(t, router, api.MFAConfirmPath, `{"code":"123456"}`,
		"usr_01CALLER0000000000", "user")

	require.Equal(t, http.StatusNoContent, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "usr_01CALLER0000000000", svc.mfaConfirmFor)
	assert.Equal(t, "123456", svc.mfaCode)
	assert.Empty(t, rec.Body.String(), "204 carries no body")
}

// TestTheIssuerReachesTheEnrollment is what a person sees in their app.
func TestTheIssuerReachesTheEnrollment(t *testing.T) {
	t.Parallel()

	svc := &fakeAuth{}
	router := mfaRouter(t, svc, "Acme Shop")

	rec := mfaRequest(t, router, api.MFAEnrolPath, "", "usr_01CALLER0000000000", "user")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "Acme Shop", svc.mfaIssuer,
		"an administrator with three installations in one app tells them apart by this")
}
