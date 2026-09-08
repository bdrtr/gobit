package adminui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// TestTheAPISessionHeaderIsTheOneTheCoreReads pins a string this package spells
// and another package reads.
//
// [UI.APISession] puts the credential in a header and [corehttp.RequireAdmin]
// looks for it. Nothing compiles the two together: the core parses the header by
// name and the panel writes it by name, so a typo on either side produces a
// panel that authenticates nobody — and the failure looks like a signed-in
// operator being told to sign in again, forever, with no error anywhere.
//
// The check is made THROUGH the core rather than against a literal: comparing
// this package's constant to a copy of the same word would prove only that the
// two copies agree.
func TestTheAPISessionHeaderIsTheOneTheCoreReads(t *testing.T) {
	t.Parallel()

	const token = "a-token"

	var seen string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get(authorizationHeader)
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/anything", http.NoBody)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: token})

	(&UI{}).APISession(next).ServeHTTP(httptest.NewRecorder(), req)

	require.NotEmpty(t, seen, "the middleware set no header at all")
	assert.Equal(t, "Bearer "+token, seen)

	// And the core really reads what was written. RequireAdmin refuses when it
	// finds no credential, so a header under a name it does not read would send
	// this authenticator nothing.
	var authenticated bool
	guarded := corehttp.RequireAdmin(headerReader{expect: token, saw: &authenticated})(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	promoted := httptest.NewRequest(http.MethodGet, "/admin/v1/anything", http.NoBody)
	promoted.Header.Set(authorizationHeader, seen)
	guarded.ServeHTTP(httptest.NewRecorder(), promoted)

	assert.True(t, authenticated,
		"the core did not read the header this package writes; the panel would "+
			"authenticate nobody and an operator would be asked to sign in forever")
}

// headerReader is an authenticator that records what reached it.
type headerReader struct {
	expect string
	saw    *bool
}

func (h headerReader) AuthenticateAdmin(
	_ context.Context, scheme, credential string,
) (corehttp.Principal, error) {
	if scheme == corehttp.SchemeBearer && credential == h.expect {
		*h.saw = true
	}

	return corehttp.Principal{}, nil
}

// AuthenticateStore is unused here and exists because the interface has two
// halves; the panel's session is an admin credential and reaches no store path.
func (h headerReader) AuthenticateStore(
	_ context.Context, _ string,
) (corehttp.Principal, error) {
	return corehttp.Principal{}, nil
}

// TestTheCookiePathCoversBothAdminTrees holds [CookiePath] against the two
// prefixes it has to reach.
//
// The value is a string, and both of the things it must cover are strings in
// other places: the panel's own tree here, and the admin API's prefix in
// core/http. A cookie under a path that covers only one of them fails in a way
// nobody sees until an operator uses the screen — 401 on every call from a page
// that looks signed in.
func TestTheCookiePathCoversBothAdminTrees(t *testing.T) {
	t.Parallel()

	assert.True(t, strings.HasPrefix(URLPrefix, CookiePath),
		"%q does not cover the panel's own tree %q; the operator cannot sign in at all",
		CookiePath, URLPrefix)
	assert.True(t, strings.HasPrefix(corehttp.DefaultAdminPrefix, CookiePath),
		"%q does not cover the admin API %q, so the panel's script authenticates on no "+
			"call and ADR 0030's client cannot work", CookiePath, corehttp.DefaultAdminPrefix)

	// And it covers NO MORE than it has to. The store surface must never see
	// this cookie: it is a credential for a different audience and every
	// request that carries it is one more place it can leak from.
	assert.False(t, strings.HasPrefix("/store/v1", CookiePath),
		"the admin session cookie is sent to the storefront")
	assert.NotEqual(t, "/", CookiePath, "the cookie is sent to every path in the tree")
}
