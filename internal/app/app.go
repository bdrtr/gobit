// Package app is the composition root of a gobit installation.
//
// The flow: load config -> set up the logger and observability -> build the
// container -> register the infrastructure services (Postgres, Redis, the event
// bus) -> install the plugins -> bootstrap the modules -> start the plugins ->
// listen.
//
// This package is the architecture's ONLY "knows everything" point: the core
// does not know the modules, the modules do not know each other, and the
// plugins do not know the commerce modules. Every decision about who talks to
// whom is made here, explicitly.
//
// The price of being that single point is this: a module NOT ADDED here does
// not EXIST in any installation — its migrations are not applied, its service
// does not enter the container, its endpoints are not mounted — and the
// module's own tests cannot see it, because they build the module themselves.
// The admin surface of Phase 8/9 and b2b's spending limit disappeared in
// exactly this way. The requirement is therefore checked from the outside: see
// internal/arch/registration_test.go, TestEveryModuleIsRegisteredInTheCompositionRoot.
//
// # Why this is not package main
//
// It was, until ADR 0027. A composition root inside package main can only be
// reached by editing it, which is the fork model ADR 0025 rejected: an
// embedding program had no way to add its own module or plugin without owning
// a copy of these files. The lifecycle is the part a customer project should
// not have to write, so it lives in a package the published facade can call.
// It stays UNDER internal/ because none of it is a contract — an outside
// program names [github.com/bdrtr/gobit.App], never anything here.
package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/redis/go-redis/v9"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errorreport"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/module"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/core/logger"
	"github.com/bdrtr/gobit/internal/core/observability"
	"github.com/bdrtr/gobit/internal/modules/auth"
	"github.com/bdrtr/gobit/internal/modules/b2b"
	"github.com/bdrtr/gobit/internal/modules/cart"
	"github.com/bdrtr/gobit/internal/modules/customer"
	"github.com/bdrtr/gobit/internal/modules/file"
	"github.com/bdrtr/gobit/internal/modules/fulfillment"
	"github.com/bdrtr/gobit/internal/modules/inventory"
	"github.com/bdrtr/gobit/internal/modules/invoice"
	"github.com/bdrtr/gobit/internal/modules/notification"
	"github.com/bdrtr/gobit/internal/modules/order"
	"github.com/bdrtr/gobit/internal/modules/payment"
	"github.com/bdrtr/gobit/internal/modules/pricing"
	"github.com/bdrtr/gobit/internal/modules/product"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/promotion"
	"github.com/bdrtr/gobit/internal/modules/region"
	"github.com/bdrtr/gobit/internal/modules/review"
	"github.com/bdrtr/gobit/internal/modules/tax"
)

// The names of the infrastructure services in the container. Modules resolve
// them by these names.
const (
	svcDB       = "core.db"
	svcRedis    = "core.redis"
	svcEventBus = "core.eventbus"
	// svcWorkflow is the saga engine; the cross-module workflows run from here.
	svcWorkflow = "core.workflow"
	// svcWorkflowStore is the durable store of the execution state.
	svcWorkflowStore = "core.workflow.store"
	// svcLink is the Module Links service; modules declare their link
	// definitions through it.
	svcLink = "core.link"
	// svcQuery is the cross-module read layer.
	svcQuery = "core.query"
)

// Options is what an embedding program contributes to the installation.
//
// Everything in it is ADDITIVE: the modules and plugins gobit ships are
// registered either way, and these are added alongside them. There is
// deliberately no way to remove one — a module missing from an installation is
// a schema that does not exist, and letting a caller subtract one would make
// "which tables does gobit have" an unanswerable question.
type Options struct {
	// Version is reported in the startup log, in the OpenAPI document and as
	// the service version on every trace. Empty means "dev".
	Version string
	// Modules are the caller's own commerce modules. They are registered after
	// the ones in the box, and the registry rejects a name that already exists.
	Modules []module.Module
	// Plugins are the caller's own plugins. They are installed after the ones
	// the configuration names, in the order given.
	Plugins []coreplugin.Plugin
}

// version reports what to call this build.
func (o Options) version() string {
	if o.Version == "" {
		return "dev"
	}

	return o.Version
}

// Main picks what this invocation does and is the ONLY place that can pick
// [serve].
//
// # Why the dispatch is a switch and not a CLI library
//
// This once said "three verbs and two flags", and by the time it was read
// again the binary answered seven, two of them with subverbs of their own. The
// count is dropped rather than corrected, because a number in a godoc goes
// stale in silence; what has to be re-argued each time the surface grows is
// the CHOICE, and it still holds. The growth has all been in one shape: every
// verb parses its own flags with its own [flag.FlagSet] and prints its own
// report, so what a CLI framework would replace is exactly this switch and
// nothing below it — at the price of a dependency, its own flag semantics and
// its own help renderer. The rule from ADR 0001 that a dependency has to earn
// its place applies to the binary's own front door too.
//
// # Why the binary grows subcommands at all
//
// The composition root is the only place that knows how to reach THIS
// installation's database, and some operator work is nothing but a query
// against it. That is why the migrate verbs live here rather than in a second
// binary with its own configuration loading, and it is why [stuckCommand] —
// the read surface for half-done sagas — lands here too. The same argument
// brought [deadLettersCommand]: the events the outbox relay gave up on are a
// core table, and the two verbs that act on them had no reachable caller at
// all before it.
//
// # Why starting the server has NO verb
//
// Before this function existed the binary parsed no arguments at all: it was
// MEASURED that `gobit migrate status` and `gobit --help` each booted a full
// server, applied every forward migration and listened on the configured port —
// the operator's typo was indistinguishable from a deploy. The invariant that
// replaces it is structural rather than careful: [serve] is called from exactly
// one branch, the one where args is EMPTY, so no present or future subcommand
// can reach it by falling through. An unrecognized argument is an error, never
// a server.
//
// out carries the operator-facing report of the migrate commands (a status
// table, a rollback plan). It is a parameter so the tests can read what an
// operator would see; errors are returned, and the calling program decides how
// an operator sees them.
func Main(args []string, out io.Writer, opts Options) error {
	if len(args) == 0 {
		return serve(opts)
	}

	switch args[0] {
	case cmdHelp, "-h", "-help", "--help":
		return writeReport(out, usageText(opts.version()))
	case cmdMigrate:
		return runMigrate(args[1:], out, opts)
	case stuckCommand:
		return runStuck(args[1:], out, opts)
	case recoverCommand:
		return runRecover(args[1:], out, opts)
	case jobsCommand:
		return runJobs(args[1:], out, opts)
	case deadLettersCommand:
		return runDeadLetters(args[1:], out, opts)
	case seedCommand:
		return runSeed(args[1:], out, opts)
	default:
		if err := writeReport(out, usageText(opts.version())); err != nil {
			return err
		}

		return errors.Invalid(codeUnknownCommand,
			"unknown command %q; the server starts with NO arguments", args[0])
	}
}

// serve drives the server's whole lifecycle and returns on the first error.
// It is kept apart from main because os.Exit skips deferred calls.
func serve(opts Options) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// The sink is built BEFORE the logger and stays empty until a plugin fills
	// it. That order is forced: the log handler has to be wired to something at
	// the moment it is created, and the reporter arrives with a plugin, which is
	// installed several hundred lines below. An empty sink drops, so the
	// installation without a collector — the usual one — pays a nil check per
	// failing request and nothing else.
	reportSink := errorreport.NewSink()

	log := logger.New(logger.Options{
		Level:      cfg.SlogLevel(),
		Format:     cfg.LogFormat,
		AddSource:  !cfg.IsProduction(),
		Middleware: errorreport.Middleware(reportSink, errorreport.Options{}),
	})
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("gobit is starting",
		"version", opts.version(),
		"env", cfg.AppEnv,
		"log_level", cfg.LogLevel,
		"event_bus", cfg.EventBus,
		"plugins", cfg.Plugins,
	)

	// The application opens EVEN IF observability setup FAILS (ADR 0007):
	// observability exists for the product's visibility, not its correctness,
	// and an outage at the collector must not close the store. When no OTLP
	// endpoint is given, no outbound connection is attempted at all.
	shutdownObservability, err := observability.Setup(ctx, observability.Options{
		Endpoint:       cfg.OTLPEndpoint,
		Insecure:       cfg.OTLPInsecure,
		ServiceName:    cfg.ServiceName,
		ServiceVersion: opts.version(),
		Environment:    cfg.AppEnv,
		SampleRatio:    cfg.TraceSampleRatio,
		MetricInterval: cfg.MetricInterval,
		Logger:         log,
	})
	if err != nil {
		log.Warn("observability could not be set up, continuing without it", "error", err)
	}
	// The shutdown context must NOT be canceled: SIGTERM will already have
	// canceled ctx and the pending spans would be dropped before they could be
	// sent.
	defer func() {
		if err := shutdownObservability(context.WithoutCancel(ctx)); err != nil {
			log.Error("observability could not be shut down", "error", err)
		}
	}()

	// The flush is deferred HERE, right after the sink exists, so it runs no
	// matter which of the returns below is taken. Its own context is detached
	// from ctx for observability's reason: SIGTERM has already canceled ctx, and
	// the reports of the failures that ended the process are the ones most worth
	// having.
	defer func() {
		if err := reportSink.Close(context.WithoutCancel(ctx)); err != nil {
			log.Error("the error reporter could not be flushed", "error", err)
		}
	}()

	app, closeApp, err := openApplication(ctx, cfg, log, reportSink, opts)
	if err != nil {
		return err
	}
	defer closeApp()

	c, registry, router := app.container, app.registry, app.router
	pluginRegistry, host := app.plugins, app.host
	panelRing, authn := app.panelRing, app.authn

	// The admin panel is set up AFTER the workflows: it resolves the read
	// surface from the container and that surface is not registered before
	// Bootstrap. The panel is not a module (ADR 0011), so it does not enter the
	// registry; the check for its wiring is the branch of the registration test
	// in internal/arch that was extended to the panel tree.
	panel, err := registerPanel(cfg, c, router)
	if err != nil {
		return err
	}
	panelRing.Bind(panel)

	// The authenticator is only in the container after Bootstrap. If it cannot
	// be resolved, startup STOPS: carrying on with an admin surface that looks
	// protected but rejects every request would hide the failure until the
	// first sign-in attempt.
	authenticator, err := container.Resolve[corehttp.Authenticator](c, auth.InteropName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), "auth_interop_missing",
			"the authenticator %q could not be resolved", auth.InteropName)
	}
	authn.Bind(authenticator)

	// The first-administrator seed also runs AFTER Bootstrap: the auth service
	// is only in the container by then and the tables are only migrated by
	// then. The service is taken through a NARROW interface (see setup.go), not
	// by its concrete type.
	//
	// An error STOPS startup: an administrator that could not be created means
	// a system with no admin surface, and that is noticed far sooner than a
	// server which opens and then accepts no admin request at all.
	users, err := container.Resolve[adminUsers](c, auth.ServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeBootstrapFailed,
			"the auth service %q could not be resolved", auth.ServiceName)
	}
	if err := seedAdmin(ctx, users, cfg, log); err != nil {
		return err
	}

	// Provider and subscription registrations are applied AFTER the modules are
	// up; routes are bound after the module routes.
	if err := pluginRegistry.Start(ctx, host); err != nil {
		return err
	}
	// The notification provider can only be checked HERE: providers brought by
	// plugins are registered during Start, and when the module registers, the
	// registry holds only the provider that ships in the box.
	if err := verifyNotificationProvider(c, cfg.NotificationProvider); err != nil {
		return err
	}
	// The file provider is checked here for the same reason; an unknown name
	// STOPS startup.
	if err := verifyFileProvider(c, cfg.FileProvider); err != nil {
		return err
	}

	// A plugin route shadowing an existing path STOPS STARTUP. Swallowing the
	// error would mean carrying on with an installation where a module endpoint
	// has silently been taken over by a plugin, or where the plugin was never
	// bound at all; both would only be noticed when the first request went to
	// the wrong place.
	// The callbacks are bound BEFORE the plugin routes, and the order carries
	// the rule: a callback path is claimed first, so a plugin's own AddRoutes
	// cannot shadow one — MountRoutes' conflict check sees the callback pattern
	// already on the router and refuses the second binding by name.
	//
	// Mounting also FREEZES the registry. A callback registered after this point
	// would never be bound, and a route the provider can reach that this ring
	// does not know is exactly the unguarded endpoint the registry exists to
	// remove; the refusal is loud rather than silent.
	if err := app.callbacks.Mount(router); err != nil {
		return err
	}

	if err := pluginRegistry.MountRoutes(router, host); err != nil {
		return err
	}

	// The OpenAPI schema is GENERATED from the router tree, not written by
	// hand: a hand-written schema starts lying silently at the first route
	// change. The endpoint publishes only the route PATTERNS, not data.
	//
	// The module list is READ from the registry; no second list is kept here:
	// modules brought in by plugins (see searchpg) appear only in the registry,
	// and a hand-maintained list would silently leave them undescribed.
	doc := describeAPI(cfg.ServiceName+" API", opts.version(), registry.Modules())
	router.Get(openAPIPath, doc.Handler(router))
	checkSchema(ctx, doc, router, log)

	warnIfShutdownIsShorterThanTheSaga(ctx, cfg, log)

	// The job runner is started HERE and not in openApplication: the migrate,
	// stuck and recover subcommands build the same application and must not
	// begin running scheduled work as a side effect of an operator reading
	// something.
	//
	// Stop() is deferred rather than left to the context, because the runner
	// waits for the pass it is in the middle of. Without it a shutdown could
	// close the pool underneath a job that is still writing.
	jobs, stopJobs, err := startJobs(ctx, c, host, cfg, log)
	if err != nil {
		return err
	}
	defer stopJobs()
	_ = jobs

	// The profiling listener is separate and comes up before the API server so
	// that a boot slow enough to be worth profiling can be profiled.
	waitForProfiling := startProfiling(ctx, cfg, log)
	defer waitForProfiling()

	srv := corehttp.NewServer(corehttp.ServerOptions{
		Addr:              cfg.Addr(),
		Handler:           router,
		Logger:            log,
		ShutdownTimeout:   cfg.ShutdownTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	})

	return srv.Run(ctx)
}

// registerModules adds every commerce module this binary ships to the registry.
//
// # Why the list is a function and not inline in serve
//
// A module's migration source is reachable only through the module VALUE
// (module.Module.Migrations), so anything that wants to speak about a module's
// schema has to build the same modules the server builds. That second caller
// now exists: the migrate subcommands ([migrationSources]). Keeping the
// registrations inline in [serve] would have forced them to keep a list of
// their own, and a module added to one list and not the other is exactly the
// failure internal/arch TestEveryModuleIsRegisteredInTheCompositionRoot was written for —
// except that check reads THIS registry, so it would go on passing while
// `migrate status` quietly stopped reporting an owner.
//
// Nothing here touches the database or the network: the constructors only build
// values. That is what makes the same call safe for a command that must not
// start a server.
func registerModules(registry *module.Registry, cfg config.Config, log *slog.Logger,
	extra []module.Module,
) {
	// The commerce modules. The ORDER DOES NOT MATTER: the registry moves on to
	// the migration and route steps only AFTER every module has registered, so
	// one module's handler can safely resolve another module's service.
	// Phase 4: catalog
	//
	// The limits of the GraphQL read surface come from configuration: at this
	// endpoint the cost of a request is decided by WHOEVER WRITES THE QUERY,
	// and how many levels or how many fields are acceptable depends on the
	// installation's hardware and catalog size. Because the module does not
	// know the config package (Principle 2.4), the values are passed in from
	// here as parameters.
	registry.Add(product.New(product.Options{
		GraphQL: graph.Options{
			MaxDepth:      cfg.GraphQLMaxDepth,
			MaxComplexity: cfg.GraphQLMaxComplexity,
			// The five limits below bind the costs depth and complexity CANNOT
			// SEE: the same heavy field multiplied through aliases, the
			// realized response bytes, the two dimensions of an introspection
			// flood, and exponential fragment expansion. All five pass through
			// here because every one of them has to be tunable by the
			// operator — a limit that cannot be tuned forces an installation to
			// fork the code.
			MaxFieldRepetition:    cfg.GraphQLMaxFieldRepetition,
			MaxResponseBytes:      cfg.GraphQLMaxResponseBytes,
			MaxIntrospectionRoots: cfg.GraphQLMaxIntrospectionRoots,
			MaxIntrospectionDepth: cfg.GraphQLMaxIntrospectionDepth,
			MaxSelections:         cfg.GraphQLMaxSelections,
			// Because the field is named NEGATIVELY (its zero value must give
			// the package default, that is, introspection ON) the value is
			// inverted here; the environment variable asks the operator the
			// positive question.
			IntrospectionDisabled: !cfg.GraphQLIntrospection,
		},
	}))
	registry.Add(pricing.New(log))
	registry.Add(inventory.New())
	// Phase 5: the cart flow
	registry.Add(region.New(log))
	registry.Add(customer.New(log))
	registry.Add(cart.New())
	// Phase 6: payment and order
	registry.Add(payment.New())
	registry.Add(order.New())
	// Phase 7: fulfillment, promotion, tax
	registry.Add(fulfillment.New())
	registry.Add(promotion.New(log))
	registry.Add(tax.New(log))
	// Notification. It is the FIRST real consumer of the "order.placed" event;
	// it does not bind to the order module, it listens for the event and reads
	// the contact details from the "order.interop" surface. Which provider is
	// used is chosen by configuration; that the name is registered is checked
	// AFTER the plugins have been loaded too (see verifyNotificationProvider).
	registry.Add(notification.New(notification.Options{
		ProviderID: cfg.NotificationProvider,
		Logger:     log,
	}))
	// File. The URL an upload produces plugs straight into the product image
	// flow; the module never touches product. The provider choice and the
	// limits come from configuration, and that the name is registered is
	// checked AFTER the plugins have been loaded too (see verifyFileProvider).
	registry.Add(file.New(file.Options{
		ProviderID:     cfg.FileProvider,
		Root:           cfg.FileRoot,
		MaxUploadBytes: cfg.FileMaxUploadBytes,
		AllowedTypes:   cfg.FileAllowedTypes,
		Logger:         log,
	}))
	warnAboutFileRoot(cfg, log)
	// Phase 8: identity. It is independent of the other modules; it only asks
	// for the core pool and, in return, leaves in the container the
	// authenticator the guard middleware needs.
	registry.Add(auth.New(auth.Options{
		JWTSecret: jwtSecret(cfg, log),
		JWTTTL:    cfg.JWTTTL,
		JWTIssuer: cfg.ServiceName,
		Logger:    log,
	}))
	// Section 10: B2B. The installation where the buyer is not an individual
	// but an EMPLOYEE with a limited spending authority. The module touches no
	// other module; it leaves the spending rule in the container under the name
	// "b2b.interop" and order resolves it through ITS OWN narrow interface (see
	// order.SpendingPolicyName).
	//
	// An installation selling pure B2C CAN DELETE this line; order then finds
	// no rule and counts every customer as unlimited — which is the correct
	// behavior, as if b2b never existed. But deleting it is a CODE change and
	// that it stays one is deliberate: an environment variable that turned it
	// off would, when flipped by accident, remove the spending limit without
	// producing a single error — that is, it would be yet another setting that
	// breaks an installation silently. Turning it off in code cannot be done
	// halfway either: the module registration check (internal/arch,
	// TestEveryModuleIsRegisteredInTheCompositionRoot) asks whoever deletes the line to write
	// the decision down with its rationale.
	//
	// The cost of KEEPING the module in a B2C installation is small and
	// visible: two empty tables and a spending rule that never triggers because
	// there are no company records.
	registry.Add(b2b.New(log))
	// Invoice. It knows no other module: a workflow (or an operator) hands it a
	// finished document and it gives that document a number no other document
	// in its series will ever have. It is registered LAST because nothing else
	// depends on it — the dependency runs the other way, and only through a
	// caller that already holds both.
	registry.Add(invoice.New(invoice.Options{Logger: log}))
	// Review. It knows no other module either: what a review is ABOUT is a
	// product identifier it stores and never validates, the same rule an order
	// line follows for the variant it sold.
	//
	// Registering it opens a STOREFRONT WRITE, and that is a decision rather
	// than a wiring detail — decision A15 in docs/gaps.md. The write is
	// accepted from a party this framework cannot identify, and what makes that
	// acceptable is the same argument the order module's return request rests
	// on: nothing the writer does has any effect until an operator approves it.
	// The module is built around that sentence — the storefront's reads carry
	// status = 'approved' as a literal in their SQL — so a shop that registers
	// this line is not opening a page anyone can write onto, it is opening a
	// queue somebody has to work through.
	//
	// An installation that wants no reviews at all DELETES this line, the way a
	// pure B2C installation may delete b2b's: the table then does not exist,
	// the endpoints are not mounted, and nothing else changes.
	registry.Add(review.New(review.Options{Logger: log}))

	// The embedding program's own modules come LAST, for the same reason
	// invoice does: nothing in the box depends on them, and the registry
	// refuses a name that is already taken rather than replacing it.
	for _, m := range extra {
		registry.Add(m)
	}
}

// installPlugins selects the plugins named in the configuration and runs their
// Setup phase against a host bound to registry.
//
// It is three lines pulled into a function for the same reason
// [registerModules] is: a plugin may bring a MODULE OF ITS OWN, with its own
// table and its own migration — searchpg does — and that module exists nowhere
// but in the registry this call fills. The migrate subcommands have to see it
// too, or `migrate status` would silently omit an owner whose schema is in the
// database and `migrate down searchpg` would answer "unknown owner" about a
// module the server migrates on every boot.
//
// Setup neither connects nor migrates: the host QUEUES the provider and
// subscriber registrations for [github.com/bdrtr/gobit/core/plugin.Registry.Start],
// which the migrate path never calls. That is why bus may be nil there.
func installPlugins(
	ctx context.Context,
	cfg config.Config,
	c *container.Container,
	registry *module.Registry,
	bus eventbus.EventBus,
	log *slog.Logger,
	extra []coreplugin.Plugin,
) (*coreplugin.Registry, *coreplugin.Host, error) {
	plugins, err := selectPlugins(cfg.Plugins, log)
	if err != nil {
		return nil, nil, err
	}
	// A plugin handed in by the embedding program is not named in the
	// configuration and does not have to be: the program that compiled it in
	// has already made the selection that PLUGINS names for the ones in the box.
	for _, p := range extra {
		plugins.Add(p)
	}

	host := coreplugin.NewHost(c, registry, bus, log, pluginSettings())
	if err := plugins.Install(ctx, host); err != nil {
		return nil, nil, err
	}

	return plugins, host, nil
}

// setupRedis opens the Redis client if it is needed, registers it in the
// container and adds it to the DEGRADING readiness checks.
//
// If it is not needed it returns (nil, nil) and NO connection is attempted:
// producing a "could not connect" warning in an installation that does not need
// Redis would drown a real failure in noise.
//
// # Why the probe degrades instead of gating
//
// Redis is degrading, not gating, and the map it is written into is the whole
// decision — see [corehttp.RouterOptions.DegradedChecks] for why, and ADR 0007
// for the identical decision one layer down, where the guard middlewares
// already choose per component what a Redis failure costs.
//
// Both users of this client survive its loss, and neither survival is a guess:
//
//   - the guard backend, measured in TestRedisOutageMeasurement — catalog read
//     200, unkeyed write 200, keyed write a retryable per-request 503;
//   - the event bus, whose publish failure is swallowed by design at the call
//     site (see the order service's publishOrderPlaced: the order is WRITTEN
//     and the lost event is logged at ERROR, because failing there would make
//     the saga compensate an order that exists).
//
// Registering it as a gating check would have taken every replica out of the
// load balancer during a Redis failover — all of them at the same instant,
// since they share this one Redis — and turned a degradation that serves 200s
// into a storefront that serves nothing.
//
// The startup ping below is NOT the same decision and stays fail-closed: an
// unreachable Redis at boot is far more likely a wrong REDIS_URL than a
// failover, and an installation that silently ran on a guard backend it never
// reached is precisely the "believed to work while it does not" case
// guardStack refuses. The cost is that a pod restarting DURING an outage
// crashloops until Redis is back; that is loud and it does not remove the
// replicas that are already serving.
func setupRedis(
	ctx context.Context,
	c *container.Container,
	cfg config.Config,
	degraded corehttp.DegradingChecks,
	log *slog.Logger,
) (*redis.Client, error) {
	if !cfg.NeedsRedis() {
		return nil, nil //nolint:nilnil // "Redis is not needed" is not an error
	}

	opt, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, "redis_url_invalid",
			"REDIS_URL could not be parsed")
	}

	client := redis.NewClient(opt)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, errors.Wrap(err, errors.KindUnavailable, "redis_unreachable",
			"Redis could not be reached (%s)", opt.Addr)
	}

	// The order matters: the container closes in reverse registration order, so
	// the event bus closes BEFORE the client.
	if err := c.Provide(svcRedis, client); err != nil {
		_ = client.Close()
		return nil, err
	}

	degraded["redis"] = func(ctx context.Context) error { return client.Ping(ctx).Err() }
	// The readiness policy is logged next to the backend flags because it is
	// the line an operator reads when deciding what a Redis alert means. Left
	// unsaid, "the pods are still Ready" during an outage reads as a broken
	// probe rather than the decision it is.
	log.InfoContext(ctx, "redis connected",
		"addr", opt.Addr,
		"event_bus", cfg.EventBus == config.BackendRedis,
		"guard_backend", cfg.GuardBackend == config.BackendRedis,
		"readiness", "degrading: a Redis outage reports \"degraded\" on /ready and does NOT take this instance out of traffic",
	)

	return client, nil
}

// setupEventBus builds the event bus according to the configuration.
//
// The Redis client is a PARAMETER and is not opened here: the guard backend
// uses the same client, and opening a separate connection in two places would
// split the shutdown order and the health check in two.
//
// # Why the namespace is given HERE
//
// Had a zero-valued [eventbus.RedisConfig] been passed, both the stream prefix
// and the consumer group would fall back to the package default and
// REDIS_KEY_PREFIX would NEVER reach the event side: the result would be an
// installation whose guard keys are separated and whose events are not. Sharing
// the group is the worse of the two; the rationale is in the
// [eventbus.RedisConfig.WithNamespace] godoc.
//
// The consumer name is SEPARATE from the namespace and is resolved per process;
// why it is logged is in the [eventbus.ConsumerName] godoc.
func setupEventBus(
	ctx context.Context,
	cfg config.Config,
	client *redis.Client,
	log *slog.Logger,
) (eventbus.EventBus, error) {
	if cfg.EventBus != config.BackendRedis {
		warnAboutEventBus(ctx, cfg, log)

		return eventbus.NewInMemory(log), nil
	}

	busCfg := eventbus.RedisConfig{
		Consumer: eventbus.ConsumerName(cfg.EventBusConsumer),
	}.WithNamespace(cfg.RedisKeyPrefix)

	bus, err := eventbus.NewRedisStream(client, busCfg, log)
	if err != nil {
		return nil, err
	}

	log.InfoContext(ctx, "event bus: Redis Streams",
		"stream_prefix", busCfg.StreamPrefix,
		"group", busCfg.Group,
		"consumer", busCfg.Consumer)

	return bus, nil
}

// warnAboutEventBus reports the risk of the in-memory bus in a shared
// environment.
//
// Its cost is in the same class as GUARD_BACKEND=memory's, and that one already
// warns (see guardStack); logging the two at different levels would have meant
// the same trade-off being visible in one and invisible in the other.
//
// The in-memory bus WORKS — every instance handles its own events — but it is
// NOT DURABLE: delivery is asynchronous, and if the process crashes or shutdown
// does not finish within SHUTDOWN_TIMEOUT, undelivered events vanish without a
// trace. Concretely, that is a customer whose order was placed but whose
// confirmation was never sent; no error appears and no record is missing.
//
// In local development it stays at INFO: there a single process runs, a lost
// event costs nothing, and printing a warning on every startup would drown a
// real warning in noise.
func warnAboutEventBus(ctx context.Context, cfg config.Config, log *slog.Logger) {
	if !cfg.IsShared() {
		log.InfoContext(ctx, "event bus: in-memory (single process)")

		return
	}

	log.WarnContext(ctx, "event bus: in-memory (single process)",
		"warning", "events are NOT DURABLE: if the process crashes or shutdown does not finish "+
			"within SHUTDOWN_TIMEOUT, undelivered events vanish without a trace (e.g. the order "+
			"confirmation notification is never sent)",
		"remedy", "EVENT_BUS=redis")
}

// shutdownContainer closes the services in the container and logs the errors.
func shutdownContainer(ctx context.Context, c *container.Container, cfg config.Config, log *slog.Logger) {
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
	defer cancel()

	if err := c.Shutdown(shutdownCtx); err != nil {
		log.Error("the container services could not be shut down", "error", err)
	}
}
