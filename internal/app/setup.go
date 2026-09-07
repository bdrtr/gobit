package app

import (
	"context"
	"io/fs"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errorreport"
	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/core/openapi"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/adminui"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/core/workflow"
	"github.com/bdrtr/gobit/internal/core/workflow/pgstore"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
	fulfillingwf "github.com/bdrtr/gobit/internal/workflows/fulfilling"
	invoicingwf "github.com/bdrtr/gobit/internal/workflows/invoicing"
	returnswf "github.com/bdrtr/gobit/internal/workflows/returns"
)

// codeFlowSetupFailed reports that the cross-module workflows could not be set
// up.
const codeFlowSetupFailed = "workflow_setup_failed"

// openAPIPath is the path the generated API schema is served from.
const openAPIPath = "/openapi.json"

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

	if err := c.Provide(invoicingwf.InteropName, invoicingwf.NewInterop(invoicingWorkflow)); err != nil {
		return err
	}

	// The fulfilling flow, on the same container and for the same reason. It is
	// what opens a SHIPMENT for an order and binds the two: the fulfillment
	// module never validates the reference it is handed and the order module
	// knows no parcels, so nothing could answer "which order is this parcel
	// for" until the assembling landed here (ADR 0001/0006).
	fulfillingWorkflow, err := fulfillingwf.FromContainer(c)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeFlowSetupFailed,
			"the fulfilling workflow could not be set up")
	}

	return c.Provide(fulfillingwf.InteropName, fulfillingwf.NewInterop(fulfillingWorkflow))
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
// Without the check three failures would stay SILENT: the description of a
// route whose path has changed drops out of the document, a route nobody
// described enters it bodiless, and two modules with an identically named DTO
// make the document impossible to build at all — all three would only be seen
// when somebody opened /openapi.json, and the second one not even then, because
// a bodiless endpoint looks like an endpoint.
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

	// The mirror direction, and it is the one an EMBEDDER needs. Every module in
	// the box describes itself; a module written outside the repository is the
	// one most likely not to, and before ADR 0035 it could not even if it wanted
	// to. Its endpoints enter the document bodiless — a valid model, but a silent
	// one, and this line is the only place that silence is broken.
	if bare := doc.UndescribedRoutes(); len(bare) > 0 {
		log.WarnContext(ctx, "openapi: there are routes no description matched",
			"routes", bare,
			"meaning", "these endpoints appear in the document with a path, a method and no body; "+
				"a module describes its own endpoints by implementing openapi.Describer")
	}

	if err != nil {
		log.ErrorContext(ctx, "the openapi schema could not be built; /openapi.json will return 500",
			"error", err)
	}
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
	// callbacks holds the inbound provider endpoints. It is a third deferred
	// binding: the plugins that own the callbacks are installed after the
	// router is built, so the registry is created here, filled during
	// Registry.Start and mounted before the plugin routes.
	callbacks *corehttp.CallbackRegistry
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
	opts Options,
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

	// The callback registry is the third object born before the router. Its
	// routes come from plugins that do not exist yet, so it guards nothing
	// until it is mounted — and refuses to recognize a path before then, which
	// is what makes a callback arriving mid-startup fail closed.
	guards, callbacks, err := guardStack(cfg, authn, panelRing, redisClient, pool, log)
	if err != nil {
		return nil, nil, err
	}

	// The registry enters the container under a PUBLISHED name so a plugin can
	// resolve it without re-spelling an unexported constant.
	if err := c.Provide(coreplugin.CallbacksName, callbacks); err != nil {
		return nil, nil, err
	}

	router := corehttp.NewRouter(corehttp.RouterOptions{
		Version:         opts.version(),
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
	registerModules(registry, cfg, log, opts.Modules)

	// The plugins are installed BEFORE the modules: a module brought in by a
	// plugin must be able to go through the Register/migration/route cycle too.
	pluginRegistry, host, err := installPlugins(ctx, cfg, c, registry, bus, log, opts.Plugins)
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
		callbacks: callbacks,
	}, closeApp, nil
}
