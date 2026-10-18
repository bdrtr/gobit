package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
)

const panelOrigin = "https://admin.shop.example.com"

// adminRequest builds a request to the admin surface from an origin; a
// preflight when method is OPTIONS.
func adminRequest(method, origin string) *http.Request {
	r := httptest.NewRequest(method, "/admin/v1/orders", http.NoBody)
	r.Header.Set("Origin", origin)
	if method == http.MethodOptions {
		r.Header.Set("Access-Control-Request-Method", http.MethodPost)
		r.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
	}

	return r
}

// TestAPanelOnItsOwnHostMayCallTheAdminSurface is ADR 0451: a listed origin's
// preflight is answered with the bearer header allowed and no credentials,
// and its answers are readable; an origin not listed is answered nothing.
func TestAPanelOnItsOwnHostMayCallTheAdminSurface(t *testing.T) {
	reached := false
	h := corehttp.AdminCORS([]string{panelOrigin + "/"})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.Header().Set("Location", "/admin/v1/orders/order_1")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, adminRequest(http.MethodOptions, panelOrigin))
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, panelOrigin, rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Contains(t, rec.Header().Get("Access-Control-Allow-Headers"), "Authorization")
	assert.NotContains(t, rec.Header().Get("Access-Control-Allow-Headers"), corehttp.PublishableKeyHeader,
		"the store surface's key is no admin header")
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"), "the session cookie is never ridden")
	assert.False(t, reached, "a preflight does not reach the handler")

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, adminRequest(http.MethodGet, panelOrigin))
	assert.True(t, reached)
	assert.Equal(t, panelOrigin, rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Contains(t, rec.Header().Get("Access-Control-Expose-Headers"), "Location")
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"))

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, adminRequest(http.MethodOptions, "https://evil.example.net"))
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"), "an origin not listed is allowed nothing")
}

// TestTheAdminSurfaceTakesNoWildcard is ADR 0451: "*" is dropped, so a list
// holding only it writes no CORS header for any site.
func TestTheAdminSurfaceTakesNoWildcard(t *testing.T) {
	for _, origins := range [][]string{{corehttp.AnyOrigin}, {corehttp.AnyOrigin, panelOrigin}} {
		h := corehttp.AdminCORS(origins)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, adminRequest(http.MethodOptions, "https://evil.example.net"))
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"), "%v", origins)
	}
}

// TestTheAdminSurfacesCORSIsItsOwn is ADR 0451 in the guard stack: the admin
// list answers the admin prefix only, and the store list never answers it.
func TestTheAdminSurfacesCORSIsItsOwn(t *testing.T) {
	stack := corehttp.APIGuards(corehttp.GuardOptions{
		CORSOrigins:      []string{testOrigin},
		AdminCORSOrigins: []string{panelOrigin},
	})
	var h http.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for i := len(stack) - 1; i >= 0; i-- {
		h = stack[i](h)
	}

	ask := func(origin, path string) string {
		r := httptest.NewRequest(http.MethodOptions, path, http.NoBody)
		r.Header.Set("Origin", origin)
		r.Header.Set("Access-Control-Request-Method", http.MethodPost)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		return rec.Header().Get("Access-Control-Allow-Origin")
	}
	assert.Equal(t, panelOrigin, ask(panelOrigin, "/admin/v1/orders"))
	assert.Empty(t, ask(testOrigin, "/admin/v1/orders"), "the storefront's site may not read admin answers")
	assert.Empty(t, ask(panelOrigin, "/store/v1/carts"), "nor the panel's the store's, unless listed there")
	assert.Equal(t, testOrigin, ask(testOrigin, "/store/v1/carts"))
}
