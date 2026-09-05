package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"

	"github.com/bdrtr/gobit/core/audit"
	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errorreport"
	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/http/redisguard"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/core/module"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/adminui"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/core/workflow"
	"github.com/bdrtr/gobit/internal/core/workflow/pgstore"
	authapi "github.com/bdrtr/gobit/internal/modules/auth/api"
	authmodels "github.com/bdrtr/gobit/internal/modules/auth/models"
	authservice "github.com/bdrtr/gobit/internal/modules/auth/service"
	cartapi "github.com/bdrtr/gobit/internal/modules/cart/api"
	"github.com/bdrtr/gobit/internal/modules/file"
	filelocal "github.com/bdrtr/gobit/internal/modules/file/local"
	fileservice "github.com/bdrtr/gobit/internal/modules/file/service"
	"github.com/bdrtr/gobit/internal/modules/notification"
	notificationservice "github.com/bdrtr/gobit/internal/modules/notification/service"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
	invoicingwf "github.com/bdrtr/gobit/internal/workflows/invoicing"
	returnswf "github.com/bdrtr/gobit/internal/workflows/returns"
	"github.com/bdrtr/gobit/plugins/errorotlp"
	"github.com/bdrtr/gobit/plugins/errorsentry"
	"github.com/bdrtr/gobit/plugins/files3"
	"github.com/bdrtr/gobit/plugins/notificationsmtp"
	"github.com/bdrtr/gobit/plugins/paymentpaytr"
	"github.com/bdrtr/gobit/plugins/paymentstripe"
	"github.com/bdrtr/gobit/plugins/searchpg"
	"github.com/bdrtr/gobit/plugins/webpush"
)

// The path prefixes of the API surfaces. The guards, the rate limit and
// idempotency are scoped by these two prefixes; /health and /ready are
// deliberately left outside.
const (
	adminPrefix = "/admin/v1"
	storePrefix = "/store/v1"
)

// codeFlowSetupFailed reports that the cross-module workflows could not be set
// up.
const codeFlowSetupFailed = "workflow_setup_failed"

// codeUnknownPlugin reports an unrecognized name in the PLUGINS list.
const codeUnknownPlugin = "plugin_unknown"

// codeBootstrapFailed reports that seeding the first administrator failed.
const codeBootstrapFailed = "admin_bootstrap_failed"

// codeAdminBootstrapRequired reports that a fresh installation is not
// manageable: there are no users and no seed has been configured.
const codeAdminBootstrapRequired = "admin_bootstrap_required"

// codeUnknownNotificationProvider reports that NOTIFICATION_PROVIDER does not
// name a registered provider.
const codeUnknownNotificationProvider = "notification_provider_unknown"

// codeUnknownFileProvider reports that FILE_PROVIDER does not name a
// registered provider.
const codeUnknownFileProvider = "file_provider_unknown"

// openAPIPath is the path the generated API schema is served from.
const openAPIPath = "/openapi.json"

// codeGuardBackendMissing reports that a shared backend was requested while no
// Redis client is present.
const codeGuardBackendMissing = "guard_backend_unavailable"

// temporarySecretBytes is the byte length of the random secret generated for
// development; it matches the output length of HS256.
const temporarySecretBytes = 32

// pluginCatalog holds the plugins COMPILED into this binary.
//
// The catalog lives here, at the composition root: adding a plugin changes
// neither the core nor any module, it only adds a line to this map (plan Phase
// 9 DoD). Which ones are installed is chosen by the PLUGINS environment
// variable.
//
// The catalog shows three different ways of extending. paymentstripe,
// notificationsmtp and files3 register a PROVIDER into a module's registry (the
// payment, notification and file modules' extension points); searchpg and
// webpush bring THEIR OWN MODULE —
// with its own table, its own migration and its own routes — and opens a new
// endpoint (GET /store/v1/search) without being named anywhere except the line
// below; errorsentry and errorotlp fill a slot the CORE owns, so they need no
// module to exist at all.
//
// webpush and paymentpaytr are the second and third of that middle kind, and
// both are there for the same reason: each looked like a plain provider and
// turned out to need durable state. A push destination is a set of devices the
// framework has to have STORED, and PayTR reports the outcome of a payment by
// posting BACK rather than answering when asked — so in both cases the provider
// slot alone cannot express the party (ADR 0018).
//
// The two provider plugins are not the same kind of thing, and the difference
// is worth naming: paymentstripe is a SKELETON that returns an error from every
// money-moving method, while notificationsmtp actually delivers. A provider
// slot with no working implementation is a promise the framework has not kept,
// and the notification slot was the one where that showed most — the only
// provider in the box writes a log line and sends nothing.
//
// The two reporters are the SAME slot filled twice, and that is deliberate:
// ADR 0014 said its shape could only be tested by a second implementation with
// a different model. Installing both is not supported — the core holds one
// reporter — and choosing between them is what the PLUGINS variable is for.
var pluginCatalog = map[string]func() coreplugin.Plugin{
	errorotlp.Name:        func() coreplugin.Plugin { return errorotlp.New() },
	errorsentry.Name:      func() coreplugin.Plugin { return errorsentry.New() },
	files3.Name:           func() coreplugin.Plugin { return files3.New() },
	notificationsmtp.Name: func() coreplugin.Plugin { return notificationsmtp.New() },
	paymentstripe.Name:    func() coreplugin.Plugin { return paymentstripe.New() },
	paymentpaytr.Name:     func() coreplugin.Plugin { return paymentpaytr.New() },
	searchpg.Name:         func() coreplugin.Plugin { return searchpg.New() },
	webpush.Name:          func() coreplugin.Plugin { return webpush.New() },
}

// describeAPI builds the OpenAPI document and runs it through the modules that
// can describe themselves.
//
// [openapi.Describer] is an OPTIONAL interface and the type assertion is made
// HERE. Adding a method to the contract ([module.Module]) would have been a
// change that broke every module at once; besides, an undescribed module is a
// VALID model — it appears in the document with its path, method and security,
// only without a body.
//
// The call has to sit at the composition root: the core does not know the
// modules (Principle 2.4) and this is the only place that sees the module list.
func describeAPI(title, apiVersion string, modules []module.Module) *openapi.Doc {
	doc := openapi.New(title, apiVersion)

	for _, mod := range modules {
		describer, canDescribe := mod.(openapi.Describer)
		if !canDescribe {
			continue
		}

		describer.Describe(doc)
	}

	return doc
}

// registerWorkflows sets up the cross-module workflows and leaves them in the
// container.
//
// # Why HERE and why AFTER Bootstrap
//
// A workflow cannot be set up inside any module: each of them resolves the
// surfaces of MORE THAN ONE module by name from the container (cart totals
// needs six, checkout seven) and those surfaces are only registered once the
// WHOLE Register cycle has finished. Had they been set up inside a module's
// Register, the result would be an installation that works or not depending on
// registration order.
//
// The reverse is true as well: the HTTP endpoints of the workflows are owned by
// a MODULE (cart), so the handler needs the workflow and the handler is built
// during Register. The cycle is therefore broken from both ends — registration
// happens here, RESOLUTION happens on the module side, deferred to the first
// request (see linePricing in the cart module). No handler code enters the
// composition root; the only thing that enters is the decision of WHAT GETS
// WIRED.
//
// # Why it STOPS startup
//
// A workflow that could not be set up means a store that cannot add a line to a
// cart and cannot turn it into an order; a server that is up but cannot sell is
// noticed far later than a server that stops at startup. The error message
// names the surface that could not be resolved (see cartwf.FromContainer).
func registerWorkflows(c *container.Container) error {
	cartWorkflows, err := cartwf.FromContainer(c)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeFlowSetupFailed,
			"the cart workflows could not be set up")
	}
	if err := c.Provide(cartwf.InteropName, cartwf.NewInterop(cartWorkflows)); err != nil {
		return err
	}

	// The checkout workflow builds its own cart totals (on the same container,
	// see checkoutwf.FromContainer); it is not shared with the instance above
	// and not sharing it is deliberate — a workflow resolves its own dependency
	// set, we do not inject an object into it.
	checkoutWorkflow, err := checkoutwf.FromContainer(c)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeFlowSetupFailed,
			"the checkout workflow could not be set up")
	}
	if err := c.Provide(checkoutwf.InteropName, checkoutwf.NewInterop(checkoutWorkflow)); err != nil {
		return err
	}

	// The return flow is set up on the same container for the same reason: it
	// resolves its own dependency set rather than being handed one.
	returnWorkflow, err := returnswf.FromContainer(c)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeFlowSetupFailed,
			"the return workflow could not be set up")
	}

	if err := c.Provide(returnswf.InteropName, returnswf.NewInterop(returnWorkflow)); err != nil {
		return err
	}

	// The invoicing flow, on the same container and for the same reason. It is
	// what turns an order into a document: the invoice module knows no orders
	// and the order module knows no documents, so the assembling belongs here
	// (ADR 0001/0006).
	invoicingWorkflow, err := invoicingwf.FromContainer(c)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeFlowSetupFailed,
			"the invoicing workflow could not be set up")
	}

	return c.Provide(invoicingwf.InteropName, invoicingwf.NewInterop(invoicingWorkflow))
}

// registerPanel builds the admin panel and binds its paths.
//
// The panel is neither core nor a module (ADR 0011); it lives in a fourth tree
// and, exactly like the workflows, is fed through a narrow interface resolved
// from the container BY NAME. Delete this line and the panel still compiles but
// is bound NOWHERE — which is precisely the silence the wiring check on the
// arch side was extended to the panel tree to close.
//
// Setup RETURNS an error rather than panicking: a broken template stops startup
// and the failure shows up in deployment, not in front of a user.
func registerPanel(cfg config.Config, c *container.Container, router chi.Router) (*adminui.UI, error) {
	panel, err := adminui.FromContainer(c, cfg.IsShared())
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), codeFlowSetupFailed,
			"the admin panel could not be set up")
	}
	panel.Routes(router)
	return panel, nil
}

// dbConfig builds the connection pool settings from the configuration.
//
// Only the two limits the operator can set are overridden; the lifetimes and
// the connect timeout stay at the package defaults, because nothing measured
// has asked for them yet and a knob nobody turns is a knob nobody tests.
//
// Why the size does not stay hardcoded at all: the pool is the whole process's
// database concurrency ceiling — HTTP requests, the workflow engine and the
// event consumer all draw from it — and the right number depends on a topology
// this repository cannot see. The measurements, the fan-out that makes the
// ceiling easy to miss, and the reason the default is still 10 are in the
// [config.Config.DBMaxConns] godoc.
func dbConfig(cfg config.Config) db.Config {
	pool := db.DefaultConfig(cfg.DatabaseURL)
	pool.MaxConns = cfg.DBMaxConns
	pool.MinConns = cfg.DBMinConns

	return pool
}

// checkSchema builds the document ONCE at startup and reports divergences.
//
// The document is CACHED and the cache refreshes itself when the route tree or
// the description version changes (see [openapi.Doc.Handler]); the build here
// is only for the check, and it also makes the first request cheaper.
// Without the check both failures would stay SILENT: the description of a
// route whose path has changed drops out of the document, while two modules
// with an identically named DTO make the document impossible to build at all —
// and both would only be seen when somebody opened /openapi.json.
//
// Startup does NOT stop (ADR 0007's distinction): a schema is documentation,
// not the product's correctness. A wrong schema breaks no order; closing the
// store over a documentation error would cost more than the failure itself.
func checkSchema(ctx context.Context, doc *openapi.Doc, r chi.Routes, log *slog.Logger) {
	_, err := doc.Build(r)

	if missing := doc.UnmatchedDescriptions(); len(missing) > 0 {
		log.WarnContext(ctx, "openapi: there are descriptions matching no route",
			"records", missing,
			"meaning", "the route's path may have changed or been deleted; the description does not enter the document")
	}

	if err != nil {
		log.ErrorContext(ctx, "the openapi schema could not be built; /openapi.json will return 500",
			"error", err)
	}
}

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
) ([]func(http.Handler) http.Handler, error) {
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
			return nil, errors.Invalid(codeGuardBackendMissing,
				"GUARD_BACKEND=%s was selected but there is no Redis client", config.BackendRedis)
		}

		store, err := redisguard.NewIdempotencyStore(rdb, cfg.RedisKeyPrefix, cfg.IdempotencyTTL)
		if err != nil {
			return nil, err
		}
		opts.IdempotencyStore = store

		// When the limit is off (limit <= 0) the limiter is not built at all;
		// building it would behave like "0 requests" and cut all traffic.
		if cfg.RateLimitPerMinute > 0 {
			limiter, limitErr := redisguard.NewLimiter(rdb, cfg.RedisKeyPrefix, cfg.RateLimitPerMinute, time.Minute)
			if limitErr != nil {
				return nil, limitErr
			}

			opts.Limiter = limiter
			opts.LimitKey = corehttp.TrustedProxyIPKey(cfg.TrustedProxyHops)
		}

		log.Info("guard backend: redis (shared)",
			"key_prefix", cfg.RedisKeyPrefix)

		return withPanelRing(opts, panel), nil
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

	return withPanelRing(opts, panel), nil
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
) []func(http.Handler) http.Handler {
	return append(corehttp.APIGuards(opts),
		corehttp.Scoped(adminui.URLPrefix, nil, panel.CheckOrigin),
		corehttp.Scoped(adminui.URLPrefix, adminui.ExemptPaths(), panel.Protect),
	)
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

// selectPlugins builds the names in the PLUGINS list from the catalog.
//
// An unknown name is an ERROR: skipping it silently would let a misspelled
// plugin be believed "installed", with the absence only noticed on first use.
// The local variable is deliberately NOT called "registry": the module
// registration invariant in internal/arch recognizes the receiver BY NAME, and
// a plugin registry sharing the name of the module registry in main.go makes
// this line look like a module registration to the check.
//
// log is a PARAMETER rather than the package default because the second caller
// is the migrate surface, which prints a table to stdout: it was measured that
// a nil logger here makes the registry fall back to slog's default handler and
// write "the plugins are installed" to stderr in the middle of an operator's
// report.
func selectPlugins(names []string, log *slog.Logger) (*coreplugin.Registry, error) {
	plugins := coreplugin.NewRegistry(log)

	for _, name := range names {
		name = strings.TrimSpace(name)

		constructor, ok := pluginCatalog[name]
		if !ok {
			return nil, errors.Invalid(codeUnknownPlugin,
				"unknown plugin %q (recognized: %s)", name, strings.Join(pluginNames(), ", "))
		}

		plugins.Add(constructor())
	}

	return plugins, nil
}

// pluginNames returns the catalog's names sorted; it is for the error message.
func pluginNames() []string {
	names := make([]string, 0, len(pluginCatalog))
	for name := range pluginCatalog {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

// pluginSettings builds the settings map handed to the plugins from the
// environment.
//
// Plugins ask for their settings by environment variable NAME (e.g.
// STRIPE_API_KEY); the core config CANNOT know those names because a plugin is
// added at compile time. Building the map here gives the same result without
// letting a plugin reach for the os package, and it makes passing fake settings
// possible in a test.
func pluginSettings() map[string]string {
	environ := os.Environ()
	settings := make(map[string]string, len(environ))

	for _, line := range environ {
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		settings[name] = value
	}

	return settings
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

// notificationProviders is the NARROW surface the setup wants from the
// notification provider registry.
//
// A two-method interface is used instead of the concrete registry type: the
// setup depends not on the notification module's service surface but only on
// the two calls listed here, and the check can be exercised with a fake
// registry without bringing the whole module up.
type notificationProviders interface {
	Get(id string) (coreprovider.NotificationProvider, error)
	IDs() []string
}

// That the real registry satisfies this narrow surface is pinned at COMPILE
// time.
//
// Conformance is checked at runtime by container.Resolve's type assertion, and
// a drift there means startup stopping with "the registry does not satisfy the
// expected surface" — that is, at the latest possible moment. This line asks
// the compiler the same question; importing the notification module is already
// allowed for the composition root (see [adminUsers]).
var _ notificationProviders = (*notificationservice.ProviderRegistry)(nil)

// verifyNotificationProvider confirms that the selected provider is REALLY
// registered.
//
// # Why here and not in config
//
// config cannot know the valid names: providers come from plugins and the
// plugin list is decided at compile time. The same split applies to PLUGINS
// (see [selectPlugins]) — config validates the FORM, the composition root
// validates the MEANING.
//
// # Why startup STOPS
//
// The alternative — ignoring the unknown name and falling back to the default
// "log" provider — is exactly what must be avoided: the installation opens, no
// error appears, and order confirmations are only written to the log. The
// failure is noticed while customers are waiting for confirmations, usually
// days later. Stopping at startup moves the failure to the moment the
// configuration is still in hand.
//
// # Why this step comes AFTER Start
//
// The plugins' provider registrations are applied during
// [coreplugin.Registry.Start]; an earlier check would reject a valid name
// coming from a plugin as "unknown".
func verifyNotificationProvider(c *container.Container, id string) error {
	registry, err := container.Resolve[notificationProviders](c, notification.ProvidersName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeUnknownNotificationProvider,
			"the notification provider registry %q could not be resolved", notification.ProvidersName)
	}

	if _, err := registry.Get(id); err != nil {
		return errors.Wrap(err, errors.KindInvalid, codeUnknownNotificationProvider,
			"NOTIFICATION_PROVIDER=%q is not a registered notification provider (registered: %s); "+
				"if a plugin brings it, has it been added to the PLUGINS list?",
			id, strings.Join(registry.IDs(), ", "))
	}

	return nil
}

// fileProviders is the NARROW surface the setup wants from the file provider
// registry.
//
// The rationale is exactly the same as [notificationProviders]'s and is not
// repeated. Merging the two interfaces into a single generic type was not
// attempted because the gain is only a two-line declaration; in exchange the
// container.Resolve call would be written with a generic interface type and a
// type mismatch error would become harder to read — while the one job of this
// code is to produce a diagnosable error.
type fileProviders interface {
	Get(id string) (coreprovider.FileProvider, error)
	IDs() []string
}

// That the real registry satisfies this narrow surface is pinned at COMPILE
// time.
var _ fileProviders = (*fileservice.ProviderRegistry)(nil)

// verifyFileProvider confirms that the selected file provider is REALLY
// registered.
//
// Why the check is here rather than in config, and why it comes AFTER
// [coreplugin.Registry.Start], is written in the [verifyNotificationProvider]
// godoc.
//
// # Why startup STOPS
//
// The cost is DIFFERENT from the notification one and shows up earlier: had an
// unknown name been ignored and the default used, the installation would open
// with the "local" provider and an installation that believes it is writing to
// object storage would write files TO THE LOCAL DISK. When the container
// restarts those files are gone; the records and the product image URLs stay
// where they are. The error would surface in its most expensive form — as data
// loss.
//
// The reverse direction is just as bad: in an installation where "local" was
// NOT REGISTERED because no root directory was given (see file.Options.Root),
// this check says at startup that the upload endpoint will reject every
// request.
func verifyFileProvider(c *container.Container, id string) error {
	registry, err := container.Resolve[fileProviders](c, file.ProvidersName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeUnknownFileProvider,
			"the file provider registry %q could not be resolved", file.ProvidersName)
	}

	if _, err := registry.Get(id); err != nil {
		return errors.Wrap(err, errors.KindInvalid, codeUnknownFileProvider,
			"FILE_PROVIDER=%q is not a registered file provider (registered: %s); "+
				"if a plugin brings it, has it been added to the PLUGINS list, and for the local "+
				"provider has FILE_ROOT been given?",
			id, strings.Join(registry.IDs(), ", "))
	}

	return nil
}

// warnAboutFileRoot logs a warning for a local root directory that is not
// durable.
//
// Why the warning does NOT stop startup, and what the "durable" criterion is,
// are in the [config.Config.LocalFileRootIsDurable] godoc. The only job here is
// to keep the risk visible: a production deployment that goes out with a
// relative or temporary root leaves no trace without this line, and the failure
// is only noticed after the first redeploy — when the images are gone.
//
// The message names BOTH reasons because the criterion covers both; the root
// itself is logged, so the operator sees at a glance which one they hit.
//
// In local development it is SILENT: there a relative root is the right thing,
// and printing a warning on every startup would drown a real warning in noise.
func warnAboutFileRoot(cfg config.Config, log *slog.Logger) {
	if !cfg.IsShared() || cfg.LocalFileRootIsDurable() {
		return
	}

	log.Warn("the file root directory is NOT DURABLE",
		"root", cfg.FileRoot,
		"warning", "a relative path is resolved against the process's WORKING DIRECTORY, and a "+
			"temporary root (/tmp, /var/tmp, /dev/shm or TMPDIR) is cleaned by the operating system; "+
			"either way the uploaded files are lost on redeploy (the URLs stay in the records)",
		"remedy", "give the absolute path of a mounted DURABLE volume as FILE_ROOT")
}

// adminUsers is the NARROW surface the seeding step wants from the auth module.
//
// A two-method interface is used instead of the concrete *service.Service: the
// setup depends not on the whole of auth but only on the two calls listed here,
// and the seeding logic can be exercised with a fake implementation and no
// database. The service is resolved from the container BY NAME
// (auth.ServiceName).
//
// The signatures USE the input/output types from auth's service package and
// that is allowed: what is forbidden is the core knowing the modules (Principle
// 2.4) or the modules knowing each other; cmd/server is the composition root
// and already imports every module.
type adminUsers interface {
	ListUsers(ctx context.Context, in authservice.ListUsersInput) (authservice.Page[authmodels.User], error)
	CreateUser(ctx context.Context, in authservice.CreateUserInput, password string) (authmodels.User, error)
}

// seedAdmin creates the first admin user when there are no users at all.
//
// A server opened against an empty database has no administrator, and because
// the admin endpoints are protected there is no way to create the first one
// over HTTP either; without this step a fresh installation is unusable.
//
// # Only on an empty installation
//
// The step runs while the user count is ZERO, and is otherwise skipped with an
// info log. This is not merely "do not create it twice": the seed NEVER touches
// an existing installation's password or privileges. Had it done so, an
// ADMIN_BOOTSTRAP_PASSWORD forgotten in an env file would silently roll back
// the production administrator's password on every restart and restarting would
// stop being safe.
//
// # What is logged
//
// The password is not logged. NEITHER IS THE EMAIL: the auth module deliberately
// writes only the id when creating a user (see internal/modules/auth/service
// user.go) and it would make no sense for the setup to pierce that decision
// here — the log collector is open to a far wider audience than the admin
// surface, and the user id is enough to answer "which account was created".
//
// The error path is separate: there startup STOPS anyway, and the operator
// seeing the rejected value is necessary for diagnosis, because that is exactly
// what has to be fixed.
func seedAdmin(
	ctx context.Context,
	users adminUsers,
	cfg config.Config,
	log *slog.Logger,
) error {
	// The user count is read IN EVERY CASE. It answers two different questions:
	// if the seed is configured, "should I create it a second time"; if it is
	// not, "is this installation manageable at all". The second used never to
	// be asked, and an installation whose answer was "no" opened silently.
	//
	// The page size is 1: the only fact needed here is "are there any users",
	// not the list itself. Page.Count gives the TOTAL matching the filter, not
	// the number of records on the page.
	page, err := users.ListUsers(ctx, authservice.ListUsersInput{Limit: 1})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeBootstrapFailed,
			"the user count could not be read for the first-administrator seed")
	}

	// config.Validate forces the two to be given TOGETHER; only the "neither
	// given" state falls through to here. Both fields are checked anyway: a
	// hand-built Config that never went through validation must not be able to
	// run the seed with half an entry.
	if cfg.AdminBootstrapEmail == "" || cfg.AdminBootstrapPassword == "" {
		return reportUnmanageableInstallation(ctx, cfg, page.Count, log)
	}

	if page.Count > 0 {
		log.InfoContext(ctx, "the first-administrator seed was skipped: the installation has users",
			slog.Int64("user_count", page.Count))

		return nil
	}

	// The scope list is DELIBERATELY not given: in the auth module a nil slice
	// means "full privileges", and the first administrator must be fully
	// privileged — there is nobody else to grant them anything. Had an empty
	// slice been passed, an account unable to reach any admin endpoint would be
	// born and the system would still be unusable.
	user, err := users.CreateUser(ctx, authservice.CreateUserInput{
		Email: cfg.AdminBootstrapEmail,
	}, cfg.AdminBootstrapPassword)

	switch {
	// A conflict is not a FAILURE, it is a RACE. When several instances open
	// against an empty database at the same time they all see "no users" and
	// they all try to create one; email uniqueness rejects all but one.
	//
	// Treating that as an error and stopping startup would mean two of three
	// replicas entering a restart loop on the first deployment — a deployment
	// that repairs itself but looks broken. The desired end state ("there is an
	// administrator") holds for the losing instances too; the one right thing
	// to do is to carry on.
	//
	// Only a CONFLICT is swallowed: a connection error or an invalid password
	// still stops startup, because for those the desired end state does not
	// hold.
	case errors.IsConflict(err):
		log.InfoContext(ctx, "the first-administrator seed was skipped: another instance created it at the same time")

		return nil
	case err != nil:
		return errors.Wrap(err, errors.KindOf(err), codeBootstrapFailed,
			"the first administrator could not be created")
	}

	log.InfoContext(ctx, "the first administrator was created", slog.String("user_id", user.ID))

	return nil
}

// reportUnmanageableInstallation checks that the installation is manageable
// when no seed has been configured.
//
// # Which failure
//
// A fresh database plus an empty ADMIN_BOOTSTRAP_* pair PASSES config.Validate,
// because leaving both empty is a legitimate choice for an INSTALLED system
// (see [config.Config.AdminBootstrapEmail]) and validation cannot see the
// "installed?" question. If the database is empty too, the result is an
// unmanageable installation: there are no users, /admin/v1 is fully protected
// except for the login endpoint, and there is NO WAY to create the first user
// over HTTP. The storefront surface is closed as well, because the publishable
// key is also minted by an admin endpoint.
//
// The server still opens without a hitch: /health and /ready return green,
// every route is mounted, no log line says anything is missing. The failure
// shows on the first sign-in attempt.
//
// # Why it STOPS in a shared environment
//
// There is NO ambiguity here, and that is the criterion:
// [config.Config.LocalFileRootIsDurable] settles for a warning because it is
// not certain the configuration is wrong; that an installation with zero users
// is unmanageable IS certain. The same certainty stops startup in main.go when
// the authenticator cannot be resolved, and the rationale is identical:
// carrying on with a surface that looks protected but can accept no admin
// request hides the failure until the first sign-in attempt — often days after
// the installation. At that point the way to fix it is not configuration but
// hand-written SQL against the production database.
//
// # Why it does NOT stop in development
//
// The repository's promise is "make up && make run works even without a .env",
// and a developer opening for the first time against a fresh database lands
// exactly in this state. There the cost is next to nothing: the person reading
// the warning is sitting at the terminal that printed it and can reopen within
// seconds using two environment variables. The distinction is the same as
// JWT_SECRET's — a warning in development, a refusal in a shared environment.
func reportUnmanageableInstallation(
	ctx context.Context,
	cfg config.Config,
	userCount int64,
	log *slog.Logger,
) error {
	if userCount > 0 {
		return nil
	}

	if cfg.IsShared() {
		return errors.Invalid(codeAdminBootstrapRequired,
			"the installation has no users and ADMIN_BOOTSTRAP_EMAIL/ADMIN_BOOTSTRAP_PASSWORD were "+
				"not given: because the admin surface is fully protected apart from the login "+
				"endpoint, there is no way to create the first administrator over HTTP (APP_ENV=%s)",
			cfg.AppEnv)
	}

	log.WarnContext(ctx, "the installation has no users",
		"warning", "the admin surface is fully protected apart from the login endpoint and there is "+
			"no way to create the first administrator over HTTP; the storefront surface is closed "+
			"too, because the publishable key is minted by an admin endpoint",
		"remedy", "provide ADMIN_BOOTSTRAP_EMAIL and ADMIN_BOOTSTRAP_PASSWORD and restart")

	return nil
}

// bindErrorReporter hands the plugin's error reporter to the sink the log
// handler already writes to.
//
// Nothing is bound when no plugin registered one, and that is not an error: an
// installation without a collector is the normal one, and the sink drops.
//
// A reporter registered under the name that does NOT satisfy the contract is a
// different matter and stops startup. The alternative is a process that looks
// configured, logs nothing about it, and reports no failure for as long as it
// runs — the failure mode of a monitoring integration is that it is silent, so
// the one moment it can be loud is this one.
func bindErrorReporter(c *container.Container, sink *errorreport.Sink, log *slog.Logger) error {
	if !c.Has(coreplugin.ErrorReporterName) {
		return nil
	}

	reporter, err := container.Resolve[coreprovider.ErrorReporter](c, coreplugin.ErrorReporterName)
	if err != nil {
		return err
	}
	if err := sink.Bind(reporter); err != nil {
		return err
	}
	log.Info("error reporting is on", "reporter", reporter.ID())

	return nil
}

// warnIfShutdownIsShorterThanTheSaga says so when a deploy can cut a checkout in
// half.
//
// The checkout saga runs synchronously inside the HTTP handler, so the only
// thing waiting for it during a graceful shutdown is the HTTP server's own
// budget. When SHUTDOWN_TIMEOUT is shorter than the saga's budget the server
// stops waiting, the process exits, and the saga's goroutine dies wherever it
// was — possibly after the payment was authorized and before anything
// compensated it.
//
// # Why a warning and not a refusal
//
// Neither number is wrong. Fifteen seconds is a sensible deploy budget and it
// matches what orchestrators expect (Kubernetes' default grace period is 30s);
// two minutes is a sensible ceiling for a chain that crosses three modules and
// a payment provider. What is wrong is not KNOWING which one an installation
// picked, and a framework that refused to start over a legitimate pair of
// values would be making an operator's deployment decision for them.
//
// The residue left by a cut saga is no longer silent: the execution's lease
// expires, the next attempt closes it, and one that had already done work is
// marked "manual intervention required" and logged at ERROR (see
// checkoutwf.ExecutionLease). This warning is what lets an operator see the
// exposure BEFORE it happens rather than after.
func warnIfShutdownIsShorterThanTheSaga(ctx context.Context, cfg config.Config, log *slog.Logger) {
	if cfg.ShutdownTimeout >= checkoutwf.SagaTimeout {
		return
	}

	log.WarnContext(ctx,
		"the shutdown budget is shorter than the checkout saga's; a deploy can cut a checkout in half",
		slog.Duration("shutdown_timeout", cfg.ShutdownTimeout),
		slog.Duration("saga_timeout", checkoutwf.SagaTimeout),
		slog.String("effect", "a checkout still running when the budget expires is killed where it "+
			"stands; payment may be authorized with nothing left to compensate it"),
		slog.String("fix", "raise SHUTDOWN_TIMEOUT above the saga budget, and raise the "+
			"orchestrator's grace period with it, or accept the exposure knowingly"),
	)
}

// application is everything the composition root builds: the container with
// every module registered, the router their routes are bound to, and the
// cross-module workflows wired on top.
//
// The struct exists so that a command which must NOT start a server can reach
// the same wiring. [runRecover] is that command: recovering a half-done saga
// runs the checkout flow's own Compensate functions, and those need the very
// module services the server resolves. A second copy of this wiring would drift
// the day a module was added to one and not the other — the failure class
// TestEveryModuleIsRegisteredInTheCompositionRoot exists for.
type application struct {
	// container holds every service, resolved by name (ADR 0001).
	container *container.Container
	// registry is the module set; its Modules() feeds the OpenAPI description.
	registry *module.Registry
	// router carries the module routes. A command that does not serve still
	// gets one: the modules bind their routes during Bootstrap and there is no
	// registration path that skips it.
	router chi.Router
	// plugins and host carry the plugin registrations that are APPLIED later
	// (Registry.Start), not here.
	plugins *coreplugin.Registry
	host    *coreplugin.Host
	// panelRing and authn are the two deferred bindings the server fills in
	// after this function returns.
	panelRing *adminui.Ring
	authn     *corehttp.DeferredAuthenticator
}

// openApplication opens the database, brings up every module and wires the
// workflows.
//
// It does everything the running server does UP TO the point where serving
// begins, and nothing after it: the plugins' queued registrations are not
// applied ([coreplugin.Registry.Start]), the panel is not bound, no
// administrator is seeded and no listener is opened. Applying the plugin queue
// here would start event-bus consumers in a process that exits a second later,
// and a consumer that claims a message and dies is worse than one that never
// ran.
//
// The returned close function releases the container services and then the
// pool, in that order — the same order the deferred calls had.
func openApplication(
	ctx context.Context,
	cfg config.Config,
	log *slog.Logger,
	reportSink *errorreport.Sink,
) (*application, func(), error) {
	c := container.New(log)

	pool, err := db.New(ctx, dbConfig(cfg), log)
	if err != nil {
		return nil, nil, err
	}
	closeApp := func() {
		shutdownContainer(ctx, c, cfg, log)
		pool.Close()
	}

	if err := c.Provide(svcDB, pool); err != nil {
		return nil, nil, err
	}

	// The core migrations are applied BEFORE the module migrations: a module
	// must be able to assume the workflow engine's schema is ready.
	//
	// The list is READ from [coreMigrationSources] rather than written out
	// here, because the migrate subcommands read the same list; a second
	// literal would mean a core schema added to one of them and reported by
	// neither the status table nor this loop.
	for _, source := range coreMigrationSources() {
		if err := db.Migrate(ctx, cfg.DatabaseURL, source.src, source.owner); err != nil {
			return nil, nil, err
		}
	}

	links := link.New(pool, log)
	if err := c.Provide(svcLink, links); err != nil {
		return nil, nil, err
	}
	if err := c.Provide(svcQuery, query.New(links, c, log)); err != nil {
		return nil, nil, err
	}

	workflowStore := pgstore.New(pool, log)
	if err := c.Provide(svcWorkflowStore, workflowStore); err != nil {
		return nil, nil, err
	}
	if err := c.Provide(svcWorkflow, workflow.New(workflowStore, log)); err != nil {
		return nil, nil, err
	}

	// Postgres GATES traffic and Redis does not; which side a dependency lands
	// on is decided by what its loss does to a request, and the two answers are
	// on [corehttp.RouterOptions.ReadinessChecks] and
	// [corehttp.RouterOptions.DegradedChecks]. Postgres is here because there
	// is no endpoint that answers correctly without it — every read and every
	// write goes through this pool.
	checks := corehttp.GatingChecks{"postgres": pool.Ping}
	degraded := corehttp.DegradingChecks{}

	// The Redis client is SHARED by the event bus and the guard backend; if
	// both are in-memory it is never opened and stays nil.
	redisClient, err := setupRedis(ctx, c, cfg, degraded, log)
	if err != nil {
		return nil, nil, err
	}

	bus, err := setupEventBus(ctx, cfg, redisClient, log)
	if err != nil {
		return nil, nil, err
	}
	if err := c.Provide(svcEventBus, bus); err != nil {
		return nil, nil, err
	}

	// The authenticator is born when the auth module registers, while the guard
	// middleware has to be attached while the router is built. The deferred
	// authenticator bridges the gap (see setup.go).
	authn := &corehttp.DeferredAuthenticator{}

	// The panel ring is born BEFORE the router and receives the panel AFTER: a
	// ring cannot be added once routes are registered, and the panel waits for
	// services resolved from the container. A request arriving before the bind
	// is REJECTED.
	panelRing := &adminui.Ring{}

	guards, err := guardStack(cfg, authn, panelRing, redisClient, pool, log)
	if err != nil {
		return nil, nil, err
	}

	router := corehttp.NewRouter(corehttp.RouterOptions{
		Version:         version,
		Logger:          log,
		ReadinessChecks: checks,
		DegradedChecks:  degraded,
		// The degrading budget is an operator's number, not a constant in the
		// binary: a Redis across a network can be healthy and still answer
		// slower than the default 250ms, and an installation that had to fork
		// the binary to say so would be paying for our tidiness.
		DegradedCheckTimeout: cfg.ReadinessDegradedTimeout,
		TelemetryService:     cfg.ServiceName,
		Middlewares:          guards,
	})

	registry := module.NewRegistry(log, func(ctx context.Context, src fs.FS, owner string) error {
		return db.Migrate(ctx, cfg.DatabaseURL, src, owner)
	})
	registerModules(registry, cfg, log)

	// The plugins are installed BEFORE the modules: a module brought in by a
	// plugin must be able to go through the Register/migration/route cycle too.
	pluginRegistry, host, err := installPlugins(ctx, cfg, c, registry, bus, log)
	if err != nil {
		return nil, nil, err
	}

	// The reporter is bound between Install and Bootstrap, which is the only
	// window that works. Earlier there is no plugin to provide one; later the
	// modules have already come up, and a migration that fails takes the process
	// down — unreported by a reporter that was still waiting for its turn.
	if err := bindErrorReporter(c, reportSink, log); err != nil {
		return nil, nil, err
	}

	if err := registry.Bootstrap(ctx, c, router); err != nil {
		return nil, nil, err
	}

	// The cross-module workflows can only be set up HERE: each of them resolves
	// the surfaces of several modules by name from the container, and those
	// surfaces are not registered before Bootstrap. Their endpoints, however,
	// are owned by a module (cart), so the handler needs the workflow; the
	// cycle is broken by deferring the module-side resolution to the first
	// request (see setup.go, registerWorkflows).
	//
	// Delete this line and no line can be added to a cart and no cart can be
	// turned into an order: the pricing path fails CLOSED, so no line is
	// written with the client's price or with a zero price. The entire workflow
	// chain of Phases 5-7 — pricing, discounts, tax, payment, fulfillment, the
	// order.placed notification and the b2b spending limit — is attached to the
	// production binary exactly here.
	if err := registerWorkflows(c); err != nil {
		return nil, nil, err
	}

	return &application{
		container: c,
		registry:  registry,
		router:    router,
		plugins:   pluginRegistry,
		host:      host,
		panelRing: panelRing,
		authn:     authn,
	}, closeApp, nil
}

// newAuditID produces an audit row's identifier.
//
// It is a plain random id rather than something derived from the request: two
// writes on the same path by the same actor in the same second are two
// different facts, and a derived id would collapse them.
func newAuditID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand failing is a broken machine rather than a condition to
		// handle; an id that repeats would silently drop audit rows on the
		// primary key, which is the one outcome this table must not have.
		panic("audit id could not be generated: " + err.Error())
	}

	return "audit_" + hex.EncodeToString(raw[:])
}
