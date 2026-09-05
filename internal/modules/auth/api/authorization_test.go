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

// This file tests the AUTHORIZATION layer of auth's admin endpoints.
//
// The identity layer (corehttp.RequireAdmin) is imitated here: what the test
// wants to prove is not "is the identity resolved correctly" but "is the SCOPE
// of the resolved identity enforced per endpoint". When the two are tested
// separately, the state in which authentication works flawlessly while
// authorization was never attached at all — that is, the fault that was fixed —
// stays visible.

// The identity constants the tests share.
const (
	// principalTestID is the identity the fake middleware puts into the
	// context.
	principalTestID = "usr_test"
	// principalKindUser is the admin user identity kind.
	principalKindUser = "user"
	// principalKindAPIKey is the API key identity kind.
	principalKindAPIKey = "api_key"
	// logoutPath is the full path of the logout endpoint.
	logoutPath = "/admin/v1/auth/logout"
)

// scopedRouter builds a router with an AUTHENTICATED identity carrying the
// given scopes.
//
// Giving no scope at all is a valid state and produces the "has an identity but
// no scope" caller; for the state where there is no identity at all there is
// [anonymousRouter].
func scopedRouter(t *testing.T, scopes ...string) (chi.Router, *fakeAuth) {
	t.Helper()

	svc := &fakeAuth{}
	r := chi.NewRouter()
	r.Use(withPrincipal(scopes...))
	api.New(svc).Routes(r)

	return r, svc
}

// anonymousRouter builds a router in which NO identity is put into the context.
func anonymousRouter(t *testing.T) (chi.Router, *fakeAuth) {
	t.Helper()

	svc := &fakeAuth{}
	r := chi.NewRouter()
	api.New(svc).Routes(r)

	return r, svc
}

// withPrincipal returns the middleware that puts an authenticated USER identity
// into the context.
//
// In production corehttp.RequireAdmin does this; in the test, putting the
// identity in by hand makes it possible to test the authorization layer without
// token production and without a database.
func withPrincipal(scopes ...string) func(http.Handler) http.Handler {
	return withPrincipalOfKind(principalKindUser, scopes...)
}

// withPrincipalOfKind puts an authenticated identity of the given KIND into the
// context.
//
// The kind has to be given separately: the logout endpoint makes its decision
// according to the identity's kind (an API key has no session to close) and
// only this way can it be tested that the handler passes the kind to the
// service.
func withPrincipalOfKind(kind string, scopes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := corehttp.Principal{
				ID:     principalTestID,
				Kind:   kind,
				Scopes: scopes,
			}
			next.ServeHTTP(w, r.WithContext(corehttp.WithPrincipal(r.Context(), principal)))
		})
	}
}

// request runs an admin request and returns the response recorder.
func request(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// errorCode returns the code inside the error envelope.
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "body: %s", rec.Body.String())
	return envelope.Error.Code
}

// writeEndpoints are all the write endpoints that ask for a scope.
//
// The list has to grow along with [api.Handler.Routes]: a write endpoint that
// is added but not written down here is the only place that could silently be
// left without a scope.
var writeEndpoints = map[string]struct {
	method string
	path   string
	body   string
	want   int
}{
	"create user": {http.MethodPost, "/admin/v1/users", `{"email":"a@b.co"}`, http.StatusCreated},
	"update user": {http.MethodPut, "/admin/v1/users/usr_1", `{}`, http.StatusOK},
	"delete user": {http.MethodDelete, "/admin/v1/users/usr_1", "", http.StatusNoContent},
	"set password": {
		http.MethodPost, "/admin/v1/users/usr_1/password", `{"password":"a-very-long-password"}`, http.StatusNoContent,
	},
	"create api key": {
		http.MethodPost, "/admin/v1/api-keys", `{"type":"secret","title":"t","scopes":["admin"]}`, http.StatusCreated,
	},
	"delete api key": {http.MethodDelete, "/admin/v1/api-keys/apk_1", "", http.StatusNoContent},
	"revoke api key": {http.MethodPost, "/admin/v1/api-keys/apk_1/revoke", "", http.StatusOK},
	"link channel": {
		http.MethodPost, "/admin/v1/api-keys/apk_1/sales-channels", `{"sales_channel_id":"sc_1"}`, http.StatusOK,
	},
	"unlink channel": {
		http.MethodDelete, "/admin/v1/api-keys/apk_1/sales-channels/sc_1", "", http.StatusNoContent,
	},
	"create channel": {http.MethodPost, "/admin/v1/sales-channels", `{"name":"web"}`, http.StatusCreated},
	"update channel": {http.MethodPut, "/admin/v1/sales-channels/sc_1", `{}`, http.StatusOK},
	"delete channel": {http.MethodDelete, "/admin/v1/sales-channels/sc_1", "", http.StatusNoContent},
}

// readEndpoints are all the read endpoints that ask for a scope.
var readEndpoints = map[string]string{
	"user list":            "/admin/v1/users",
	"single user":          "/admin/v1/users/usr_1",
	"api key list":         "/admin/v1/api-keys",
	"single api key":       "/admin/v1/api-keys/apk_1",
	"channels of a key":    "/admin/v1/api-keys/apk_1/sales-channels",
	"sales channel list":   "/admin/v1/sales-channels",
	"single sales channel": "/admin/v1/sales-channels/sc_1",
}

// TestWriteEndpointRejectsNarrowlyScopedCaller proves that the write endpoints
// ask for [api.ScopeWrite].
//
// The caller is a REAL identity and has the read scope; the only thing missing
// is the write scope. The fault itself was exactly this: every caller whose
// identity was authenticated could call all the admin endpoints, with no regard
// for their scope.
func TestWriteEndpointRejectsNarrowlyScopedCaller(t *testing.T) {
	for name, tt := range writeEndpoints {
		t.Run(name, func(t *testing.T) {
			r, svc := scopedRouter(t, api.ScopeRead)

			rec := request(t, r, tt.method, tt.path, tt.body)

			assert.Equal(t, http.StatusForbidden, rec.Code,
				"a read-scoped caller has to get a 403 at a write endpoint; body: %s", rec.Body.String())
			assert.Equal(t, corehttp.CodeForbidden, errorCode(t, rec))
			assert.Zero(t, svc.callCount,
				"a rejected request must NOT reach the service at all; the write would have happened before the rejection")
		})
	}
}

// TestWriteEndpointAcceptsAdminCaller proves that the authorization layer does
// not merely reject but also PASSES the right identity through.
//
// Being a separate test is deliberate: a middleware that rejects every request
// would pass the table above flawlessly while locking the admin surface
// entirely.
func TestWriteEndpointAcceptsAdminCaller(t *testing.T) {
	for name, tt := range writeEndpoints {
		t.Run(name, func(t *testing.T) {
			r, svc := scopedRouter(t, corehttp.ScopeAdmin)

			rec := request(t, r, tt.method, tt.path, tt.body)

			assert.Equal(t, tt.want, rec.Code,
				"an admin has to be able to call the write endpoint; body: %s", rec.Body.String())
			assert.Positive(t, svc.callCount, "the request has to reach the service")
		})
	}
}

// TestReadEndpointWorksWithNarrowScope proves that the read endpoints DO NOT
// ASK for admin.
//
// [api.ScopeRead] exists only to keep the write endpoints closed; binding
// reading to admin as well would reduce the dictionary to a single scope and
// would lead a narrowly scoped integration (a job reporting the user list, for
// example) to ask for full privileges.
func TestReadEndpointWorksWithNarrowScope(t *testing.T) {
	for name, path := range readEndpoints {
		t.Run(name, func(t *testing.T) {
			r, svc := scopedRouter(t, api.ScopeRead)

			rec := request(t, r, http.MethodGet, path, "")

			assert.Equal(t, http.StatusOK, rec.Code,
				"the read scope has to be enough for a read endpoint; body: %s", rec.Body.String())
			assert.Equal(t, 1, svc.callCount)
		})
	}
}

// TestReadEndpointRejectsCallerWithoutScope proves that a user with no scope at
// all cannot reach the read endpoints either.
//
// The godoc of service.CreateUserInput.Scopes says an empty scope list produces
// a user who "can log in but cannot reach any admin endpoint"; this test is the
// counterpart of that sentence.
func TestReadEndpointRejectsCallerWithoutScope(t *testing.T) {
	for name, path := range readEndpoints {
		t.Run(name, func(t *testing.T) {
			r, svc := scopedRouter(t)

			rec := request(t, r, http.MethodGet, path, "")

			assert.Equal(t, http.StatusForbidden, rec.Code,
				"a user without scope has to get a 403 at a read endpoint; body: %s", rec.Body.String())
			assert.Zero(t, svc.callCount)
		})
	}
}

// TestIdentityEndpointsRequireNoScope proves that the login, /auth/me and
// /auth/logout endpoints ask for no scope.
//
// The login endpoint is only about to establish the identity; had it asked for
// a scope nobody could ever log in. The identity endpoint reads back the scopes
// the caller ALREADY holds; had it asked for a scope, a caller without scope
// could not see the reason for their 403. The logout endpoint asks for no scope
// either: closing your own session is not a privilege, and had it asked for a
// scope, the token of an admin whose scope had been taken away could not be
// closed until it expired.
func TestIdentityEndpointsRequireNoScope(t *testing.T) {
	r, svc := scopedRouter(t)

	login := request(t, r, http.MethodPost, api.LoginPath, `{"email":"a@b.co","password":"secret"}`)
	assert.Equal(t, http.StatusOK, login.Code,
		"the login endpoint must not ask for a scope; body: %s", login.Body.String())

	identity := request(t, r, http.MethodGet, "/admin/v1/auth/me", "")
	assert.Equal(t, http.StatusOK, identity.Code,
		"the identity endpoint must not ask for a scope; body: %s", identity.Body.String())

	logout := request(t, r, http.MethodPost, logoutPath, "")
	assert.Equal(t, http.StatusOK, logout.Code,
		"the logout endpoint must not ask for a scope; body: %s", logout.Body.String())

	assert.Equal(t, 2, svc.callCount,
		"login and logout go down to the service; /auth/me reads from the context")
}

// TestRequestWithoutIdentityReturns401AtTheScopeLayer proves that when there is
// no identity the authorization layer returns a 401 and NOT a 403.
//
// The distinction is meaningful for the client: a 401 means "tell me who you
// are", a 403 means "I know who you are but you have no scope". Had a 403 been
// returned, a client that forgot the identity header would go asking for a
// scope instead of refreshing its token.
func TestRequestWithoutIdentityReturns401AtTheScopeLayer(t *testing.T) {
	r, svc := anonymousRouter(t)

	rec := request(t, r, http.MethodGet, "/admin/v1/users", "")

	assert.Equal(t, http.StatusUnauthorized, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "Bearer", rec.Header().Get("WWW-Authenticate"),
		"RFC 9110: a 401 has to report which scheme is expected")
	assert.Zero(t, svc.callCount)
}
