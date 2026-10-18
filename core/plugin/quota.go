package plugin

import (
	"slices"
	"time"
)

// RouteQuota is a limit on how often one client may call a set of routes,
// counted together (ADR 0452): a plugin's own routes, the core's, or a
// contrib module's, as the installation binds them.
//
// The composition root counts it on the installation's guard backend, in
// memory or on Redis as GUARD_BACKEND says, keyed by the client key the
// whole-API limit uses (TRUSTED_PROXY_HOPS), and answers a spent quota 429
// rate_limited with Retry-After before identity is asked.
type RouteQuota struct {
	// Name keeps the quota's counts apart from every other's, the core's
	// included; prefix it with the plugin's own name. ASCII letters, digits,
	// '-', '_' and '.'.
	Name string
	// Routes are the routes the quota counts, each "METHOD /path" as
	// /openapi.json prints it, a parameter in braces:
	// "POST /store/v1/orders/{id}/returns". Every route must be bound when
	// the installation is assembled, or it does not start: a quota on a path
	// nothing serves guards nothing, and a renamed route would leave it so
	// without a word.
	Routes []string
	// Limit is how many requests a client may make to the routes, together,
	// per Window. It must be positive.
	Limit int
	// Window is the period the limit renews over. It must be positive.
	Window time.Duration

	// plugin is the name of the plugin that declared the quota, captured by
	// [Host.RegisterRouteQuota], for [Job.PluginName]'s reason.
	plugin string
}

// PluginName reports the plugin that declared the quota.
func (q RouteQuota) PluginName() string { return q.plugin }

// RegisterRouteQuota declares a limit on routes, a plugin's own or anybody
// else's (ADR 0452).
//
// It is how a plugin puts a quota on a route it does not bind, the core's
// sign-in or a storefront's return request, where wrapping its own routes
// cannot reach. It follows [Host.RegisterStoreProofHeader]'s shape: collected
// during Setup and validated by the composition root, which refuses a name, a
// route, a limit or a window it cannot hold, and once everything is bound, a
// route nothing serves.
func (h *Host) RegisterRouteQuota(q RouteQuota) {
	q.Routes = slices.Clone(q.Routes)
	q.plugin = h.active
	h.quotas = append(h.quotas, q)
}

// RouteQuotas returns the quotas the plugins declared, in registration order.
// It is the composition root's read; the slice is a copy, for [Host.Jobs]'s
// reason.
func (h *Host) RouteQuotas() []RouteQuota {
	out := slices.Clone(h.quotas)
	for i := range out {
		out[i].Routes = slices.Clone(out[i].Routes)
	}

	return out
}
