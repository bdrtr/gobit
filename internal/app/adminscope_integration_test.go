//go:build integration

package app

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/module"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	"github.com/bdrtr/gobit/plugins/analytics"
	"github.com/bdrtr/gobit/plugins/errorotlp"
	"github.com/bdrtr/gobit/plugins/errorsentry"
	"github.com/bdrtr/gobit/plugins/paymentpaytr"
	"github.com/bdrtr/gobit/plugins/searchpg"
)

// The gate in an installation rather than on a router built for it (ADR 0434):
// every installation this tree ships passes it, a module's or a plugin's
// unscoped admin route stops one, and every exemption names a route the auth
// module really binds. The rules themselves are adminscope_test.go's.

// TestEveryInstallationThisTreeShipsDemandsAPrivilegeOnEveryAdminRoute opens
// the default installation and one with every plugin in the catalog.
//
// The two error reporters fill one slot and refuse to share it, so "every
// plugin" is two installations, one per reporter; neither binds a route.
func TestEveryInstallationThisTreeShipsDemandsAPrivilegeOnEveryAdminRoute(t *testing.T) {
	setUpGateInstallation(t)

	t.Run("the default installation", func(t *testing.T) {
		t.Setenv("PLUGINS", "")

		router, err := openGateInstallation(t, Options{})
		require.NoError(t, err, "the default installation does not pass its own gate")

		bound := boundRoutes(t, router)
		for route := range ownAdminRoutes {
			assert.True(t, bound[route],
				"%s %s is exempt from the gate and nothing binds it; an exemption outliving "+
					"its route is one the next route on that path inherits", route.Method, route.Path)
		}
	})

	settings := everyPluginsSettings(t)
	for _, reporter := range []string{errorsentry.Name, errorotlp.Name} {
		t.Run("every plugin, reporting through "+reporter, func(t *testing.T) {
			for name, value := range settings {
				t.Setenv(name, value)
			}
			names := slices.DeleteFunc(pluginNames(), func(name string) bool {
				return (name == errorsentry.Name || name == errorotlp.Name) && name != reporter
			})
			require.Len(t, names, len(pluginCatalog)-1)
			t.Setenv("PLUGINS", strings.Join(names, ","))

			router, err := openGateInstallation(t, Options{})
			require.NoError(t, err, "an installation with %s does not pass the gate", strings.Join(names, ", "))

			// One admin route of each plugin that binds some, so a pass is the
			// plugins' routes passing rather than the plugins not being there.
			bound := boundRoutes(t, router)
			for _, route := range []AdminRoute{
				{http.MethodGet, analytics.FunnelPath},
				{http.MethodGet, paymentpaytr.PendingPath},
				{http.MethodPost, searchpg.ReindexPath},
				{http.MethodGet, "/admin/v1/webhooks/"},
				{http.MethodGet, "/admin/v1/webpush/subscriptions"},
			} {
				assert.True(t, bound[route], "%s %s is not bound; the plugins were not installed",
					route.Method, route.Path)
			}
		})
	}
}

// boundRoutes answers every route a router binds.
func boundRoutes(t *testing.T, router chi.Router) map[AdminRoute]bool {
	t.Helper()

	bound := map[AdminRoute]bool{}
	require.NoError(t, chi.Walk(router, func(
		method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler,
	) error {
		bound[AdminRoute{Method: method, Path: pattern}] = true

		return nil
	}))

	return bound
}

// TestAnUnscopedAdminRouteStopsTheInstallationThatBindsIt is D270 through the
// assembly: a module or a plugin from outside the tree binds an operator
// endpoint that demands nothing, and the installation does not start.
func TestAnUnscopedAdminRouteStopsTheInstallationThatBindsIt(t *testing.T) {
	setUpGateInstallation(t)
	t.Setenv("PLUGINS", "")

	for _, tc := range []struct {
		name  string
		opts  Options
		route string
	}{
		{
			name:  "a module's route",
			opts:  Options{Modules: []module.Module{unscopedModule{}}},
			route: "PUT /admin/v1/gate-credentials",
		},
		{
			name:  "a plugin's route",
			opts:  Options{Plugins: []coreplugin.Plugin{unscopedPlugin{}}},
			route: "POST /admin/v1/gate-impersonation",
		},
		{
			name:  "a percent-encoded prefix",
			opts:  Options{Modules: []module.Module{shapedModule{bind: putAt("/admin%2Fv1/gate-takeover")}}},
			route: "PUT /admin%2Fv1/gate-takeover",
		},
		{
			name:  "an exempt route served by a module bound after the auth module",
			opts:  Options{Modules: []module.Module{shapedModule{bind: getAt("/admin/v1/auth/me")}}},
			route: "GET /admin/v1/auth/me",
		},
		{
			name:  "a module's route under the panel's address",
			opts:  Options{Modules: []module.Module{shapedModule{bind: getAt("/admin/ui/gate-page")}}},
			route: "GET /admin/ui/gate-page",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := openGateInstallation(t, tc.opts)

			require.Error(t, err, "an installation started with %s demanding no privilege", tc.route)
			assert.Equal(t, codeAdminRouteUnscoped, errors.CodeOf(err), err.Error())
			assert.Contains(t, err.Error(), tc.route)
		})
	}
}

// TestAModulesFallbackDoesNotAnswerTheAdminSurfaces is the review's F3 shapes
// through the assembly: a module's not-found and method-not-allowed handlers
// are not routes, so no walk sees them, and they, like a page router or a
// single-page app at the root, answered every unbound path and method under
// /admin/v1 to any principal the ring let in. The installation starts with all
// of them, and gobit answers the admin surface.
func TestAModulesFallbackDoesNotAnswerTheAdminSurfaces(t *testing.T) {
	setUpGateInstallation(t)
	t.Setenv("PLUGINS", "")
	t.Setenv("ADMIN_BOOTSTRAP_EMAIL", "owner@gate.test")
	t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", "the-gate-owner-password")

	answered := &atomic.Int32{}
	fallback := func(w http.ResponseWriter, _ *http.Request) {
		answered.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}
	router, err := openGateInstallation(t, Options{Modules: []module.Module{shapedModule{bind: func(r chi.Router) {
		r.NotFound(fallback)
		r.MethodNotAllowed(fallback)
		r.Get("/gate-shop", noContent)
		r.Get("/{category}/{product}", fallback)
		r.Put("/{surface}/v1/{rest}", fallback)
		r.Handle("/*", http.HandlerFunc(fallback))
	}}}})
	require.NoError(t, err, "a fallback, a page router and a single-page app at the root start")

	token := gateOwnerToken(t, router)
	for _, c := range []struct {
		method, path string
		status       int
	}{
		{http.MethodPut, "/admin/v1/gate-anything", http.StatusNotFound},
		{http.MethodPut, "/admin/v1/customer-credentials", http.StatusNotFound},
		{http.MethodGet, "/admin/v1", http.StatusNotFound},
		{http.MethodGet, "/admin/v1/a/b", http.StatusNotFound},
		{http.MethodDelete, "/admin/v1/auth/me", http.StatusMethodNotAllowed},
	} {
		rec := gateRequest(router, c.method, c.path, token)
		assert.Equal(t, c.status, rec.Code, "%s %s: %s", c.method, c.path, rec.Body.String())
	}
	assert.Zero(t, answered.Load(), "the module's fallback answered a path on the admin surface")

	assert.Equal(t, http.StatusNoContent, gateRequest(router, http.MethodGet, "/shoes/red", "").Code)
	assert.Equal(t, http.StatusNoContent, gateRequest(router, http.MethodGet, "/a/deep/spa/path", "").Code)
	assert.Equal(t, int32(2), answered.Load(), "the storefront keeps its own paths")
}

// gateOwnerToken signs the bootstrapped owner in over the router.
func gateOwnerToken(t *testing.T, router http.Handler) string {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/admin/v1/auth/login",
		strings.NewReader(`{"email":"owner@gate.test","password":"the-gate-owner-password"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var answer struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &answer))
	require.NotEmpty(t, answer.Data.Token)

	return answer.Data.Token
}

// gateRequest sends one request, carrying the token when there is one.
func gateRequest(router http.Handler, method, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "gate-"+method+path)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	return rec
}

// setUpGateInstallation points the configuration at a database of its own.
func setUpGateInstallation(t *testing.T) {
	t.Helper()

	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", migrateDSN(t))
	t.Setenv("JWT_SECRET", "admin-scope-gate-test-secret-32-bytes-long")
	t.Setenv("LOG_LEVEL", "error")
}

// openGateInstallation opens an installation through [Open], the assembly the
// server, the facade and the end-to-end ground share, and answers its router or
// the reason it did not start.
func openGateInstallation(t *testing.T, opts Options) (chi.Router, error) {
	t.Helper()

	opts.Version = "gate"
	installation, closeAll, err := Open(t.Context(), opts)
	if err != nil {
		return nil, err
	}
	t.Cleanup(closeAll)

	return installation.Router, nil
}

// everyPluginsSettings answers what each plugin in the catalog needs to install.
//
// None of it reaches a network while the installation opens: the providers are
// registered, not called. A plugin added to the catalog without a line here
// fails the test above by naming its missing setting, which is the moment to
// add one.
func everyPluginsSettings(t *testing.T) map[string]string {
	t.Helper()

	smtpCopy := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(smtpCopy, "order.placed.tmpl"),
		[]byte(`{{define "subject"}}Order{{end}}{{define "body"}}Thank you.{{end}}`), 0o600))
	pushCopy := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(pushCopy, "order.placed.tmpl"),
		[]byte(`{{define "title"}}Order{{end}}{{define "body"}}placed{{end}}`), 0o600))
	privateKey, _ := generatedPair(t)

	return map[string]string{
		"ANTHROPIC_API_KEY":         "sk-ant-not-a-real-key",
		"ANTHROPIC_MODEL":           "a-model-nobody-calls",
		"SENTRY_DSN":                "https://public@sentry.example.test/1",
		"OTLP_LOGS_ENDPOINT":        "http://127.0.0.1:4318/v1/logs",
		"S3_BUCKET":                 "gobit-gate",
		"S3_REGION":                 "eu-central-1",
		"S3_ACCESS_KEY_ID":          "not-a-key-id",
		"S3_SECRET_ACCESS_KEY":      "not-a-secret",
		"SMTP_HOST":                 "smtp.example.test",
		"SMTP_FROM":                 "Store <no-reply@example.test>",
		"SMTP_TEMPLATE_DIR":         smtpCopy,
		"PAYTR_MERCHANT_ID":         "000000",
		"PAYTR_MERCHANT_KEY":        "not-a-key",
		"PAYTR_MERCHANT_SALT":       "not-a-salt",
		"PAYTR_SUCCESS_URL":         "https://shop.example.test/paid",
		"PAYTR_FAILURE_URL":         "https://shop.example.test/not-paid",
		"STRIPE_API_KEY":            "sk_test_not_a_real_key",
		"WEBPUSH_VAPID_PRIVATE_KEY": privateKey,
		"WEBPUSH_VAPID_SUBJECT":     "mailto:ops@example.test",
		"WEBPUSH_TEMPLATE_DIR":      pushCopy,
	}
}

// unscopedModule is a module from outside the tree that binds an operator
// endpoint behind the admin ring and demands nothing of the operator, D270's
// shape.
type unscopedModule struct{}

func (unscopedModule) Name() string { return "gate_unscoped" }

func (unscopedModule) Register(context.Context, *container.Container) error { return nil }

func (unscopedModule) Migrations() fs.FS { return nil }

func (unscopedModule) Routes(r chi.Router) {
	r.Put("/admin/v1/gate-credentials", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
}

// shapedModule is a module from outside the tree that binds whatever shape a
// case gives it.
type shapedModule struct {
	bind func(r chi.Router)
}

func (shapedModule) Name() string { return "gate_shaped" }

func (shapedModule) Register(context.Context, *container.Container) error { return nil }

func (shapedModule) Migrations() fs.FS { return nil }

func (m shapedModule) Routes(r chi.Router) { m.bind(r) }

// putAt binds an unguarded write at a pattern.
func putAt(pattern string) func(chi.Router) {
	return func(r chi.Router) { r.Put(pattern, noContent) }
}

// getAt binds an unguarded read at a pattern.
func getAt(pattern string) func(chi.Router) {
	return func(r chi.Router) { r.Get(pattern, noContent) }
}

// noContent answers 204, as a write that went through would.
func noContent(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }

// unscopedPlugin is the same fault arriving through a plugin's routes, which
// are bound after every module's.
type unscopedPlugin struct{}

func (unscopedPlugin) Name() string { return "gate-unscoped" }

func (unscopedPlugin) Setup(_ context.Context, h *coreplugin.Host) error {
	h.AddRoutes(func(r chi.Router) {
		r.Post("/admin/v1/gate-impersonation", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
	})

	return nil
}
