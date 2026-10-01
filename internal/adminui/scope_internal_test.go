package adminui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// The panel's privileges (ADR 0156).
//
// The subject of every check here is what a BROWSER receives, through the router
// the panel binds. A test calling [allowedItems] or [builtInScopes] would prove
// those functions work and say nothing about whether the panel uses them — the
// mistake the previous slice's own measurement was written about.

// panelFor builds the panel and a router that signs every request in as an
// operator carrying exactly the given privileges.
//
// Exactly those: [corehttp.ScopeAdmin] satisfies any scope, so a fixture holding
// it cannot tell a screen that asks for the right privilege from one that asks
// for nothing.
func panelFor(t *testing.T, pages []Page, scopes ...string) (*UI, chi.Router) {
	t.Helper()

	ui, err := FromContainer(wiringContainer(t), false, pages)
	require.NoError(t, err)

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(corehttp.WithPrincipal(req.Context(),
				corehttp.Principal{ID: "usr_scope_test", Kind: "user", Scopes: scopes})))
		})
	})
	ui.Routes(r)

	return ui, r
}

// get requests a path and returns the recorder.
func get(r chi.Router, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))

	return rec
}

// TestTheMenuOffersExactlyTheScreensTheRouterOpens is the two-sided check.
//
// # Why both directions in one assertion
//
// An entry the router refuses teaches the operator that the panel is broken
// rather than that a grant is missing. An entry MISSING for a screen the router
// opens hides a screen they are entitled to, and the panel has already been
// bitten by that half once — [TestTheReviewScreenIsInTheMenu] exists because of
// it. A test naming the expected menu per privilege would assert my arithmetic;
// what is asserted instead is that the two things a reader can disagree about
// AGREE: the rendered menu, and the router's own answer for each of its links.
//
// The comparison runs per menu path rather than as one set literal so a failure
// names the screen and the direction it broke in.
func TestTheMenuOffersExactlyTheScreensTheRouterOpens(t *testing.T) {
	t.Parallel()

	for name, held := range map[string][]string{
		"only the orders":                     {scopeOrderRead},
		"only the customers":                  {scopeCustomerRead},
		"the catalog and the reviews":         {scopeProductRead, scopeReviewRead},
		"everything, through the admin scope": {corehttp.ScopeAdmin},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ui, r := panelFor(t, []Page{testPage()}, held...)

			// The frame is rendered by whatever answers; the catalog list is
			// requested because it always renders one — with a 200 when the
			// privilege is held and with the refusal page when it is not, and
			// both carry the menu.
			body := get(r, ProductsPath).Body.String()

			for _, item := range ui.menu() {
				opened := get(r, item.Path).Code != http.StatusForbidden
				offered := strings.Contains(body, `href="`+item.Path+`"`)

				assert.Equal(t, opened, offered,
					"the menu and the router disagree about %q (%s): the router %s it and the "+
						"menu %s it. A link that can only refuse teaches the operator the panel "+
						"is broken; a screen missing from the menu is one only somebody who "+
						"knows the URL can open",
					item.Label, item.Path,
					map[bool]string{true: "OPENS", false: "REFUSES"}[opened],
					map[bool]string{true: "OFFERS", false: "HIDES"}[offered])
			}
		})
	}
}

// TestAScreenRefusesTheOperatorWhoLacksItsPrivilege pins the refusal itself.
//
// The status and the SENTENCE both: 403 rather than 404 because the screen exists
// and the answer is about the account (the split [corehttp.RequireScope] already
// makes for the API), and the missing privilege is NAMED because an operator who
// is not told which grant is absent has to ask somebody to guess.
func TestAScreenRefusesTheOperatorWhoLacksItsPrivilege(t *testing.T) {
	t.Parallel()

	_, r := panelFor(t, nil, scopeOrderRead)

	rec := get(r, CustomersPath)

	require.Equal(t, http.StatusForbidden, rec.Code,
		"an operator carrying only %q read the customers. The API refuses them the same "+
			"data (every /admin/v1 route names a scope), so the panel is a second door "+
			"into what the first one declined", scopeOrderRead)
	assert.Contains(t, rec.Body.String(), scopeCustomerRead,
		"the refusal must name the privilege that is missing")
}

// TestTheEntryPointSendsTheOperatorToAScreenTheyCanOpen covers the door.
//
// It used to redirect to the catalog unconditionally, which was right while every
// signed-in operator could open everything. With privileges it would answer 403
// at the front door — right after a successful sign-in, because the login's
// fallback target is that door.
func TestTheEntryPointSendsTheOperatorToAScreenTheyCanOpen(t *testing.T) {
	t.Parallel()

	t.Run("the first screen the grants open", func(t *testing.T) {
		t.Parallel()

		// The customers sit FOURTH in the menu, so a fixed redirect to the
		// catalog cannot pass this by accident.
		_, r := panelFor(t, nil, scopeCustomerRead)

		rec := get(r, URLPrefix)

		require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
		assert.Equal(t, CustomersPath, rec.Header().Get("Location"),
			"the door must open on a room the operator may enter")
	})

	t.Run("a registered screen counts as one", func(t *testing.T) {
		t.Parallel()

		// An operator whose ONLY grant is a plugin's: the door has to know about
		// the registered screens too, or a shop whose operator was granted
		// exactly the plugin's privilege finds the panel shut.
		_, r := panelFor(t, []Page{testPage()}, testPageScope)

		rec := get(r, URLPrefix)

		require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
		assert.Equal(t, testPage().Path, rec.Header().Get("Location"))
	})

	t.Run("nothing at all", func(t *testing.T) {
		t.Parallel()

		_, r := panelFor(t, nil)

		rec := get(r, URLPrefix)

		// An account no grant opens a screen for can still protect itself, and
		// it is the one thing an account that owes a second factor has to do
		// (ADR 0266); the screen tells it no other screen opens.
		require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
		assert.Equal(t, SecondFactorPath, rec.Header().Get("Location"))
	})
}

// TestARegisteredScreensScriptCarriesTheScreensPrivilege is the half that is easy
// to leave open.
//
// The script is bytes the panel serves from its OWN origin to a browser holding
// an operator's session. A plugin is free to put anything in there, and there is
// no reason to hand it to an account that could not open the screen it belongs
// to. The shell and the script are bound in one loop for exactly this: they
// cannot end up with different answers.
func TestARegisteredScreensScriptCarriesTheScreensPrivilege(t *testing.T) {
	t.Parallel()

	_, r := panelFor(t, []Page{testPage()}, scopeOrderRead)

	assert.Equal(t, http.StatusForbidden, get(r, testPage().Path).Code,
		"the shell opened for an operator without the screen's privilege")
	assert.Equal(t, http.StatusForbidden, get(r, testPage().Path+scriptSuffix).Code,
		"the shell was refused and its script was served anyway; the two are bound from "+
			"one loop so that they cannot disagree")
}

// TestEachRouteDemandsThePrivilegeItsOwnPathIsListedUnder is the second walk.
//
// # What it catches that the first one cannot
//
// internal/app walks the router as an operator carrying NOTHING and requires a
// refusal everywhere, which proves each route is checked. It cannot see WHICH
// privilege a route checks: the path is named twice on every binding line — once
// to bind and once to ask the table — and a line pairing one screen's path with
// another's handler refuses the unprivileged operator just as correctly while
// demanding the wrong grant from everybody else.
//
// So each route is requested by an operator holding EXACTLY the scope its own
// pattern is listed under, and a refusal is the failure. The population is the
// router's, not a list kept here.
func TestEachRouteDemandsThePrivilegeItsOwnPathIsListedUnder(t *testing.T) {
	t.Parallel()

	ui, _ := panelFor(t, []Page{testPage()})
	bound := chi.NewRouter()
	ui.Routes(bound)

	type route struct{ method, pattern string }
	var routes []route
	require.NoError(t, chi.Walk(bound, func(
		method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler,
	) error {
		routes = append(routes, route{method: method, pattern: pattern})

		return nil
	}))
	require.GreaterOrEqual(t, len(routes), 22,
		"the walk found %d routes; a blind walk asserts nothing about any of them",
		len(routes))

	checked := 0
	for _, bound := range routes {
		scope := ui.scopes[routeKey(bound.method, bound.pattern)]
		if scope == "" {
			continue
		}
		checked++

		t.Run(bound.method+" "+bound.pattern, func(t *testing.T) {
			_, r := panelFor(t, []Page{testPage()}, scope)

			// The route's OWN method. Asking a POST-only path with GET would get
			// 405 from the router, and 405 is not 403 — every one of those routes
			// would pass without its privilege ever being looked at.
			path := strings.NewReplacer(
				"{id}", "probe", "{variantID}", "probe").Replace(bound.pattern)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(bound.method, path, http.NoBody))

			assert.NotEqual(t, http.StatusForbidden, rec.Code,
				"%s %s is listed under %q and refused an operator holding exactly that. The "+
					"binding line names its path twice; one of the two is another screen's",
				bound.method, bound.pattern, scope)
		})
	}

	// TWELVE routes carry none, and each is deliberate: the login page on both
	// verbs, the sign-out, the stylesheet and the panel's entry point (which
	// holds no data and refuses by having nowhere to send the operator), the
	// person's own second factor on its four (ADR 0266) and their own sessions
	// on three (ADR 0268), which ask for a session and no privilege. An exact
	// count rather than a floor: a thirteenth open route would otherwise join
	// them silently.
	const openRoutes = 12
	assert.Equal(t, len(routes)-openRoutes, checked,
		"%d of %d routes carry a privilege; %d are open, and only twelve are meant to be",
		checked, len(routes), len(routes)-checked)

	// And the table lists nothing the router does not bind: a stale entry would
	// read as a decision about a route that no longer exists (ADR 0255).
	walked := make(map[string]bool, len(routes))
	for _, bound := range routes {
		walked[routeKey(bound.method, bound.pattern)] = true
	}
	for key := range ui.scopes {
		assert.True(t, walked[key], "the scope table lists %s, which the panel does not bind", key)
	}
}

// TestARouteTheTableDoesNotListCannotBeBound is ADR 0255: a route is its
// method and its path, and one the table does not list stops the panel from
// being built. A POST on a read path therefore inherits nothing — neither the
// read privilege nor an open door.
func TestARouteTheTableDoesNotListCannotBeBound(t *testing.T) {
	t.Parallel()

	ui := &UI{scopes: builtInScopes()}
	handler := func(http.ResponseWriter, *http.Request) {}

	assert.PanicsWithValue(t,
		"adminui: POST "+OrdersPath+" is bound with no entry in the scope table; "+
			"list it in builtInScopes with the privilege it needs, or with none",
		func() { ui.needs(http.MethodPost, OrdersPath, handler) },
		"a POST on the orders' read path was bound without a decision")
	assert.NotPanics(t, func() { ui.needs(http.MethodGet, OrdersPath, handler) })
	assert.NotPanics(t, func() { ui.needs(http.MethodPost, LogoutPath, handler) },
		"an open route is listed, with no privilege, and binds")
}

// TestSigningOutNeedsNoPrivilege is the counterpart of every refusal above.
//
// An operator whose grants open no screen must still be able to clear their own
// session. Putting the sign-out behind a privilege would leave exactly that
// account signed in with no way out — and the panel would be holding a live
// session for somebody it refuses to show anything to.
func TestSigningOutNeedsNoPrivilege(t *testing.T) {
	t.Parallel()

	_, r := panelFor(t, nil)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, LogoutPath, http.NoBody))

	assert.NotEqual(t, http.StatusForbidden, rec.Code,
		"an operator with no privilege could not sign out")
}

// TestTheTelephoneOrderAsksForTheCartsPrivileges pins the four routes of ADR
// 0290: the form that opens a cart and both writes ask for cart:write, and the
// cart's page asks for cart:read. The walk of ADR 0260 cannot tell the two
// apart, because both belong to the cart module.
func TestTheTelephoneOrderAsksForTheCartsPrivileges(t *testing.T) {
	t.Parallel()

	post := func(r chi.Router, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, http.NoBody)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.ServeHTTP(rec, req)

		return rec
	}
	cartPage := CartsPath + "/cart_phone"

	_, reader := panelFor(t, nil, scopeCartRead)
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"the opening form": get(reader, CartsPath),
		"the opening":      post(reader, CartsPath),
		"a line":           post(reader, cartPage+"/lines"),
	} {
		assert.Equal(t, http.StatusForbidden, rec.Code, "%s opened for a reader", name)
		assert.Contains(t, rec.Body.String(), scopeCartWrite, name)
	}
	assert.NotEqual(t, http.StatusForbidden, get(reader, cartPage).Code, "a reader reads the cart")

	_, writer := panelFor(t, nil, scopeCartWrite)
	assert.Equal(t, http.StatusOK, get(writer, CartsPath).Code, "a writer opens the form")
	page := get(writer, cartPage)
	assert.Equal(t, http.StatusForbidden, page.Code, "the page is the read's")
	assert.Contains(t, page.Body.String(), scopeCartRead)
}
