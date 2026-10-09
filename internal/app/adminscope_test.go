package app

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
	authapi "github.com/bdrtr/gobit/internal/modules/auth/api"
)

// The gate's rules on routers built here, one shape at a time. That the full
// installation passes it, with every plugin, and that an installation refuses
// a module's unscoped route through the assembly, is the integration lane's
// (adminscope_integration_test.go).

// fakeAdminModule stands for a module or plugin binding admin routes; its
// handlers are methods so the refusal can be seen naming who bound a route,
// and it counts what its fallback answered.
type fakeAdminModule struct {
	fallbacks *atomic.Int32
}

func newFakeAdminModule() fakeAdminModule { return fakeAdminModule{fallbacks: &atomic.Int32{}} }

func (fakeAdminModule) writeCredential(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (fakeAdminModule) read(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// fallback is a module's own not-found or method-not-allowed handler, which
// writes as a credential route would.
func (m fakeAdminModule) fallback(w http.ResponseWriter, _ *http.Request) {
	m.fallbacks.Add(1)
	w.WriteHeader(http.StatusNoContent)
}

// passThrough is a middleware that demands nothing, standing for the route's
// own rate limit or decoder, so a guard further out is not the last one.
func passThrough(next http.Handler) http.Handler { return next }

// noPanel is an installation whose panel binds nothing.
type noPanel struct{}

func (noPanel) Binds(string, string) bool { return false }

// sendTo sends one request to a router.
func sendTo(r http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, path, http.NoBody))

	return rec
}

// TestAnUnscopedAdminRouteStopsTheInstallation is D270's shape: a module binds
// an operator endpoint behind the admin ring and demands nothing of the
// operator.
func TestAnUnscopedAdminRouteStopsTheInstallation(t *testing.T) {
	t.Parallel()

	m := newFakeAdminModule()
	r := chi.NewRouter()
	r.With(corehttp.RequireScope("fake:read")).Get("/admin/v1/fake", m.read)
	r.Put("/admin/v1/fake-credentials", m.writeCredential)
	r.Delete("/admin/v1/fake-credentials/{id}", m.writeCredential)

	err := refuseUnscopedAdminRoutes(r, noPanel{})

	require.Error(t, err, "an admin route demanding no privilege passed the gate")
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, codeAdminRouteUnscoped, errors.CodeOf(err))
	for _, route := range []string{"PUT /admin/v1/fake-credentials", "DELETE /admin/v1/fake-credentials/{id}"} {
		assert.Contains(t, err.Error(), route,
			"the refusal has to name every unscoped route, or the operator fixes one and meets the next")
	}
	assert.Contains(t, err.Error(), "internal/app.fakeAdminModule.writeCredential",
		"the refusal names the handler, whose package is the module that bound the route")
	assert.NotContains(t, err.Error(), "GET /admin/v1/fake ", "a guarded route is not named")
}

// TestAScopedAdminRoutePasses is the ordinary module, and the routes that can
// answer no path on the admin surfaces, which the gate has no question for.
func TestAScopedAdminRoutePasses(t *testing.T) {
	t.Parallel()

	m := newFakeAdminModule()
	r := chi.NewRouter()
	r.With(corehttp.RequireScope("customer-credential:write")).Put("/admin/v1/fake-credentials", m.writeCredential)
	r.With(corehttp.RequireScope("fake:read"), passThrough).Get("/admin/v1/fake", m.read)
	r.Get("/store/v1/fake", m.read)
	r.Get("/health", m.read)
	// A parameter matches one segment, so these reach neither /admin/v1 nor
	// /admin/ui: a storefront's page router at the root is not an admin route.
	r.Get("/{handle}", m.read)
	r.Get("/files/{key}", m.read)
	r.Put("/admin/v10/fake", m.writeCredential)
	r.Put("/admin/v1x*", m.writeCredential)
	r.Get("/admin/uix", m.read)

	assert.NoError(t, refuseUnscopedAdminRoutes(r, noPanel{}))
}

// TestThePersonsOwnRoutesPassAndNothingBesideThem holds the exemptions to the
// method and the path: a new route under /admin/v1/auth, or a second verb on an
// exempt path, decides its privilege like any other.
func TestThePersonsOwnRoutesPassAndNothingBesideThem(t *testing.T) {
	t.Parallel()

	own := chi.NewRouter()
	authapi.New(nil).Routes(own)
	assert.NoError(t, refuseUnscopedAdminRoutes(own, noPanel{}),
		"the auth module's own routes demand no privilege where they act on the caller alone")

	m := newFakeAdminModule()
	for _, beside := range []AdminRoute{
		{http.MethodPost, "/admin/v1/auth/impersonate"},
		{http.MethodGet, authapi.LoginPath},
		{http.MethodDelete, authapi.SessionsPath},
	} {
		r := chi.NewRouter()
		r.Method(beside.Method, beside.Path, http.HandlerFunc(m.writeCredential))

		err := refuseUnscopedAdminRoutes(r, noPanel{})
		require.Error(t, err, "%s %s rode an exemption it is not named in", beside.Method, beside.Path)
		assert.Contains(t, err.Error(), beside.Method+" "+beside.Path)
	}
}

// TestAnExemptRouteServedByAnotherHandlerIsRefused is the review's F4: chi
// replaces a route bound twice, so a module bound after the auth module that
// binds GET /admin/v1/auth/me serves every admin principal its own handler
// under the exemption the auth module's handler earned.
func TestAnExemptRouteServedByAnotherHandlerIsRefused(t *testing.T) {
	t.Parallel()

	m := newFakeAdminModule()
	r := chi.NewRouter()
	authapi.New(nil).Routes(r)
	r.Get("/admin/v1/auth/me", m.read)

	err := refuseUnscopedAdminRoutes(r, noPanel{})

	require.Error(t, err, "a handler that is not the auth module's rode the exemption")
	assert.Contains(t, err.Error(), "GET /admin/v1/auth/me")
	assert.Contains(t, err.Error(), "internal/app.fakeAdminModule.read")
}

// TestAGuardOnAGroupCounts: a guard installed once for a sub-router, a mounted
// router or a group guards each route in it, as chi serves them.
func TestAGuardOnAGroupCounts(t *testing.T) {
	t.Parallel()

	m := newFakeAdminModule()
	guarded := chi.NewRouter()
	guarded.Route("/admin/v1/fake", func(r chi.Router) {
		r.Use(corehttp.RequireScope("fake:write"))
		r.With(passThrough).Put("/{id}", m.writeCredential)
		r.Get("/", m.read)
	})
	guarded.Group(func(g chi.Router) {
		g.Use(corehttp.RequireScope("fake:read"))
		g.With(passThrough).Get("/admin/v1/fake-group", m.read)
	})
	mounted := chi.NewRouter()
	mounted.Use(corehttp.RequireScope("fake:write"))
	mounted.With(passThrough).Post("/", m.writeCredential)
	guarded.Mount("/admin/v1/fake-mounted", mounted)

	assert.NoError(t, refuseUnscopedAdminRoutes(guarded, noPanel{}), "a guard on a group was not counted")

	// The same shapes with the group's guard taken off are refused, so the walk
	// reached each route rather than passing a tree it never read.
	bare := chi.NewRouter()
	bare.Route("/admin/v1/fake", func(r chi.Router) {
		r.Use(passThrough)
		r.Put("/{id}", m.writeCredential)
	})
	bare.Group(func(g chi.Router) {
		g.Use(passThrough)
		g.Get("/admin/v1/fake-group", m.read)
	})
	bareMounted := chi.NewRouter()
	bareMounted.Post("/", m.writeCredential)
	bare.Mount("/admin/v1/fake-mounted", bareMounted)

	err := refuseUnscopedAdminRoutes(bare, noPanel{})
	require.Error(t, err)
	for _, route := range []string{
		"PUT /admin/v1/fake/{id}", "GET /admin/v1/fake-group", "POST /admin/v1/fake-mounted/",
	} {
		assert.Contains(t, err.Error(), route)
	}
}

// The admin surfaces are gobit's to answer (ADR 0434): gobit binds each
// prefix and everything below it that no route of its own takes, and chi
// matches a static segment before a parameter, so a pattern bound elsewhere
// answers no admin path however it is spelled. The first gate refused such
// patterns by what they could reach instead, and with them a storefront's own
// page routers.

// publicShapes binds the routes a storefront serves at the root, and the shapes
// the review served a product:read key through, each counting the requests
// that reached it.
func publicShapes(r chi.Router, reached *atomic.Int32) {
	page := func(w http.ResponseWriter, _ *http.Request) {
		reached.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}
	r.Get("/{category}/{product}", page)
	r.Get("/{lang}/{category}/{slug}", page)
	r.Get("/{year:[0-9]{4}}/{slug}", page)
	r.Handle("/*", http.HandlerFunc(page))
	r.Put("/{surface}/v1/credentials", page)
	r.Put("/admin/v1*", page)
	r.Put("/admin/ui*", page)
}

// TestAPublicRouterStartsAndAnswersNoAdminPath: a page router, a localized one,
// a dated one and a single-page app at the root start as they are, keep their
// own paths, and answer nothing under /admin/v1 or /admin/ui.
func TestAPublicRouterStartsAndAnswersNoAdminPath(t *testing.T) {
	t.Parallel()

	reached := &atomic.Int32{}
	r := chi.NewRouter()
	r.With(corehttp.RequireScope("fake:read")).Get("/admin/v1/fake", newFakeAdminModule().read)
	publicShapes(r, reached)

	require.NoError(t, refuseUnscopedAdminRoutes(r, noPanel{}),
		"a storefront's routes at the root are not admin routes")
	require.NoError(t, ownAdminSurfaces(r))

	for _, c := range []struct{ method, path string }{
		{http.MethodPut, "/admin/v1/x"},
		{http.MethodGet, "/admin/v1"},
		{http.MethodPut, "/admin/v1/"},
		{http.MethodGet, "/admin/v1/a/b"},
		{http.MethodPut, "/admin/v1/credentials"},
		{http.MethodGet, "/admin/ui/x"},
		{http.MethodPut, "/admin/ui"},
	} {
		rec := sendTo(r, c.method, c.path)
		assert.Equal(t, http.StatusNotFound, rec.Code, "%s %s", c.method, c.path)
		assert.Contains(t, rec.Body.String(), codeRouteNotFound, "%s %s", c.method, c.path)
	}
	assert.Zero(t, reached.Load(), "a route bound outside the admin surfaces answered an admin path")

	for _, path := range []string{"/shoes/red", "/en/shoes/red", "/2026/hello", "/a/deep/spa/path"} {
		assert.Equal(t, http.StatusNoContent, sendTo(r, http.MethodGet, path).Code, path)
	}
	assert.Equal(t, int32(4), reached.Load(), "the storefront keeps its own paths")
}

// TestAModulesNotFoundDoesNotAnswerAnAdminPath: a module's not-found handler,
// set on the root or on a router mounted under /admin/v1, answered every
// unbound path there behind the identity ring alone. The prefix is gobit's,
// and so are the fallbacks of a router mounted under it; the module's root
// fallback keeps its own address.
func TestAModulesNotFoundDoesNotAnswerAnAdminPath(t *testing.T) {
	t.Parallel()

	m := newFakeAdminModule()
	r := chi.NewRouter()
	r.With(corehttp.RequireScope("fake:read")).Get("/admin/v1/fake", m.read)
	r.Get("/shop", m.read)
	r.Route("/admin/v1/fake-sub", func(s chi.Router) {
		s.Use(passThrough)
		s.NotFound(m.fallback)
		s.With(corehttp.RequireScope("fake:read")).Get("/", m.read)
	})
	r.Route("/admin/v1/fake-inherits", func(s chi.Router) {
		s.With(corehttp.RequireScope("fake:read")).Get("/", m.read)
	})
	r.NotFound(m.fallback)

	require.NoError(t, ownAdminSurfaces(r))

	for _, path := range []string{
		"/admin/v1/customer-credentials-v2", "/admin/v1/fake-sub/nothing-here",
		"/admin/v1/fake-inherits/nothing-here", adminui.URLPrefix + "/no-such-page",
	} {
		rec := sendTo(r, http.MethodPut, path)
		assert.Equal(t, http.StatusNotFound, rec.Code, path)
		assert.Contains(t, rec.Body.String(), codeRouteNotFound, path)
	}
	assert.Zero(t, m.fallbacks.Load(), "a module's fallback answered a path on the admin surfaces")

	assert.Equal(t, http.StatusNoContent, sendTo(r, http.MethodGet, "/shop/no-such-page").Code)
	assert.Equal(t, int32(1), m.fallbacks.Load(), "the module's fallback keeps its own address")
}

// TestAModulesMethodNotAllowedDoesNotAnswerAnAdminPath: a module's
// method-not-allowed handler answered DELETE on every admin route bound for GET
// alone. gobit answers 405 with the methods the route takes.
func TestAModulesMethodNotAllowedDoesNotAnswerAnAdminPath(t *testing.T) {
	t.Parallel()

	m := newFakeAdminModule()
	r := chi.NewRouter()
	r.With(corehttp.RequireScope("fake:read")).Get("/admin/v1/fake", m.read)
	r.Get("/shop", m.read)
	r.MethodNotAllowed(m.fallback)

	require.NoError(t, ownAdminSurfaces(r))

	rec := sendTo(r, http.MethodDelete, "/admin/v1/fake")
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, []string{http.MethodGet}, rec.Header().Values("Allow"))
	assert.Zero(t, m.fallbacks.Load(), "a module's fallback answered a method on an admin route")

	assert.Equal(t, http.StatusNoContent, sendTo(r, http.MethodDelete, "/shop").Code)
	assert.Equal(t, int32(1), m.fallbacks.Load(), "the module's fallback keeps its own address")
}

// wrappedMux is a router of a module's own type, a chi router embedded in it.
type wrappedMux struct{ *chi.Mux }

// TestARouterThatIsNotChisOwnTypeAnswersNoAdminPath is the review's N2(a): a
// router of a module's own type mounted at the root has fallbacks gobit cannot
// reach, and the prefix keeps them away from every admin path. Mounted under an
// admin prefix, where its fallbacks would answer, it stops the installation.
func TestARouterThatIsNotChisOwnTypeAnswersNoAdminPath(t *testing.T) {
	t.Parallel()

	m := newFakeAdminModule()
	r := chi.NewRouter()
	sub := wrappedMux{chi.NewRouter()}
	sub.NotFound(m.fallback)
	r.Mount("/{tenant}", sub)

	require.NoError(t, refuseUnscopedAdminRoutes(r, noPanel{}))
	require.NoError(t, ownAdminSurfaces(r))
	assert.Equal(t, http.StatusNotFound, sendTo(r, http.MethodPut, "/admin/v1/x").Code)
	assert.Zero(t, m.fallbacks.Load(), "a module's router answered an admin path")

	under := chi.NewRouter()
	wrapped := wrappedMux{chi.NewRouter()}
	wrapped.NotFound(m.fallback)
	under.Mount("/admin/v1/wrapped", wrapped)

	err := ownAdminSurfaces(under)
	require.Error(t, err, "a router whose fallbacks gobit cannot claim was mounted under the prefix")
	assert.Contains(t, err.Error(), "/admin/v1/wrapped")
}

// TestAPercentEncodedPatternIsRefused is the review's N2(b): chi matches a
// pattern's text against the raw path, while the guard stack's rings read the
// decoded one, so /admin%2Fv1/… is not under /admin/v1 to the gate and is to
// the ring.
func TestAPercentEncodedPatternIsRefused(t *testing.T) {
	t.Parallel()

	m := newFakeAdminModule()
	for _, pattern := range []string{"/admin%2Fv1/x", "/%61dmin/v1/x", "/admin%2Fui/x", "/shop%20front"} {
		r := chi.NewRouter()
		r.Put(pattern, m.writeCredential)

		err := refuseUnscopedAdminRoutes(r, noPanel{})
		require.Error(t, err, "%s passed the gate", pattern)
		assert.Contains(t, err.Error(), "PUT "+pattern)
	}
}

// TestTheAdminPrefixIsGobitsAlone: a route bound on the prefix's own catch-all,
// even under a privilege, and a router mounted above the prefix that carries
// routes under it would both be shadowed by gobit's, so the installation names
// them instead of losing them.
func TestTheAdminPrefixIsGobitsAlone(t *testing.T) {
	t.Parallel()

	m := newFakeAdminModule()
	guard := corehttp.RequireScope("fake:write")

	catchAll := chi.NewRouter()
	catchAll.With(guard).Handle("/admin/v1/*", http.HandlerFunc(m.writeCredential))
	require.NoError(t, refuseUnscopedAdminRoutes(catchAll, noPanel{}), "the route is guarded")
	err := ownAdminSurfaces(catchAll)
	require.Error(t, err, "a module bound the prefix's catch-all")
	assert.Contains(t, err.Error(), "/admin/v1/*")

	above := chi.NewRouter()
	sub := chi.NewRouter()
	sub.With(guard).Put("/admin/v1/mounted-above", m.writeCredential)
	above.Mount("/", sub)
	require.NoError(t, refuseUnscopedAdminRoutes(above, noPanel{}), "the route is guarded")
	err = ownAdminSurfaces(above)
	require.Error(t, err, "a router mounted above the prefix carries a route gobit's would shadow")
	assert.Contains(t, err.Error(), "/admin/v1/mounted-above")
}

// TestARouteUnderThePanelsAddressMustBeThePanels is the review's F6: a module's
// route under /admin/ui passes the panel's ring, which proves who is calling,
// and none of the panel's privileges, which are its scope table's.
func TestARouteUnderThePanelsAddressMustBeThePanels(t *testing.T) {
	t.Parallel()

	panel, r := panelRouter(t)
	require.NoError(t, refuseUnscopedAdminRoutes(r, panel), "the panel's own routes are the panel's")

	m := newFakeAdminModule()
	for _, tc := range []struct {
		name  string
		bind  func(r chi.Router)
		route string
	}{
		{
			name:  "a module's route the panel does not list",
			bind:  func(r chi.Router) { r.Post(adminui.URLPrefix+"/customers/{id}/password", m.writeCredential) },
			route: "POST " + adminui.URLPrefix + "/customers/{id}/password",
		},
		{
			name: "a module's route under a privilege of its own",
			bind: func(r chi.Router) {
				r.With(corehttp.RequireScope("fake:write")).Post(adminui.URLPrefix+"/fake", m.writeCredential)
			},
			route: "POST " + adminui.URLPrefix + "/fake",
		},
		{
			name:  "a module's handler on a path the panel lists",
			bind:  func(r chi.Router) { r.Post(adminui.LoginPath, m.writeCredential) },
			route: "POST " + adminui.LoginPath,
		},
		{
			name: "the panel's handler on a path the panel does not list",
			bind: func(r chi.Router) {
				r.Method(http.MethodGet, adminui.URLPrefix+"/elsewhere", panelHandler(t, r, http.MethodGet, adminui.LoginPath))
			},
			route: "GET " + adminui.URLPrefix + "/elsewhere",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			panel, r := panelRouter(t)
			tc.bind(r)

			err := refuseUnscopedAdminRoutes(r, panel)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.route)
		})
	}
}

// panelRouter binds a real panel on a router of its own.
func panelRouter(t *testing.T) (*adminui.UI, chi.Router) {
	t.Helper()

	c := container.New(discardLogger())
	identity := panelIdentity{token: "a-valid-admin-token"}
	require.NoError(t, c.Provide(adminui.ServiceQuery, panelCatalog{}))
	require.NoError(t, c.Provide(adminui.ServiceAuth, panelSession{token: identity.token}))
	require.NoError(t, c.Provide(adminui.InteropAuth, identity))

	panel, err := adminui.FromContainer(c, false, []adminui.Page{{
		Label: "Gate probe", Path: adminui.URLPrefix + "/gate-probe",
		Scope: "probe:read", Script: []byte("// probe\n"),
	}})
	require.NoError(t, err)

	r := chi.NewRouter()
	panel.Routes(r)

	return panel, r
}

// panelHandler reads the handler the panel bound for a route off the router.
func panelHandler(t *testing.T, r chi.Routes, method, path string) http.Handler {
	t.Helper()

	var found http.Handler
	require.NoError(t, chi.Walk(r, func(
		m, pattern string, handler http.Handler, _ ...func(http.Handler) http.Handler,
	) error {
		if m == method && pattern == path {
			found = handler
		}

		return nil
	}))
	require.NotNil(t, found, "the panel binds no %s %s", method, path)

	return found
}

// TestTheDocumentDoesNotOfferGobitsOwnAnswer: the served document walks the
// router, and gobit's own answer on the admin surfaces is not an endpoint a
// generated client should call. A route of the surface's on the same pattern,
// the panel's entry point among them, stays.
func TestTheDocumentDoesNotOfferGobitsOwnAnswer(t *testing.T) {
	t.Parallel()

	m := newFakeAdminModule()
	r := chi.NewRouter()
	r.With(corehttp.RequireScope("fake:read")).Get("/admin/v1/fake", m.read)
	r.Get(adminui.URLPrefix, m.read)
	require.NoError(t, ownAdminSurfaces(r))

	var walked []string
	require.NoError(t, chi.Walk(endpointRoutes{r}, func(
		method, pattern string, handler http.Handler, _ ...func(http.Handler) http.Handler,
	) error {
		assert.False(t, IsOwnedAnswer(handler), "%s %s is gobit's own answer", method, pattern)
		walked = append(walked, method+" "+pattern)

		return nil
	}))
	assert.ElementsMatch(t, []string{"GET /admin/v1/fake", "GET " + adminui.URLPrefix}, walked)
}
