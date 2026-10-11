//go:build integration

package app

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/plugins/errorotlp"
)

// preflightOrigin is the storefront the installations below allow.
const preflightOrigin = "https://shop.example.test"

// TestEveryStoreMethodPassesABrowsersPreflight asks a browser's permission for
// every method a store route binds, in the default installation and in one
// with every plugin, with a storefront's origin allowed (D287).
//
// The allow-list is written once, in the core; the routes are the modules' and
// the plugins'. A method a route binds and the list leaves out is a route a
// browser storefront cannot call: its preflight answers without the method and
// the browser never sends the request. PATCH on a cart's line item was one.
func TestEveryStoreMethodPassesABrowsersPreflight(t *testing.T) {
	setUpGateInstallation(t)
	t.Setenv("CORS_ALLOWED_ORIGINS", preflightOrigin)

	t.Run("the default installation", func(t *testing.T) {
		t.Setenv("PLUGINS", "")

		router, err := openGateInstallation(t, Options{})
		require.NoError(t, err)

		methods := storeMethods(t, router)
		// The walk reaching the method that was left out, so a pass is the
		// route passing rather than the route not being there.
		require.Contains(t, methods, http.MethodPatch, "no store route binds PATCH; the walk saw %v", methods)
		assertPreflightsPass(t, router, methods)
	})

	t.Run("every plugin", func(t *testing.T) {
		for name, value := range everyPluginsSettings(t) {
			t.Setenv(name, value)
		}
		// The two error reporters refuse to share their slot, and neither
		// binds a route.
		names := slices.DeleteFunc(pluginNames(), func(name string) bool { return name == errorotlp.Name })
		t.Setenv("PLUGINS", strings.Join(names, ","))

		router, err := openGateInstallation(t, Options{})
		require.NoError(t, err)

		assertPreflightsPass(t, router, storeMethods(t, router))
	})
}

// storeParam is a path parameter or a trailing wildcard in a route pattern.
var storeParam = regexp.MustCompile(`\{[^}]*\}|\*$`)

// storeMethods answers each method a store route binds, with the path of the
// first route binding it.
func storeMethods(t *testing.T, router chi.Router) map[string]string {
	t.Helper()

	methods := map[string]string{}
	require.NoError(t, chi.Walk(router, func(
		method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler,
	) error {
		if !strings.HasPrefix(pattern, corehttp.DefaultStorePrefix+"/") {
			return nil
		}
		path := storeParam.ReplaceAllString(pattern, "gate")
		if known, ok := methods[method]; !ok || path < known {
			methods[method] = path
		}

		return nil
	}))
	require.NotEmpty(t, methods, "the walk found no store route")

	return methods
}

// assertPreflightsPass sends a browser's preflight for each method to its
// route and asserts the answer allows it.
func assertPreflightsPass(t *testing.T, router http.Handler, methods map[string]string) {
	t.Helper()

	for method, path := range methods {
		req := httptest.NewRequest(http.MethodOptions, path, http.NoBody)
		req.Header.Set("Origin", preflightOrigin)
		req.Header.Set("Access-Control-Request-Method", method)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNoContent, rec.Code, "the preflight of %s %s: %s", method, path, rec.Body.String())
		assert.Equal(t, preflightOrigin, rec.Header().Get("Access-Control-Allow-Origin"), "%s %s", method, path)
		allowed := strings.Split(rec.Header().Get("Access-Control-Allow-Methods"), ", ")
		assert.Contains(t, allowed, method,
			"%s %s is a store route a browser storefront cannot call: its preflight allows %v",
			method, path, allowed)
	}
}
