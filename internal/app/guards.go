// The HTTP guard stack: the two prefixes it is scoped to, the middlewares
// every API request passes through before a module's handler sees it, and the
// two things configuration feeds them — the guard backend and the signing
// secret. It is its own file because the ORDER and the SCOPE of these rings
// are a security decision that has to be read in one sitting, and the
// composition root's job of assembling modules says nothing about it.

package app

import (
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/bdrtr/gobit/core/audit"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/http/redisguard"
	"github.com/bdrtr/gobit/internal/adminui"
	"github.com/bdrtr/gobit/internal/core/config"
	authapi "github.com/bdrtr/gobit/internal/modules/auth/api"
	cartapi "github.com/bdrtr/gobit/internal/modules/cart/api"
	filelocal "github.com/bdrtr/gobit/internal/modules/file/local"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
)

// The path prefixes of the API surfaces. The guards, the rate limit and
// idempotency are scoped by these two prefixes; /health and /ready are
// deliberately left outside.
const (
	adminPrefix = "/admin/v1"
	storePrefix = "/store/v1"
)

// codeGuardBackendMissing reports that a shared backend was requested while no
// Redis client is present.
const codeGuardBackendMissing = "guard_backend_unavailable"

// temporarySecretBytes is the byte length of the random secret generated for
// development; it matches the output length of HS256.
const temporarySecretBytes = 32

// memoryIdempotencyStore builds the in-memory idempotency store from the
// configuration.
//
// It takes the whole configuration and no separate numbers, which is the entire
// point: the store's byte budget bounds a correctness guarantee — when it fills,
// a retry is processed a second time — and the startup line next to it reports
// cfg.IdempotencyMaxMemoryBytes whether or not that number ever reached the
// store. Measured by mutation: a call site handing the constructor 0 runs the
// 64 MiB default while the log still prints the configured value, and every
// test in this repository stayed green. With the construction behind a
// cfg-only function there is no argument left for a call site to get wrong, and
// the function itself is asserted in setup_test.go.
func memoryIdempotencyStore(cfg config.Config) *corehttp.MemoryIdempotencyStore {
	return corehttp.NewMemoryIdempotencyStore(cfg.IdempotencyTTL, cfg.IdempotencyMaxMemoryBytes)
}

// guardStack builds the application's guard middlewares from the
// configuration.
//
// The decision about order and scope lives in the core, in [corehttp.APIGuards];
// only the parts that come from configuration (the rate limiter, the
// idempotency store, the exempt paths) are chosen here. The reason for the
// split is that the end-to-end tests must be able to build the SAME stack: had
// the order been written here, the test would keep its own copy and the two
// copies would silently diverge.
//
// # Choosing the backend
//
// GUARD_BACKEND=memory (the default) is for a single-process installation. If
// more than one instance runs, "redis" is required; the rationale and the
// difference between the two implementations are in the [redisguard] package's
// godoc.
//
// If Redis is selected the client is MANDATORY and startup stops without it: "I
// asked for a shared store but silently ran on the in-memory one" is exactly
// the case where the guard is believed to work while it does not.
//
// The in-memory store is bounded by a byte BUDGET
// (IDEMPOTENCY_MAX_MEMORY_BYTES) and drops its oldest record when the budget
// fills; the trade and the measurements are on
// [corehttp.MemoryIdempotencyStore]. The number is logged here on every start
// because it bounds a guarantee, not a cost.
//
// The key namespace prefix (REDIS_KEY_PREFIX) also passes through here and is
// the only thing separating two installations that share the SAME Redis; the
// rationale is on [config.Config]'s RedisKeyPrefix field. The prefix is logged:
// two installations falling into the same namespace is a failure that can only
// be seen with two startup logs side by side, and the consequence of getting it
// wrong (one installation's answer going to the other) is silent.
//
// The two silent states of the rate limit are reported here too; see
// [warnAboutRateLimit].
func guardStack(
	cfg config.Config,
	authn corehttp.Authenticator,
	panel *adminui.Ring,
	rdb *redis.Client,
	pool *db.Pool,
	log *slog.Logger,
) ([]func(http.Handler) http.Handler, *corehttp.CallbackRegistry, error) {
	warnAboutRateLimit(cfg, log)

	// The audit log is unconditional in a real server: an installation whose
	// admin writes are not recorded looks exactly like one where nobody wrote
	// anything. The nil check is for the guard-stack tests, which build the
	// stack without a database — and those are also the only callers that can
	// pass nil, because serve always has a pool by this point.
	var auditor corehttp.AuditWriter
	if pool != nil {
		auditor = audit.NewStore(pool.Pool())
	}

	opts := corehttp.GuardOptions{
		Audit:         auditor,
		Authenticator: authn,
		AdminPrefix:   adminPrefix,
		StorePrefix:   storePrefix,
		// CORS is applied to the STORE surface only, and only when an
		// installation configured origins; the reasoning is on
		// [corehttp.GuardOptions.CORSOrigins].
		CORSOrigins: cfg.CORSAllowedOrigins,
		AuditID:     newAuditID,
		AuditLogger: log,
		// The audit log's OWN listing is the one read this framework records.
		// The rule that excludes reads exists because "somebody listed the
		// orders" answers no question; who read the record of who did what is
		// the question an incident starts with (ADR 0037).
		AuditedReads: []string{auditLogPath},
		// The login endpoint is EXEMPT from the guard: the request whose
		// identity is to be checked is the one about to establish it. The path
		// is not spelled out here, it is read from the auth module's constant.
		AdminExempt: []string{authapi.LoginPath},
		// Uploaded files are served WITHOUT identity (an <img> in a storefront
		// cannot send a header) but NOT without a quota: every request performs
		// a database read and a disk access. The prefix is not spelled out
		// here, it is read from the provider's constant.
		//
		// /openapi.json is in the same class and is here for the same reason:
		// the client is a code generator or an IDE and sends no header — but
		// the endpoint is not free. Even with the document cached, every
		// request walks the route tree to confirm the cache is still valid, and
		// when the tree changes every module's DTOs are translated again
		// through reflection. Identity and quota are SEPARATE decisions; the
		// decision for this endpoint is "no identity, but a quota".
		// The admin panel is NOT in the same class and enters this list not for
		// identity but for the QUOTA: its own identity ring is attached just
		// below, at the end of the stack. Were the prefix missing from this
		// list the panel would face no rate limit at all — guard scope matches
		// on a segment boundary and /admin/ui is NOT under /admin/v1.
		OpenPrefixes: []string{filelocal.DefaultURLPrefix, openAPIPath, adminui.URLPrefix},
		// The GraphQL storefront endpoint is a POST but it is a READ; there is
		// no side effect for an idempotency record to protect, and because the
		// GraphQL contract returns 200 even on an internal error, a record
		// would replay a transient failure for the whole TTL. The full
		// rationale is on the [corehttp.GuardOptions.IdempotencyExempt] field;
		// the path is not spelled out here, it is read from the module's
		// constant.
		//
		// Cart CREATION is exempt for a different reason, and it is a leak
		// rather than a waste. The idempotency namespace is the caller's
		// Principal, and on the storefront the Principal is the PUBLISHABLE
		// KEY — the store's identity, identical for every shopper and visible
		// in every browser. So all shoppers share one namespace, and the key
		// that selects a record inside it is a header the CLIENT chooses.
		//
		// Every other storefront POST survives that, because the fingerprint
		// includes the PATH and those paths carry the cart id: a second shopper
		// reusing the key on their own cart gets 409 idempotency_key_reuse, not
		// somebody else's data. Cart creation is the one endpoint whose path
		// carries no capability and whose response CREATES one — so a second
		// shopper sending the same key and the same body was handed the first
		// shopper's cart id, which is a capability URL (there is no ownership
		// check on a cart; see README's known limits). Measured, not deduced:
		// two independent callers, `Idempotency-Key: cart-9`, identical bodies,
		// identical cart id in both responses and `Idempotency-Replayed: true`
		// on the second.
		//
		// Exempting it costs a duplicate cart when a client retries a timed-out
		// creation. That is an abandoned row. The alternative was handing a
		// stranger someone's cart.
		IdempotencyExempt: []string{graph.Path, cartapi.StoreCartsPath},
	}

	if cfg.GuardBackend == config.BackendRedis {
		if rdb == nil {
			return nil, nil, errors.Invalid(codeGuardBackendMissing,
				"GUARD_BACKEND=%s was selected but there is no Redis client", config.BackendRedis)
		}

		store, err := redisguard.NewIdempotencyStore(rdb, cfg.RedisKeyPrefix, cfg.IdempotencyTTL)
		if err != nil {
			return nil, nil, err
		}
		opts.IdempotencyStore = store

		// When the limit is off (limit <= 0) the limiter is not built at all;
		// building it would behave like "0 requests" and cut all traffic.
		if cfg.RateLimitPerMinute > 0 {
			limiter, limitErr := redisguard.NewLimiter(rdb, cfg.RedisKeyPrefix, cfg.RateLimitPerMinute, time.Minute)
			if limitErr != nil {
				return nil, nil, limitErr
			}

			opts.Limiter = limiter
			opts.LimitKey = corehttp.TrustedProxyIPKey(cfg.TrustedProxyHops)
		}

		log.Info("guard backend: redis (shared)",
			"key_prefix", cfg.RedisKeyPrefix)

		callbacks := newCallbackRegistry(opts, log)

		return withPanelRing(opts, panel, callbacks), callbacks, nil
	}

	opts.IdempotencyStore = memoryIdempotencyStore(cfg)

	// The memory budget is logged on EVERY start, not only in a shared
	// environment. It bounds a correctness guarantee rather than a cost: when
	// the budget fills, the oldest record is dropped and a retry carrying that
	// key is processed a second time. An operator who never saw the number has
	// no way to tell that outcome apart from a bug, and no way to know which
	// knob moves it.
	log.Info("idempotency store: in-memory",
		"budget_bytes", cfg.IdempotencyMaxMemoryBytes,
		"ttl", cfg.IdempotencyTTL,
		"when_full", "the oldest record is dropped and a retry with that key is processed AGAIN",
		"remedy", "GUARD_BACKEND=redis or a larger IDEMPOTENCY_MAX_MEMORY_BYTES")

	// NewMemoryLimiter returns nil when the limit is not positive (the rate
	// limit is off). Assigning that straight to the interface field would turn
	// a nil *MemoryLimiter into a non-nil interface and attach the limiter
	// anyway; hence the check first.
	if limiter := corehttp.NewMemoryLimiter(cfg.RateLimitPerMinute, time.Minute); limiter != nil {
		opts.Limiter = limiter
		opts.LimitKey = corehttp.TrustedProxyIPKey(cfg.TrustedProxyHops)
	}

	// The in-memory setup is BROKEN in a multi-instance deployment and it
	// breaks silently; the warning is the only chance of noticing.
	if cfg.IsShared() {
		log.Warn("guard backend: in-memory",
			"warning", "if more than one instance is running, idempotency protection does NOT work across instances",
			"remedy", "GUARD_BACKEND=redis")
	}

	callbacks := newCallbackRegistry(opts, log)

	return withPanelRing(opts, panel, callbacks), callbacks, nil
}

// newCallbackRegistry gives the inbound-callback ring the same guard services
// the API surfaces got.
//
// It takes them from the FILLED GuardOptions rather than from the config a
// second time: the store and the limiter are chosen by a branch above (Redis or
// memory, and no limiter at all when the limit is off), and reading the config
// again would be a second place that has to reach the same conclusion. A
// callback guarded by a different store than the API is guarded by is a
// difference nobody would look for.
func newCallbackRegistry(opts corehttp.GuardOptions, log *slog.Logger) *corehttp.CallbackRegistry {
	return corehttp.NewCallbackRegistry(corehttp.CallbackOptions{
		Limiter:  opts.Limiter,
		LimitKey: opts.LimitKey,
		Store:    opts.IdempotencyStore,
		Logger:   log,
	})
}

// withPanelRing adds the panel's own ring ON TOP of the API guards.
//
// # Why not inside the core's stack
//
// The panel is not UNDER /admin/v1 and that is deliberate (ADR 0011): had it
// been placed there, every page typed into the address bar would get a 401,
// HTML endpoints would leak into the OpenAPI document, and the authorization
// test that walks the route tree would expect a 403 from every page. The cost
// is that the panel tree does not enter the core's identity ring BY ITSELF —
// scope matches on a segment boundary.
//
// The cost is paid here. Without the ring the panel opens without identity, and
// NO test could see it; the failure would only show on the first unauthorized
// access.
//
// # Order
//
// The origin check comes BEFORE identity and has NO exemption: submitting the
// login form does not require identity, but it is a state-changing request —
// which makes it precisely the one open to being triggered cross-site. The
// identity ring, by contrast, exempts the login page: the request whose
// identity is to be checked is the one about to establish it.
func withPanelRing(
	opts corehttp.GuardOptions,
	panel *adminui.Ring,
	callbacks *corehttp.CallbackRegistry,
) []func(http.Handler) http.Handler {
	// The API session ring comes FIRST, before every guard in the API stack,
	// and the order is the whole of its correctness: it turns the panel's
	// session cookie into the Authorization header that corehttp.RequireAdmin
	// reads, and RequireAdmin is inside APIGuards. Appended after, as the two
	// panel rings below are, it would run once the request had already been
	// refused.
	//
	// It is scoped to the ADMIN API prefix and not to the panel's, because the
	// requests it serves are the ones the panel's script sends to /admin/v1.
	return append([]func(http.Handler) http.Handler{
		corehttp.Scoped(adminPrefixOf(opts), nil, panel.APISession),
	},
		append(corehttp.APIGuards(opts),
			corehttp.Scoped(adminui.URLPrefix, nil, panel.CheckOrigin),
			corehttp.Scoped(adminui.URLPrefix, adminui.ExemptPaths(), panel.Protect),
			// The callback ring carries no prefix of its own: it acts on the
			// paths it was given and passes everything else through. A
			// reserved prefix would force every provider's configured URL to
			// move, and that URL lives on the PROVIDER's side, where changing
			// it is an operational break rather than a deploy.
			callbacks.Middleware(),
		)...)
}

// adminPrefixOf answers what prefix the admin surface is mounted under.
//
// The empty option means the core's default, and the fallback is written here
// rather than assumed: a middleware scoped to "" would match every path in the
// tree, so the panel's session cookie would be promoted on the storefront too.
func adminPrefixOf(opts corehttp.GuardOptions) string {
	if opts.AdminPrefix == "" {
		return corehttp.DefaultAdminPrefix
	}

	return opts.AdminPrefix
}

// warnAboutRateLimit reports the two silent states of the rate limit.
//
// Both are born of configuration, both left NOT ONE LINE of trace until now,
// and both would only be seen under load — that is, at the most expensive
// moment:
//
//  1. With RATE_LIMIT_PER_MINUTE <= 0 the limiter is NOT built at all (in ADR
//     0007 zero means "off", not "0 requests"). It is a legitimate choice, but
//     in a shared environment it also leaves the login endpoint unprotected:
//     an attacker trying passwords works without a quota. An "off" nobody knows
//     about is indistinguishable from a zero typed by accident; the log makes
//     both visible.
//  2. The state where the limit is ON but the quota does not fall PER CLIENT.
//     The rationale, and why the default has not changed, are in the
//     [config.Config.RateLimitKeyIsPerClient] godoc.
//
// In local development both are silent or INFO: there a single instance runs,
// there is no reverse proxy, and printing a warning on every startup would
// drown a real warning in noise. The same door is open in [warnAboutFileRoot]
// and in the in-memory guard warning.
func warnAboutRateLimit(cfg config.Config, log *slog.Logger) {
	if cfg.RateLimitPerMinute <= 0 {
		if !cfg.IsShared() {
			log.Info("the rate limiter was not attached",
				"reason", "RATE_LIMIT_PER_MINUTE <= 0")

			return
		}

		log.Warn("the rate limiter was NOT ATTACHED",
			"rate_limit_per_minute", cfg.RateLimitPerMinute,
			"warning", "no endpoint gets a quota; the login endpoint (POST /admin/v1/auth/login) "+
				"is open to unlimited attempts too",
			"remedy", "unless turning it off was deliberate, give RATE_LIMIT_PER_MINUTE a positive value")

		return
	}

	if !cfg.IsShared() || cfg.RateLimitKeyIsPerClient() {
		return
	}

	log.Warn("the rate limit key falls on the CONNECTION, not the client",
		"trusted_proxy_hops", cfg.TrustedProxyHops,
		"rate_limit_per_minute", cfg.RateLimitPerMinute,
		"warning", "X-Forwarded-For is never read; behind a reverse proxy, an ingress or a CDN the "+
			"source of every request is the proxy's IP, so the quota is not per customer but a "+
			"SINGLE bucket for the WHOLE STORE and one customer can lock the storefront",
		"remedy", "give the number of reverse proxies you trust with TRUSTED_PROXY_HOPS; for an "+
			"installation facing the internet directly 0 is CORRECT and this warning should be ignored")
}

// jwtSecret returns the signing secret; in development it generates one for
// this startup if none was given.
//
// In SHARED environments (production, staging) an empty or short secret is
// already REJECTED by config.Validate, so only local development and test fall
// through to here. Putting a fixed default there was the worst option: a token
// signed with a secret everybody knows is a way to mint a fully privileged
// admin token in a configuration that gets carried into production by accident.
//
// A random secret makes tokens valid only UNTIL A RESTART. The cost is a
// developer signing in again; in exchange no environment ever holds a
// predictable signing secret.
func jwtSecret(cfg config.Config, log *slog.Logger) string {
	if cfg.JWTSecret != "" {
		return cfg.JWTSecret
	}

	secret := make([]byte, temporarySecretBytes)
	if _, err := rand.Read(secret); err != nil {
		// If crypto/rand cannot be read there is a larger problem on the
		// system and falling back to something weak would be wrong; an empty
		// secret stops the auth module at startup.
		log.Error("a random JWT secret could not be generated", "error", err)

		return ""
	}

	log.Warn("JWT_SECRET was not provided; a random secret was generated for this startup",
		"warning", "every admin session drops on restart")

	return base64.RawURLEncoding.EncodeToString(secret)
}
