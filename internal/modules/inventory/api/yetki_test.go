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
	"github.com/bdrtr/gobit/internal/modules/inventory/api"
)

// This file exercises the SCOPE layer of inventory's admin endpoints.
//
// The identity layer (corehttp.RequireAdmin) is imitated here: what the test
// wants to prove is not "is the identity resolved correctly" but "is the SCOPE
// of a resolved identity enforced endpoint by endpoint". When the two are
// exercised separately, the case where authentication works flawlessly while
// authorization was never wired up — that is, the fault that was fixed — stays
// visible.

// requestWithScopes sends a request with an identity carrying the given
// scopes.
//
// Granting no scope at all is a valid case and produces the caller who "has an
// identity but no scope" — that user was the fault itself.
func requestWithScopes(
	t *testing.T, router chi.Router, method, path, body string, scopes ...string,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
		ID:     "usr_dar",
		Kind:   "user",
		Scopes: scopes,
	}))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// requestWithoutPrincipal sends a request without putting ANY identity into
// the context.
func requestWithoutPrincipal(t *testing.T, router chi.Router, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(""))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// scopeErrorCode returns the code in the error envelope.
func scopeErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "body: %s", rec.Body.String())
	return envelope.Error.Code
}

// assertServiceUntouched verifies that the fake carries NO record of a call.
//
// The status code alone is not enough: a handler that returned the 403 AFTER
// writing the stock would write the same code. The only thing the fake records
// is the call parameters, which is why "no record at all" and "the service was
// never reached" are the same thing. The one exception is location creation:
// the fake does not record that call's input, and there the only proof is the
// status code.
func assertServiceUntouched(t *testing.T, svc *fakeInventory) {
	t.Helper()

	assert.Empty(t, svc.lastID, "a rejected request must NEVER reach the service")
	assert.Empty(t, svc.lastLocationID, "a rejected request must NEVER reach the service")
	assert.Zero(t, svc.lastItemInput, "a rejected request must NEVER reach the service")
	assert.Zero(t, svc.lastStocked, "a rejected request must NEVER reach the service")
	assert.Zero(t, svc.lastDelta, "a rejected request must NEVER reach the service")
	assert.Zero(t, svc.lastMovementInput, "a rejected request must NEVER reach the service")
}

// writeEndpoints are all the admin endpoints that ask for [api.ScopeWrite].
//
// The list has to grow along with [api.Handler.Routes]: a write endpoint that
// is added but not written here is the only place that could silently end up
// unguarded.
var writeEndpoints = map[string]struct {
	method string
	path   string
	body   string
}{
	"creating a location": {
		http.MethodPost, "/admin/v1/stock-locations", `{"name":"Merkez","country_code":"TR"}`,
	},
	// Closing a location is a write and belongs here for the reason the godoc
	// above gives (ADR 0055): the route sits on the write router, but until it
	// is in this table the three authorization tests never reach it, and a
	// route that fell off that router would go red nowhere.
	"closing a location": {
		http.MethodPost, "/admin/v1/stock-locations/sloc_1/close", "",
	},
	"creating an item": {http.MethodPost, "/admin/v1/inventory-items", `{"sku":"SKU-1"}`},
	"deleting an item": {http.MethodDelete, "/admin/v1/inventory-items/iitem_1", ""},
	"setting a level": {
		http.MethodPost, "/admin/v1/inventory-items/iitem_1/levels",
		`{"location_id":"sloc_1","stocked_quantity":5}`,
	},
	"adjusting a level": {
		http.MethodPost, "/admin/v1/inventory-items/iitem_1/levels/sloc_1/adjust", `{"delta":3}`,
	},
}

// readEndpoints are all the admin endpoints that ask for [api.ScopeRead].
var readEndpoints = map[string]string{
	"location list":   "/admin/v1/stock-locations",
	"single location": "/admin/v1/stock-locations/sloc_1",
	"item list":       "/admin/v1/inventory-items",
	"single item":     "/admin/v1/inventory-items/iitem_1",
	"level list":      "/admin/v1/inventory-items/iitem_1/levels",
	// The movement ledger is READ authority and not a scope of its own
	// (ADR 0068): it shows the history of the numbers the level listing above
	// already shows, to the same audience. Its row here is what keeps that
	// decision from becoming an accident — a listing that quietly fell off the
	// read router would go red nowhere else.
	"movement ledger": "/admin/v1/inventory-items/iitem_1/movements",
}

// TestWriteEndpointRejectsNarrowScopedCaller proves that the write endpoints
// ask for [api.ScopeWrite].
//
// The caller is a REAL identity and it has the read scope; the only thing
// missing is the write scope. This was exactly the fault: every caller whose
// identity was authenticated could write stock levels regardless of its scope.
func TestWriteEndpointRejectsNarrowScopedCaller(t *testing.T) {
	for name, tt := range writeEndpoints {
		t.Run(name, func(t *testing.T) {
			router, svc := newRouter(t)

			rec := requestWithScopes(t, router, tt.method, tt.path, tt.body, api.ScopeRead)

			assert.Equal(t, http.StatusForbidden, rec.Code,
				"a caller with the read scope has to get a 403 on a write endpoint; body: %s", rec.Body.String())
			assert.Equal(t, corehttp.CodeForbidden, scopeErrorCode(t, rec))
			assertServiceUntouched(t, svc)
		})
	}
}

// TestReadEndpointWorksWithNarrowScope proves that the read endpoints LET
// THROUGH the same narrow identity.
//
// Being a separate test is deliberate: a middleware that rejects every request
// would pass the table above flawlessly while locking the admin surface
// entirely. [api.ScopeRead] exists only to keep writing closed; binding
// reading to admin as well would force a narrowly scoped integration that
// reports stock (a warehouse dashboard, a sales forecast) to work with an
// identity that can corrupt the real stock.
func TestReadEndpointWorksWithNarrowScope(t *testing.T) {
	for name, path := range readEndpoints {
		t.Run(name, func(t *testing.T) {
			router, _ := newRouter(t)

			rec := requestWithScopes(t, router, http.MethodGet, path, "", api.ScopeRead)

			assert.Equal(t, http.StatusOK, rec.Code,
				"the read scope has to be enough for a read endpoint; body: %s", rec.Body.String())
		})
	}
}

// TestWriteEndpointAcceptsAdminCaller proves that corehttp.ScopeAdmin is a
// SUPERSCOPE, that is, that it is enough for writing without "inventory:write"
// being granted separately.
func TestWriteEndpointAcceptsAdminCaller(t *testing.T) {
	for name, tt := range writeEndpoints {
		t.Run(name, func(t *testing.T) {
			router, _ := newRouter(t)

			rec := requestWithScopes(t, router, tt.method, tt.path, tt.body, corehttp.ScopeAdmin)

			assert.NotEqual(t, http.StatusForbidden, rec.Code,
				"admin MUST NOT get a 403 on a write endpoint; body: %s", rec.Body.String())
			assert.NotEqual(t, http.StatusUnauthorized, rec.Code,
				"the admin identity has to be accepted; body: %s", rec.Body.String())
		})
	}
}

// TestUserWithoutScopesCannotReachStock proves that an admin user with no
// scope at all cannot call any stock endpoint.
//
// The godoc of auth service.CreateUserInput.Scopes says an empty scope list
// produces a user that "can log in but can reach no admin endpoint"; this test
// is the counterpart of that sentence on the inventory side.
func TestUserWithoutScopesCannotReachStock(t *testing.T) {
	for name, path := range readEndpoints {
		t.Run("read/"+name, func(t *testing.T) {
			router, svc := newRouter(t)

			rec := requestWithScopes(t, router, http.MethodGet, path, "")

			assert.Equal(t, http.StatusForbidden, rec.Code,
				"a user with no scope has to get a 403 on a read endpoint; body: %s", rec.Body.String())
			assertServiceUntouched(t, svc)
		})
	}

	for name, tt := range writeEndpoints {
		t.Run("write/"+name, func(t *testing.T) {
			router, svc := newRouter(t)

			rec := requestWithScopes(t, router, tt.method, tt.path, tt.body)

			assert.Equal(t, http.StatusForbidden, rec.Code,
				"a user with no scope has to get a 403 on a write endpoint; body: %s", rec.Body.String())
			assertServiceUntouched(t, svc)
		})
	}
}

// TestAdminRequestWithoutPrincipalReturns401 proves that when there is no
// identity at all the scope layer returns a 401, NOT a 403.
//
// The distinction matters to the client: 401 means "tell me who you are", 403
// means "I know who you are but you have no scope". Had it returned a 403, a
// client that forgot the identity header would go asking for a scope instead
// of refreshing its token.
func TestAdminRequestWithoutPrincipalReturns401(t *testing.T) {
	router, svc := newRouter(t)

	rec := requestWithoutPrincipal(t, router, http.MethodGet, "/admin/v1/inventory-items")

	assert.Equal(t, http.StatusUnauthorized, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "Bearer", rec.Header().Get("WWW-Authenticate"),
		"RFC 9110: a 401 has to report which scheme is expected")
	assertServiceUntouched(t, svc)
}
