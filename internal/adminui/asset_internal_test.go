package adminui

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// The panel's assets: the address they are asked for and who may cache them
// (D94, D95).
//
// Both checks read the RESPONSE the router produces, and the first deliberately
// does not recompute a stamp of its own. A test that hashed the bytes and
// compared its own answer to the page's would pass with the serving side broken
// in the same way — what has to agree is the address the page PRINTS and the
// stamp the route SERVES, which are written by two different functions.

// TestTheStylesheetAddressCarriesTheStampItIsServedWith ties the two halves.
//
// The cache header says `immutable`, which tells the browser not to revalidate at
// all for a year. That is only honest when a changed file is asked for at a
// changed address. Before D94 the stamp went into the ETag alone, the address
// never moved, and nothing ever issued a conditional request — so a release that
// edited the panel's CSS did not reach an operator who had opened it once. The
// godoc claimed the refetch; this is what makes the claim true.
func TestTheStylesheetAddressCarriesTheStampItIsServedWith(t *testing.T) {
	t.Parallel()

	_, r := panelFor(t, nil, scopeOrderRead)

	served := get(r, StylesheetPath)
	require.Equal(t, http.StatusOK, served.Code)
	stamp := strings.Trim(served.Header().Get("ETag"), `"`)
	require.NotEmpty(t, stamp, "the stylesheet is served without a stamp; nothing identifies "+
		"the version a browser is holding")

	// The frame is drawn by every page; the orders list is one that draws it.
	printed := get(r, OrdersPath).Body.String()

	assert.Contains(t, printed, StylesheetPath+"?v="+stamp,
		"the page asks for the stylesheet at an address that does not carry the stamp the "+
			"route serves. With `immutable` in the response, a browser holding the old copy "+
			"never asks again — the release changes the file and the operator keeps the old "+
			"one for a year")
	assert.NotContains(t, printed, `href="`+StylesheetPath+`"`,
		"the unstamped address must not be printed at all")
}

// TestEveryShellAsksForItsScriptAtTheStampedAddress covers BOTH shells.
//
// The panel renders two kinds of script-filled page — the review screen it ships
// and a screen a plugin registered — and the first version of these checks gated
// the stylesheet and the plugin's shell while the review screen's went
// unaudited. Removing the stamp from that one address stayed green, which is the
// same defect the shells themselves are written against: a rule stated for every
// page, audited on the pages somebody remembered.
//
// So the population is every shell the panel serves, and each one's printed
// address is compared with the stamp its own script is served with.
func TestEveryShellAsksForItsScriptAtTheStampedAddress(t *testing.T) {
	t.Parallel()

	page := testPage()
	_, r := panelFor(t, []Page{page}, corehttp.ScopeAdmin)

	for name, shell := range map[string]struct{ page, script string }{
		"the review screen the panel ships": {page: ReviewsPath, script: ReviewsScriptPath},
		"a screen a plugin registered":      {page: page.Path, script: page.Path + scriptSuffix},
	} {
		t.Run(name, func(t *testing.T) {
			served := get(r, shell.script)
			require.Equal(t, http.StatusOK, served.Code)
			stamp := strings.Trim(served.Header().Get("ETag"), `"`)
			require.NotEmpty(t, stamp, "%s is served with no stamp", shell.script)

			body := get(r, shell.page).Body.String()

			assert.Contains(t, body, shell.script+"?v="+stamp,
				"the shell asks for %s at an address carrying no stamp, or the wrong one. "+
					"The response says immutable, so a browser holding the old script keeps "+
					"it — and for a script-filled screen that means last release's content "+
					"under this release's heading", shell.script)
			assert.NotContains(t, body, `src="`+shell.script+`"`,
				"the unstamped address must not be printed")
		})
	}
}

// TestTwoScreensScriptsGetDifferentAddresses is what makes the stamp a STAMP.
//
// A stamp that does not depend on the bytes passes every other check here: the
// address and the ETag would carry the same constant and agree with each other
// perfectly, while a changed script kept the old address and never reached a
// browser. So two screens with different scripts are registered and their
// addresses compared — the one claim the agreement checks cannot make.
func TestTwoScreensScriptsGetDifferentAddresses(t *testing.T) {
	t.Parallel()

	first := Page{
		Label: "First", Path: URLPrefix + "/first",
		Scope: testPageScope, Script: []byte("// first\n"),
	}
	second := Page{
		Label: "Second", Path: URLPrefix + "/second",
		Scope: testPageScope, Script: []byte("// second, and longer\n"),
	}

	screens, err := validatePages([]Page{first, second})
	require.NoError(t, err)
	require.Len(t, screens, 2)

	assert.NotEqual(t, screens[0].stamp, screens[1].stamp,
		"two different scripts were stamped the same. A stamp that does not follow the "+
			"bytes makes the address permanent, and `immutable` then keeps last release's "+
			"script in every browser that has it")
	assert.NotEqual(t, screens[0].scriptURL(), screens[1].scriptURL())
}

// TestOnlyAnUnprivilegedAssetIsPubliclyCacheable is the population check for the
// cache directive.
//
// # Why it walks the router
//
// `public` invites a shared cache — a CDN, a company proxy — to store the
// response and hand it to somebody else. Since ADR 0156 the review screen's
// script and every registered screen's script sit behind a privilege, and they
// were still served `public`: a proxy answering a caller the panel had just
// refused (D95). The rule is "privileged means private", and its population is
// every panel route that answers with a stamp — derived from the router rather
// than from a list, because the next asset will be added by copying a neighbor.
//
// BOTH directions are asserted. A check that only demanded `private` where a
// scope exists would pass if every asset became private, which would take the
// stylesheet — install-identical bytes the login page needs before anyone is
// signed in — out of every shared cache for no reason.
func TestOnlyAnUnprivilegedAssetIsPubliclyCacheable(t *testing.T) {
	t.Parallel()

	ui, r := panelFor(t, []Page{testPage()}, corehttp.ScopeAdmin)

	var patterns []string
	require.NoError(t, chi.Walk(r, func(
		method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler,
	) error {
		if method == http.MethodGet {
			patterns = append(patterns, pattern)
		}

		return nil
	}))
	require.GreaterOrEqual(t, len(patterns), 17,
		"the walk found %d GET routes; a blind walk audits no asset at all", len(patterns))

	assets := 0
	for _, pattern := range patterns {
		rec := get(r, pattern)
		if rec.Header().Get("ETag") == "" {
			// Not an asset: the pages carry no stamp, because they depend on the
			// session and on data.
			continue
		}
		assets++

		t.Run(pattern, func(t *testing.T) {
			directive := rec.Header().Get("Cache-Control")
			require.NotEmpty(t, directive)

			if ui.scopes[pattern] == "" {
				assert.Contains(t, directive, "public",
					"%s carries no privilege and is not publicly cacheable; the login page "+
						"needs the stylesheet before anybody is signed in", pattern)

				return
			}

			assert.Contains(t, directive, "private",
				"%s is behind the %q privilege and is served %q. `public` lets a shared "+
					"cache hand these bytes to a caller this panel refused — the rule a "+
					"privileged endpoint states is not a rule if something in front of it "+
					"can answer instead", pattern, ui.scopes[pattern], directive)
			assert.NotContains(t, directive, "public")
		})
	}

	assert.GreaterOrEqual(t, assets, 3,
		"only %d stamped responses were found; the panel serves a stylesheet, the review "+
			"screen's script and a registered screen's script, so a smaller number means "+
			"this walk stopped seeing assets", assets)
}
