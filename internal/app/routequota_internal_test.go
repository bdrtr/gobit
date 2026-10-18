package app

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	"github.com/bdrtr/gobit/internal/adminui"
	authapi "github.com/bdrtr/gobit/internal/modules/auth/api"
)

// quotaPlugin declares route quotas and nothing else.
type quotaPlugin struct {
	name   string
	quotas []coreplugin.RouteQuota
}

func (p quotaPlugin) Name() string { return p.name }

func (p quotaPlugin) Setup(_ context.Context, h *coreplugin.Host) error {
	for _, q := range p.quotas {
		h.RegisterRouteQuota(q)
	}

	return nil
}

// quotaHost is a host the plugins were installed on.
func quotaHost(t *testing.T, plugins ...coreplugin.Plugin) *coreplugin.Host {
	t.Helper()
	host := coreplugin.NewHost(nil, nil, nil, discardLogger(), nil)
	reg := coreplugin.NewRegistry(discardLogger())
	for _, p := range plugins {
		reg.Add(p)
	}
	require.NoError(t, reg.Install(t.Context(), host))

	return host
}

// TestTheSignInQuotaCountsEveryWayToTryAPassword is ADR 0452: on by default,
// the sign-in quota counts the API's sign-in, the invitation's acceptance and
// the panel's form together, and ADMIN_SIGN_IN_LIMIT=0 turns it off.
func TestTheSignInQuotaCountsEveryWayToTryAPassword(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.AdminSignInLimit, cfg.AdminSignInWindow = 20, 15*time.Minute
	declared, err := routeQuotaDeclarations(cfg, nil)
	require.NoError(t, err)
	require.Len(t, declared, 1)
	assert.Equal(t, adminSignInQuota, declared[0].name)
	assert.ElementsMatch(t, []string{
		"POST " + authapi.LoginPath, "POST " + authapi.AcceptInvitationPath, "POST " + adminui.LoginPath,
	}, declared[0].routes)
	assert.Equal(t, 20, declared[0].limit)

	cfg.AdminSignInLimit = 0
	declared, err = routeQuotaDeclarations(cfg, nil)
	require.NoError(t, err)
	assert.Empty(t, declared)
}

// TestAPluginsRouteQuotaIsCheckedAtStartup is ADR 0452: a quota whose name,
// route, limit or window cannot be held, or whose name another quota has,
// stops the installation naming the plugin.
func TestAPluginsRouteQuotaIsCheckedAtStartup(t *testing.T) {
	t.Parallel()

	good := coreplugin.RouteQuota{
		Name: "dtm.returns", Routes: []string{"POST /store/v1/orders/{id}/returns"}, Limit: 5, Window: time.Minute,
	}
	cfg := baseConfig()
	cfg.AdminSignInLimit, cfg.AdminSignInWindow = 20, 15*time.Minute
	declared, err := routeQuotaDeclarations(cfg, quotaHost(t, quotaPlugin{name: "dtm", quotas: []coreplugin.RouteQuota{good}}))
	require.NoError(t, err)
	require.Len(t, declared, 2)
	assert.Equal(t, "the dtm plugin", declared[1].by)

	bad := map[string]coreplugin.RouteQuota{
		"a name a key cannot take": {Name: "dtm returns", Routes: good.Routes, Limit: 5, Window: time.Minute},
		"the sign-in quota's name": {Name: "Admin-Sign-In", Routes: good.Routes, Limit: 5, Window: time.Minute},
		"no route":                 {Name: "dtm.none", Limit: 5, Window: time.Minute},
		"a catch-all":              {Name: "dtm.all", Routes: []string{"POST /store/v1/*"}, Limit: 5, Window: time.Minute},
		"no limit":                 {Name: "dtm.zero", Routes: good.Routes, Window: time.Minute},
		"no window":                {Name: "dtm.ever", Routes: good.Routes, Limit: 5},
	}
	for label, q := range bad {
		_, err := routeQuotaDeclarations(cfg, quotaHost(t, quotaPlugin{name: "dtm", quotas: []coreplugin.RouteQuota{q}}))
		require.Error(t, err, label)
		assert.Equal(t, corehttp.CodeRouteQuotaInvalid, errors.CodeOf(err), label)
		assert.Contains(t, err.Error(), "dtm plugin", "%s: the refusal names the plugin", label)
	}
}

// TestAQuotaOnARouteNothingBindsStopsTheInstallation is ADR 0452: once
// everything is bound, a quota naming a route the router does not serve, a
// renamed parameter included, is refused with the route and who declared it.
func TestAQuotaOnARouteNothingBindsStopsTheInstallation(t *testing.T) {
	t.Parallel()

	r := chi.NewRouter()
	r.Post("/store/v1/orders/{id}/returns", func(http.ResponseWriter, *http.Request) {})
	r.Route("/admin/v1/auth", func(sub chi.Router) {
		sub.Post("/login", func(http.ResponseWriter, *http.Request) {})
	})

	bound := []declaredQuota{{
		name: "q", routes: []string{"POST /store/v1/orders/{id}/returns", "POST /admin/v1/auth/login"}, by: "the dtm plugin",
	}}
	require.NoError(t, refuseUnboundRouteQuotas(r, bound), "a route under a mounted router is bound too")

	for _, route := range []string{"POST /store/v1/orders/{order_id}/returns", "PUT /admin/v1/auth/login", "POST /admin/v1/auth/logins"} {
		err := refuseUnboundRouteQuotas(r, []declaredQuota{{name: "q", routes: []string{route}, by: "the dtm plugin"}})
		require.Error(t, err, route)
		assert.Equal(t, corehttp.CodeRouteQuotaInvalid, errors.CodeOf(err))
		assert.Contains(t, err.Error(), route)
		assert.Contains(t, err.Error(), "dtm plugin")
	}
	require.NoError(t, refuseUnboundRouteQuotas(r, nil))
}

// TestTheSignInQuotaIsCountedPerClientInTheRealStack is ADR 0452 on the
// composition root's guard stack: the quota is keyed as the whole-API limit
// is (TRUSTED_PROXY_HOPS), counted with that limit off, and refuses before
// identity.
func TestTheSignInQuotaIsCountedPerClientInTheRealStack(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.RateLimitPerMinute = 0
	cfg.TrustedProxyHops = 1
	cfg.AdminSignInLimit, cfg.AdminSignInWindow = 2, time.Hour
	declared, err := routeQuotaDeclarations(cfg, nil)
	require.NoError(t, err)
	guards, _, err := guardStack(cfg, &corehttp.DeferredAuthenticator{}, &adminui.Ring{}, nil, nil, discardLogger(),
		nil, nil, declared...)
	require.NoError(t, err)

	reached := 0
	r := corehttp.NewRouter(corehttp.RouterOptions{Version: "test", Middlewares: guards})
	r.Post(authapi.LoginPath, func(http.ResponseWriter, *http.Request) { reached++ })
	r.Post(authapi.AcceptInvitationPath, func(http.ResponseWriter, *http.Request) { reached++ })

	signIn := func(path, client string) int {
		req := httptest.NewRequest(http.MethodPost, path, http.NoBody)
		req.Header.Set("X-Forwarded-For", client)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		return rec.Code
	}
	assert.Equal(t, http.StatusOK, signIn(authapi.LoginPath, "203.0.113.1"))
	assert.Equal(t, http.StatusOK, signIn(authapi.AcceptInvitationPath, "203.0.113.1"))
	assert.Equal(t, http.StatusTooManyRequests, signIn(authapi.LoginPath, "203.0.113.1"),
		"signing in and accepting an invitation spend one quota")
	assert.Equal(t, http.StatusOK, signIn(authapi.LoginPath, "203.0.113.2"), "another client behind the proxy")
	assert.Equal(t, 3, reached)
}

// TestTheAdminSurfaceAnswersAListedPanelsPreflight is ADR 0451 on the
// composition root: ADMIN_CORS_ALLOWED_ORIGINS reaches the admin surface, and
// a preflight is answered before the identity guard.
func TestTheAdminSurfaceAnswersAListedPanelsPreflight(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.AdminCORSAllowedOrigins = []string{"https://admin.shop.example"}
	h := guardedRouter(t, cfg, &corehttp.DeferredAuthenticator{})

	req := httptest.NewRequest(http.MethodOptions, "/admin/v1/users", http.NoBody)
	req.Header.Set("Origin", "https://admin.shop.example")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	req.Header.Set("Access-Control-Request-Headers", "authorization")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	assert.Equal(t, "https://admin.shop.example", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Contains(t, rec.Header().Get("Access-Control-Allow-Headers"), "Authorization")

	cfg.AdminCORSAllowedOrigins = nil
	rec = httptest.NewRecorder()
	guardedRouter(t, cfg, &corehttp.DeferredAuthenticator{}).ServeHTTP(rec, req)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"), "no list, no CORS on the admin surface")
}

// TestTheSignInQuotaWarnsAboutTheKeyBehindAProxy is ADR 0452: in a shared
// environment a sign-in quota keyed on the connection is reported, even with
// the whole-API limit off, and is silent once the proxies are counted.
func TestTheSignInQuotaWarnsAboutTheKeyBehindAProxy(t *testing.T) {
	t.Parallel()

	const message = "the admin sign-in quota falls on the CONNECTION, not the client"
	cfg := baseConfig()
	cfg.AppEnv = "production"
	cfg.RateLimitPerMinute = 0
	cfg.AdminSignInLimit, cfg.AdminSignInWindow = 20, 15*time.Minute
	catcher := &recordCatcher{}
	_, _, err := guardStack(cfg, validIdentity{}, &adminui.Ring{}, nil, nil, catcher.logger(), nil, nil)
	require.NoError(t, err, "the warning does not stop startup")
	assert.Contains(t, catcher.messages(slog.LevelWarn), message)

	cfg.TrustedProxyHops = 1
	catcher = &recordCatcher{}
	_, _, err = guardStack(cfg, validIdentity{}, &adminui.Ring{}, nil, nil, catcher.logger(), nil, nil)
	require.NoError(t, err)
	assert.NotContains(t, catcher.messages(slog.LevelWarn), message)
}
