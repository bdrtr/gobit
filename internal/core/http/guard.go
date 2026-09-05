package http

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
)

// Scoped runs a middleware stack ONLY under a given path prefix.
//
// # Why it is needed
//
// Modules register their routes with their FULL PATH and on a SINGLE router
// (see any module's api.Handler.Routes): opening a sub-router for "/admin/v1"
// would make chi panic on the second module mounting the same pattern. The price
// is that chi's natural scoping tool (Route/Group) is lost: middleware added
// with r.Use applies to ALL requests, /health and /ready included.
//
// Scoped pays that price: the scope is established inside the middleware itself,
// not in the router tree.
//
//	router.Use(corehttp.Scoped("/admin/v1", []string{authapi.LoginPath},
//	    corehttp.RequireAdmin(auth)))
//
// # The matching rule
//
// The prefix matches at a SEGMENT boundary: "/admin/v1" catches only "/admin/v1"
// and "/admin/v1/..." , never "/admin/v1x". Otherwise a new "/admin/v1x" prefix
// would silently come under the guard and every endpoint defined there would
// pass through middleware it was not designed for.
//
// # Why r.URL.Path
//
// chi routes over RawPath when it is filled; we look at Path (the decoded form).
// The difference only shows up on an encoded request (say "/admin%2Fv1/users"):
// Path TURNS the guard ON, while chi cannot find the route and returns a 404. So
// the divergence is always IN THE GUARD'S FAVOR.
//
// # exempt
//
// exempt are the full paths that are EXEMPT from the middleware even though they
// sit under the prefix (the login endpoint, say: the request whose identity is to
// be verified is the very one that establishes it). The match is on the full
// path; it does not look at the method — an undefined method on an exempt path is
// rejected by the router anyway.
func Scoped(prefix string, exempt []string, mws ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	exemptSet := make(map[string]struct{}, len(exempt))
	for _, path := range exempt {
		exemptSet[path] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		// The chain is built once; wrapping it again on every request would repeat
		// the same work per request.
		guarded := next
		for i := len(mws) - 1; i >= 0; i-- {
			if mws[i] == nil {
				continue
			}
			guarded = mws[i](guarded)
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !inScope(r.URL.Path, prefix) {
				next.ServeHTTP(w, r)
				return
			}
			if _, ok := exemptSet[r.URL.Path]; ok {
				next.ServeHTTP(w, r)
				return
			}

			guarded.ServeHTTP(w, r)
		})
	}
}

// inScope reports whether the path carries the prefix at a segment boundary.
func inScope(path, prefix string) bool {
	if prefix == "" || prefix == "/" {
		return true
	}

	prefix = strings.TrimSuffix(prefix, "/")

	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// GuardOptions are the inputs of the [APIGuards] stack.
type GuardOptions struct {
	// Authenticator resolves identities. With nil the guard is still installed and
	// rejects ALL requests (ADR 0007): an unguarded admin surface should stay
	// noisily closed rather than quietly open.
	Authenticator Authenticator
	// AdminPrefix is the path prefix of the admin surface; empty means [DefaultAdminPrefix].
	AdminPrefix string
	// StorePrefix is the path prefix of the store surface; empty means [DefaultStorePrefix].
	StorePrefix string
	// CORSOrigins are the sites allowed to call the STORE surface from a
	// browser; empty means no CORS at all.
	//
	// It covers the store surface only. The admin surface authenticates with a
	// bearer token, and ADR 0011 refused CORS there precisely because the token
	// would then have to live in a browser.
	CORSOrigins []string
	// AdminExempt are the full paths on the admin surface that are EXEMPT from
	// authentication — in practice only the login endpoint.
	//
	// The core CANNOT KNOW this path itself: auth is a module and the core cannot
	// import modules (Principle 2.4). The path arrives as a parameter from the side
	// building the application.
	AdminExempt []string
	// Limiter is the rate limiter; with nil no rate limit is installed.
	Limiter RateLimiter
	// LimitKey produces the key identifying the client; nil means [ClientIPKey].
	//
	// A function looking at the caller's IDENTITY does not fit here: this ring runs
	// before identity (see the ordering below) and at that point there is no
	// [Principal] in the context. The full reasoning is in [KeyFunc]'s godoc.
	LimitKey KeyFunc
	// IdempotencyStore is the store of the idempotency records; with nil no
	// idempotency is installed.
	IdempotencyStore IdempotencyStore
	// IdempotencyExempt are the full paths EXEMPT from the idempotency ring.
	//
	// [Idempotency]'s "turning a transient fault into a permanent one" guard looks
	// only at the HTTP STATUS: a 5xx is not recorded, everything else is. On a
	// surface that answers with a 200 even in the error case that guard NEVER
	// engages, and a transient internal error is frozen for the whole TTL (24 hours
	// by default) — the client keeps getting the same error body even after the
	// fault is fixed. This field is the way to keep such an endpoint out of the
	// stack entirely.
	//
	// The exemption fits READ surfaces best: the job of an idempotency record is not
	// to produce the side effect (a charge, an order) a second time, and a read
	// endpoint has no side effect to produce — the record only holds stale data. If
	// a read endpoint ends up in the stack because it is a POST (GraphQL carrying
	// its query in the body, say), the exemption is what belongs there.
	//
	// The core CANNOT KNOW this path itself: the endpoint lives in a module and the
	// core cannot import modules (Principle 2.4). The path arrives as a parameter
	// from the side building the application, just like [GuardOptions.AdminExempt].
	//
	// The exemption is ONLY from the idempotency ring: the path keeps going through
	// the rate limit and through identity.
	IdempotencyExempt []string
	// PublishableKeyHeader is the header the publishable key is read from; empty
	// means [PublishableKeyHeader].
	PublishableKeyHeader string
	// OpenPrefixes are the path prefixes that do NOT ASK for identity but still have
	// to be subject to the rate limit (the "/files" prefix serving uploaded files,
	// say).
	//
	// Identity and quota are SEPARATE decisions. An endpoint may be identity-free
	// because its client cannot send a header (an <img> in the storefront), but that
	// does not mean it is free of charge: if every request does a database read or a
	// disk access, having no quota means a load that can be thrown at us without
	// even paying the authentication cost.
	//
	// Health endpoints do NOT GO HERE: having the path the orchestrator watches hit
	// the quota would pull a healthy instance out of traffic.
	OpenPrefixes []string
}

// The default API prefixes.
const (
	// DefaultAdminPrefix is the path prefix of the admin surface.
	DefaultAdminPrefix = "/admin/v1"
	// DefaultStorePrefix is the path prefix of the store surface.
	DefaultStorePrefix = "/store/v1"
)

// APIGuards produces, in order, the middleware stack guarding the two API surfaces.
//
// The stack is handed to the [RouterOptions.Middlewares] field. The health
// endpoints (/health, /ready) match no prefix, so the stack does not touch them.
//
// # The order
//
//  1. RATE LIMIT — BEFORE authentication. Otherwise an attacker trying passwords
//     would make us pay the authentication cost (bcrypt + a database lookup) on
//     every attempt, and only then have their quota drop. With the limit running
//     first a rejected request is almost free.
//  2. IDENTITY — the whole admin surface except the login endpoint, and the whole
//     store surface without a publishable key, is rejected.
//  3. IDEMPOTENCY — AFTER identity. The record key is held together with the
//     caller's identity (see [Idempotency]); had it run while identity was still
//     unresolved, the same key from two different callers would collide.
//     [GuardOptions.IdempotencyExempt] paths skip this ring — and ONLY this ring.
//
// The reason this function stands in the core is that the order is written in a
// SINGLE place: the application and the end-to-end tests build the same stack,
// that is, the guard we test is the very one in production.
func APIGuards(opts GuardOptions) []func(http.Handler) http.Handler {
	admin := opts.AdminPrefix
	if admin == "" {
		admin = DefaultAdminPrefix
	}

	store := opts.StorePrefix
	if store == "" {
		store = DefaultStorePrefix
	}

	stack := make([]func(http.Handler) http.Handler, 0, 7)

	// CORS goes FIRST, and the order is load-bearing: a preflight carries no
	// credentials and no idempotency key, so a browser asking permission would
	// be turned away by the identity guard before it ever learned whether the
	// call was allowed. Answering the preflight before the guards is what makes
	// the answer about the POLICY rather than about the missing key.
	if len(opts.CORSOrigins) > 0 {
		stack = append(stack, Scoped(store, nil, CORS(opts.CORSOrigins)))
	}

	if opts.Limiter != nil {
		limitKey := opts.LimitKey
		if limitKey == nil {
			limitKey = ClientIPKey
		}

		limit := RateLimit(opts.Limiter, limitKey)
		stack = append(stack, Scoped(admin, nil, limit), Scoped(store, nil, limit))

		// IDENTITY-FREE prefixes are limited too.
		//
		// Being identity-free does NOT mean "unguarded": an endpoint that asks for no
		// identity is, for that very reason, not one that should have no quota. File
		// serving is the example — the <img> tag in the storefront cannot send a
		// header, so the endpoint is identity-free; but every request does a database
		// read, and having no limit means a load that can be thrown at us without
		// paying the authentication cost.
		for _, onek := range opts.OpenPrefixes {
			stack = append(stack, Scoped(onek, nil, limit))
		}
	}

	stack = append(stack,
		Scoped(admin, opts.AdminExempt, RequireAdmin(opts.Authenticator)),
		Scoped(store, nil, RequireStore(opts.Authenticator, opts.PublishableKeyHeader)),
	)

	if opts.IdempotencyStore != nil {
		idem := Idempotency(opts.IdempotencyStore)
		// The exempt list is the SAME for both prefixes: a path sits under only one of
		// them anyway, and splitting the list in two would make the caller answer a
		// meaningless question.
		stack = append(stack,
			Scoped(admin, opts.IdempotencyExempt, idem),
			Scoped(store, opts.IdempotencyExempt, idem))
	}

	return stack
}

// codeAuthNotBound reports that the authenticator has not been bound yet.
const codeAuthNotBound = "auth_not_bound"

// DeferredAuthenticator makes the authenticator bindable LATER.
//
// # Why it is needed
//
// The guard middleware has to be installed while the router is being built — chi
// rejects an r.Use called after a route is registered, with a panic. The
// authenticator, on the other hand, is born when the auth module Registers, that
// is DURING module bootstrap. The two moments are not the same, and the router
// has to exist before bootstrap in order to receive the modules' routes.
//
// This type closes the gap in between: it is installed while the router is being
// built and filled in with [DeferredAuthenticator.Bind] once the authenticator is
// ready.
//
// # A request arriving before the binding
//
// It is REJECTED (ADR 0007). An unguarded admin surface should stay noisily
// closed rather than quietly open. The side building the application is expected
// to bind right after bootstrap and to stop startup if it cannot; this 401 is the
// last line of defense for the case where that contract is forgotten.
//
// It is safe for concurrent use: the binding happens once, the read on every request.
type DeferredAuthenticator struct {
	// value always holds an authnHolder; atomic.Value wants a single concrete type
	// and storing the interface value directly would panic when the dynamic type
	// changed.
	value atomic.Value
}

// authnHolder wraps the interface value in a single concrete type for atomic.Value.
type authnHolder struct {
	inner Authenticator
}

var _ Authenticator = (*DeferredAuthenticator)(nil)

// Bind puts the real authenticator in place.
func (d *DeferredAuthenticator) Bind(a Authenticator) {
	d.value.Store(authnHolder{inner: a})
}

// resolve returns the bound authenticator; an error if it is not bound.
func (d *DeferredAuthenticator) resolve() (Authenticator, error) {
	h, ok := d.value.Load().(authnHolder)
	if !ok || h.inner == nil {
		return nil, coreerrors.Unauthorized(codeAuthNotBound,
			"the authenticator has not been bound yet")
	}

	return h.inner, nil
}

// AuthenticateAdmin forwards the call to the bound authenticator.
func (d *DeferredAuthenticator) AuthenticateAdmin(
	ctx context.Context, scheme, credential string,
) (Principal, error) {
	a, err := d.resolve()
	if err != nil {
		return Principal{}, err
	}

	return a.AuthenticateAdmin(ctx, scheme, credential)
}

// AuthenticateStore forwards the call to the bound authenticator.
func (d *DeferredAuthenticator) AuthenticateStore(ctx context.Context, key string) (Principal, error) {
	a, err := d.resolve()
	if err != nil {
		return Principal{}, err
	}

	return a.AuthenticateStore(ctx, key)
}
