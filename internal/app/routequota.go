// The route quotas (ADR 0452): the admin sign-in quota configuration sets, the
// quotas plugins declare, the backend that counts them, and the gate that
// refuses a quota on a route nothing serves. It is its own file because the
// guard stack only places the ring; what it counts is decided here.

package app

import (
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/http/redisguard"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	"github.com/bdrtr/gobit/internal/adminui"
	"github.com/bdrtr/gobit/internal/core/config"
	authapi "github.com/bdrtr/gobit/internal/modules/auth/api"
)

// adminSignInQuota is the name of the quota ADMIN_SIGN_IN_LIMIT sets.
const adminSignInQuota = "admin-sign-in"

// quotaSection is the key segment a quota's counters sit under on Redis, after
// the installation's prefix, apart from the whole-API limit's.
const quotaSection = ".quota."

// adminSignInRoutes are the routes the sign-in quota counts together: every
// way to try a password or an invitation's token at the admin surface, the
// API's and the panel's form alike, since both reach one account lock.
func adminSignInRoutes() []string {
	return []string{
		http.MethodPost + " " + authapi.LoginPath,
		http.MethodPost + " " + authapi.AcceptInvitationPath,
		http.MethodPost + " " + adminui.LoginPath,
	}
}

// declaredQuota is a route quota as configuration or a plugin declared it,
// before a backend counts it.
type declaredQuota struct {
	name   string
	routes []string
	limit  int
	window time.Duration
	// by says who declared it, for the refusals.
	by string
}

// routeQuotaDeclarations are the quotas the installation counts: the sign-in
// quota while ADMIN_SIGN_IN_LIMIT is on, then the plugins', in registration
// order. A quota whose name is not one a counter key takes, or is another's,
// whose route [corehttp.ParseQuotaRoute] refuses, or whose limit or window is
// not positive stops the installation with who declared it. A nil host is no
// plugins.
func routeQuotaDeclarations(cfg config.Config, host *coreplugin.Host) ([]declaredQuota, error) {
	var out []declaredQuota
	if cfg.AdminSignInLimit > 0 {
		out = append(out, declaredQuota{
			name: adminSignInQuota, routes: adminSignInRoutes(),
			limit: cfg.AdminSignInLimit, window: cfg.AdminSignInWindow, by: "ADMIN_SIGN_IN_LIMIT",
		})
	}
	if host != nil {
		for _, q := range host.RouteQuotas() {
			out = append(out, declaredQuota{
				name: q.Name, routes: q.Routes, limit: q.Limit, window: q.Window,
				by: "the " + q.PluginName() + " plugin",
			})
		}
	}

	names := make(map[string]bool, len(out))
	for _, q := range out {
		reason := quotaRefusal(q)
		if reason == "" && names[strings.ToLower(q.name)] {
			reason = "another quota already has that name"
		}
		if reason != "" {
			return nil, errors.Invalid(corehttp.CodeRouteQuotaInvalid,
				"the route quota %q declared by %s is refused: %s (ADR 0452)", q.name, q.by, reason)
		}
		names[strings.ToLower(q.name)] = true
	}

	return out, nil
}

// quotaRefusal says why a declared quota cannot be counted, or nothing.
func quotaRefusal(q declaredQuota) string {
	if err := corehttp.CheckRouteQuotaName(q.name); err != nil {
		return err.Error()
	}
	if q.limit <= 0 || q.window <= 0 {
		return fmt.Sprintf("its limit (%d) and its window (%s) have to be positive", q.limit, q.window)
	}
	if len(q.routes) == 0 {
		return "it names no route"
	}
	for _, route := range q.routes {
		if _, _, err := corehttp.ParseQuotaRoute(route); err != nil {
			return err.Error()
		}
	}

	return ""
}

// countRouteQuotas gives each declared quota a counter on the installation's
// guard backend: on Redis under its own prefix when GUARD_BACKEND=redis, so a
// quota holds across instances as the whole-API limit does, in memory
// otherwise. A shared backend without a client is [guardStack]'s refusal.
func countRouteQuotas(
	keyPrefix string, shared bool, rdb *redis.Client, declared []declaredQuota,
) ([]corehttp.RouteQuota, error) {
	out := make([]corehttp.RouteQuota, 0, len(declared))
	for _, q := range declared {
		var limiter corehttp.RateLimiter
		if shared {
			l, err := redisguard.NewLimiter(rdb, keyPrefix+quotaSection+q.name, q.limit, q.window)
			if err != nil {
				return nil, err
			}
			limiter = l
		} else {
			limiter = corehttp.NewMemoryLimiter(q.limit, q.window)
		}
		out = append(out, corehttp.RouteQuota{Name: q.name, Routes: slices.Clone(q.routes), Limiter: limiter})
	}

	return out, nil
}

// refuseUnboundRouteQuotas stops an installation in which a quota names a
// route the router does not bind (ADR 0452).
//
// It runs once everything is bound, beside the admin privilege gate. A quota
// on a path nothing serves counts nothing, and the route it was written for
// may have been renamed under it: the plugin keeps compiling and the limit is
// gone without a word. The route is compared as chi binds it, method and
// pattern, parameter names included.
func refuseUnboundRouteQuotas(r chi.Routes, declared []declaredQuota) error {
	if len(declared) == 0 {
		return nil
	}
	bound := map[string]bool{}
	err := chi.Walk(r, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		bound[method+" "+pattern] = true

		return nil
	})
	if err != nil {
		return errors.Wrap(err, errors.KindInternal, corehttp.CodeRouteQuotaInvalid,
			"the router could not be walked for the route quotas")
	}

	var unbound []string
	for _, q := range declared {
		for _, route := range q.routes {
			if !bound[route] {
				unbound = append(unbound, fmt.Sprintf("%q of quota %q (declared by %s)", route, q.name, q.by))
			}
		}
	}
	if len(unbound) == 0 {
		return nil
	}

	return errors.Invalid(corehttp.CodeRouteQuotaInvalid,
		"the installation does not start: %d route quota route(s) name no route this installation "+
			"binds, so they would count nothing: %s. Name each route as /openapi.json prints it, "+
			"\"METHOD /path/{param}\" (ADR 0452)", len(unbound), strings.Join(unbound, "; "))
}

// warnAboutSignInQuota reports a sign-in quota keyed on the connection in a
// shared environment (ADR 0452).
//
// Behind a reverse proxy with TRUSTED_PROXY_HOPS=0 every request comes from the
// proxy's address, so the quota is one bucket for the whole shop and anybody can
// keep every operator from signing in with ADMIN_SIGN_IN_LIMIT requests. It is
// the whole-API limit's silent state ([warnAboutRateLimit]) at a far smaller
// number, and it is reported on its own because that limit may be off. An
// installation facing the internet directly is right at zero and reads past it.
func warnAboutSignInQuota(cfg config.Config, log *slog.Logger) {
	if !cfg.IsShared() || cfg.AdminSignInLimit <= 0 || cfg.TrustedProxyHops > 0 {
		return
	}

	log.Warn("the admin sign-in quota falls on the CONNECTION, not the client",
		"admin_sign_in_limit", cfg.AdminSignInLimit,
		"admin_sign_in_window", cfg.AdminSignInWindow.String(),
		"warning", "behind a reverse proxy every sign-in comes from the proxy's address, so the quota is "+
			"ONE bucket for the whole shop and anybody can keep every operator from signing in",
		"remedy", "give the number of reverse proxies you trust with TRUSTED_PROXY_HOPS; for an "+
			"installation facing the internet directly 0 is CORRECT and this warning should be ignored")
}
