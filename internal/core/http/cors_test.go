package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

const testOrigin = "https://shop.example.com"

// corsHandler wraps a trivial handler in the CORS middleware.
func corsHandler(origins []string) (handler http.Handler, reachedFlag *bool) {
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	return corehttp.CORS(origins)(next), &reached
}

// preflight builds a browser preflight for the given origin.
func preflight(origin string) *http.Request {
	r := httptest.NewRequest(http.MethodOptions, "/store/v1/carts", http.NoBody)
	r.Header.Set("Origin", origin)
	r.Header.Set("Access-Control-Request-Method", http.MethodPost)

	return r
}

// TestABrowserStorefrontCanAskPermission is the call that could not happen.
//
// The store surface's identity is a publishable key, which is not a secret and
// is expected to live in a browser — and yet the preflight died before that key
// was ever read, so the one topology that otherwise works end to end was
// unreachable.
func TestABrowserStorefrontCanAskPermission(t *testing.T) {
	h, reached := corsHandler([]string{testOrigin})
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, preflight(testOrigin))

	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, testOrigin, rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Contains(t, rec.Header().Get("Access-Control-Allow-Headers"), corehttp.PublishableKeyHeader)
	assert.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), http.MethodPost)
	assert.False(t, *reached, "a preflight must not reach the real handler")
}

// TestCredentialsAreNEVERAllowed is the CSRF decision, asserted.
//
// The store surface authenticates from a HEADER, which a browser does not
// attach by itself — that is where this API's CSRF immunity comes from.
// Allowing credentials would let a cross-site page ride an ambient cookie.
func TestCredentialsAreNEVERAllowed(t *testing.T) {
	h, _ := corsHandler([]string{testOrigin, corehttp.AnyOrigin})

	for _, r := range []*http.Request{preflight(testOrigin), simpleRequest(testOrigin)} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"),
			"allowing credentials would destroy the header-only CSRF immunity")
	}
}

// TestAnUnknownOriginGetsNothing keeps the allow-list meaningful.
func TestAnUnknownOriginGetsNothing(t *testing.T) {
	h, reached := corsHandler([]string{testOrigin})
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, preflight("https://evil.example.com"))

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
	assert.False(t, *reached, "a preflight the browser will discard must not run a handler")
}

// TestWithNoConfiguredOriginsNothingChanges is the safe default.
//
// An installation opts IN to being callable from other sites; a default-open
// policy is a security decision nobody made.
func TestWithNoConfiguredOriginsNothingChanges(t *testing.T) {
	h, reached := corsHandler(nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, simpleRequest(testOrigin))

	assert.True(t, *reached, "a normal request still passes through")
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, rec.Header().Get("Vary"))
}

// TestVaryIsSetWheneverAPolicyExists stops a cache from serving one origin's
// answer to another.
//
// It is set on DISALLOWED responses too: the answer still depends on the
// origin, and a cache that stored it without Vary would hand it to everybody.
func TestVaryIsSetWheneverAPolicyExists(t *testing.T) {
	h, _ := corsHandler([]string{testOrigin})

	for _, origin := range []string{testOrigin, "https://evil.example.com"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, simpleRequest(origin))

		assert.Contains(t, rec.Header().Get("Vary"), "Origin", "origin: %s", origin)
	}
}

// TestABareOPTIONSIsNotAPreflight keeps the method usable for anything else.
func TestABareOPTIONSIsNotAPreflight(t *testing.T) {
	h, reached := corsHandler([]string{testOrigin})
	r := httptest.NewRequest(http.MethodOptions, "/store/v1/carts", http.NoBody)
	r.Header.Set("Origin", testOrigin)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	assert.True(t, *reached, "without a request-method header it is an ordinary OPTIONS")
}

// TestTheWildcardOpensEverySite covers the public-storefront configuration.
func TestTheWildcardOpensEverySite(t *testing.T) {
	h, _ := corsHandler([]string{corehttp.AnyOrigin})
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, simpleRequest("https://anywhere.example.org"))

	assert.Equal(t, "https://anywhere.example.org", rec.Header().Get("Access-Control-Allow-Origin"),
		"the ORIGIN is echoed rather than the wildcard, so a cache stores one answer per site")
}

// TestOriginsAreMatchedCaseAndSlashInsensitively keeps a configuration typo
// from failing silently.
func TestOriginsAreMatchedCaseAndSlashInsensitively(t *testing.T) {
	h, _ := corsHandler([]string{"HTTPS://Shop.Example.com/"})
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, simpleRequest(testOrigin))

	assert.Equal(t, testOrigin, rec.Header().Get("Access-Control-Allow-Origin"))
}

// simpleRequest builds an ordinary cross-origin request.
func simpleRequest(origin string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/store/v1/carts", http.NoBody)
	r.Header.Set("Origin", origin)

	return r
}
