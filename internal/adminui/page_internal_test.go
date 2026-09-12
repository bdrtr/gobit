package adminui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// A plugin's screen in the panel (ADR 0155).
//
// The refusals are the interesting half: each one is a startup failure, and each
// would otherwise be a screen an operator opens.

// testPageScope is the privilege the registration below names.
//
// A plugin's own scope rather than a built-in one: the panel must not care which
// module a scope belongs to, and a test using "product:read" here would pass even
// if the panel silently looked the page's privilege up in its own table.
const testPageScope = "analytics:read"

// testPage is a well-formed registration.
func testPage() Page {
	return Page{
		Label: "Funnel", Path: URLPrefix + "/analytics/funnel",
		Scope: testPageScope, Script: []byte("// hi\n"),
	}
}

// TestARegisteredScreenGetsBothARouteAndAMenuEntry is the inert shape this
// slice's own measurement named first.
//
// An entry whose link answers 404 and a screen only somebody who knew the URL
// could open are the same defect from two sides, and nothing else in the tree
// would catch either: the route audits read PROSE, and a plugin's path appears
// in none.
//
// # The subject is the RENDERED page, through the real construction
//
// The first version of this test called navItemsOf directly and asserted its
// answer — which proves that function works and says nothing about whether the
// panel uses it. Measured: with the wiring line replaced by nil, that version
// stayed green. So the panel is built the way the composition root builds it, a
// page is requested, and what is asserted is what a browser receives.
func TestARegisteredScreenGetsBothARouteAndAMenuEntry(t *testing.T) {
	ui, err := FromContainer(wiringContainer(t), false, []Page{testPage()})
	require.NoError(t, err)

	r := chi.NewRouter()
	ui.Routes(r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, signedInRequest(testPage().Path))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	body := rec.Body.String()
	assert.Contains(t, body, `href="`+testPage().Path+`"`,
		"the screen must be in the NAVIGATION; a route nothing links to is a screen only "+
			"somebody who knows the URL can open")
	assert.Contains(t, body, ">Funnel<",
		"and the entry must carry the label the plugin registered")

	require.Len(t, ui.pages, 1)
	assert.Equal(t, testPage().Path+scriptSuffix, ui.pages[0].scriptPath(),
		"the script's address is DERIVED from the page's; a second field would let a "+
			"screen run another screen's code with the operator's session")
}

// signedInRequest is a request the panel treats as an operator's.
//
// The navigation is drawn only for a signed-in request — a menu on the login page
// would be an invitation to a click that redirects — so a test about the menu has
// to carry a principal.
func signedInRequest(path string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)

	return req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
		ID: "user_panel_test", Kind: "user", Scopes: []string{corehttp.ScopeAdmin},
	}))
}

// TestAMalformedRegistrationStopsStartup pins every refusal.
//
// They are refusals rather than skips because a skipped registration is a plugin
// that looks installed and put nothing in the panel — and two of them are worse
// than that, which the messages say.
func TestAMalformedRegistrationStopsStartup(t *testing.T) {
	builtIn := sections()[0].Path

	// Each case starts from a VALID registration and breaks exactly one thing.
	// Spelling the broken pages out field by field is how a table stops
	// discriminating: a page missing two fields trips whichever rule fires
	// first, so every case after that one would pass without its own rule
	// existing at all.
	for name, break_ := range map[string]func(p *Page){
		"no label":  func(p *Page) { p.Label = "" },
		"no script": func(p *Page) { p.Script = nil },
		"no scope":  func(p *Page) { p.Scope = "" },
		"outside the panel's prefix": func(p *Page) {
			p.Path = "/admin/v1/x"
		},
		"the panel's own prefix but not under it": func(p *Page) {
			p.Path = URLPrefix + "-elsewhere/x"
		},
		"collides with its own script address": func(p *Page) {
			p.Path = URLPrefix + "/x" + scriptSuffix
		},
		"collides with a screen the panel ships": func(p *Page) {
			p.Path = builtIn
		},
	} {
		t.Run(name, func(t *testing.T) {
			page := testPage()
			break_(&page)

			// The valid page it was broken from must be ACCEPTED, or the case
			// below proves nothing about the field it changed.
			_, valid := validatePages([]Page{testPage()})
			require.NoError(t, valid)

			_, err := validatePages([]Page{page})

			require.Error(t, err)
			assert.Equal(t, CodeNotReady, coreerrors.CodeOf(err))
		})
	}
}

// TestTwoScreensCannotShareAPath keeps one of them from disappearing.
func TestTwoScreensCannotShareAPath(t *testing.T) {
	_, err := validatePages([]Page{testPage(), testPage()})

	require.Error(t, err)
	assert.Equal(t, CodeNotReady, coreerrors.CodeOf(err))
}

// TestTheScreenRendersItsShellAndServesItsScript is the pair, over a real
// router.
func TestTheScreenRendersItsShellAndServesItsScript(t *testing.T) {
	screens, err := validatePages([]Page{testPage()})
	require.NoError(t, err)

	templates, err := loadTemplates()
	require.NoError(t, err)
	templates.extra = navItemsOf(screens)

	ui := &UI{templates: templates, pages: screens}
	r := chi.NewRouter()
	ui.Routes(r)

	shell := httptest.NewRecorder()
	r.ServeHTTP(shell, httptest.NewRequest(http.MethodGet, testPage().Path, http.NoBody))
	require.Equal(t, http.StatusOK, shell.Code, shell.Body.String())
	assert.Contains(t, shell.Body.String(), "Funnel", "the shell carries the screen's label")
	assert.Contains(t, shell.Body.String(), testPage().Path+scriptSuffix,
		"the shell must point at the script the panel serves, not at a third-party URL")
	assert.Contains(t, shell.Body.String(), "<noscript>",
		"a page whose content arrives by script must say so when the script does not run")

	script := httptest.NewRecorder()
	r.ServeHTTP(script, httptest.NewRequest(
		http.MethodGet, testPage().Path+scriptSuffix, http.NoBody))
	require.Equal(t, http.StatusOK, script.Code)
	assert.Equal(t, "// hi\n", script.Body.String())
	assert.True(t, strings.HasPrefix(script.Header().Get("Content-Type"), "text/javascript"),
		"WriteAsset sends nosniff, so a wrong type here means the browser REFUSES to run "+
			"the screen")
	assert.NotEmpty(t, script.Header().Get("ETag"),
		"the stamp is derived from the bytes, so a changed script is refetched and an "+
			"unchanged one is not")
}

// TestTheShellCarriesTheSecurityPolicy ties the two halves of this slice
// together.
//
// The screen runs a plugin's script inside the operator's session. The policy is
// what says that script may only come from this origin — which is why the panel
// serves the BYTES rather than a URL.
func TestTheShellCarriesTheSecurityPolicy(t *testing.T) {
	screens, err := validatePages([]Page{testPage()})
	require.NoError(t, err)

	templates, err := loadTemplates()
	require.NoError(t, err)

	ui := &UI{templates: templates, pages: screens}
	r := chi.NewRouter()
	ui.Routes(r)

	for _, path := range []string{testPage().Path, testPage().Path + scriptSuffix} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))

		policy := rec.Header().Get(headerContentSecurityPolicy)
		require.NotEmpty(t, policy, "%s answered with no policy", path)
		assert.Contains(t, policy, "script-src 'self'",
			"a plugin's script may come from this origin and nowhere else")
		assert.Contains(t, policy, "default-src 'none'",
			"anything the panel gains later must fail loudly rather than quietly work")
	}
}
