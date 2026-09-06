// Package plugin defines the plugins that add capabilities without touching the
// core.
//
// A plugin can register a module, routes, event subscribers, scheduled jobs
// ([Host.RegisterJob]) and providers (payment, fulfillment, notification,
// file). While doing so it imports NO
// commerce module: it reaches the registration points from the container BY
// NAME and takes the contracts from the core's
// [github.com/bdrtr/gobit/core/provider] package (ADR 0001).
//
// # Why a compile-time plugin
//
// Go's standard [plugin] package (.so loading) is DELIBERATELY not used here.
// The reasons: it works only on Linux and macOS, does not support cross
// compilation, requires ALL dependencies of the plugin and the main binary to
// be built at bit-identical versions, and the loaded code can never be
// unloaded. Those constraints turn the "plug the plugin in at runtime" promise
// into "rebuild the whole application for every plugin" in practice — that is,
// into what compile-time registration already gives, with added fragility on
// top.
//
// Instead a plugin is an ordinary Go package; the application imports it and
// adds it to the [Registry]. The "without touching the core" criterion is met
// like this: adding a plugin adds one line to the setup file, and the code of
// the core or of any module DOES NOT CHANGE.
//
// # Two phases
//
// Plugins are installed with [Registry.Install] and started with
// [Registry.Start]. The modules come up in between. The split is mandatory: a
// plugin wants to add a provider to the "payment.providers" registry, but that
// registry is NOT in the container until the payment module has registered.
// Provider and subscriber registrations made during Install are therefore not
// applied immediately; they are QUEUED and processed at Start.
package plugin

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/module"
	coreprovider "github.com/bdrtr/gobit/core/provider"
)

// PaymentProvidersName is the container name of the payment provider registry.
//
// It carries the same value as the payment module's ProvidersName constant but
// does NOT import that package: the core may not import the modules (Principle
// 2.4). The value is a contract; that the two agree is protected by
// [TestTheProviderRegistryNamesAgree].
const PaymentProvidersName = "payment.providers"

// FulfillmentProvidersName is the container name of the shipping provider
// registry.
const FulfillmentProvidersName = "fulfillment.providers"

// NotificationProvidersName is the container name of the notification provider
// registry.
const NotificationProvidersName = "notification.providers"

// CallbacksName is the container name of the inbound-callback registry.
//
// It is PUBLISHED, unlike the infrastructure names ("core.db" and the rest),
// which are unexported constants in the composition root that a plugin has to
// re-spell as a literal. A rename there breaks every plugin silently; a rename
// here does not compile.
const CallbacksName = "core.callbacks"

// ErrorReporterName is the container name of the error reporter.
//
// It is SINGULAR where the four above are plural, and the difference is the
// whole shape of the thing: a payment provider is chosen per order out of a
// registry, while there is at most ONE reporter and nothing chooses it. The
// container refuses a duplicate name, so "at most one" is enforced by the
// registration itself rather than by a rule somebody has to remember.
const ErrorReporterName = "error.reporter"

// FileProvidersName is the container name of the file provider registry.
//
// Unlike the other three there is NOT YET a module satisfying this name: the
// contract ([github.com/bdrtr/gobit/core/provider.FileProvider]) and
// the registration point were written before the module that will consume
// them. The name is therefore ONE-SIDED for now and
// [TestTheProviderRegistryNamesAgree] can carry no assertion for it; a line must
// be added there when the file module arrives.
const FileProvidersName = "file.providers"

// The error codes.
const (
	codeNameEmpty       = "plugin_name_empty"
	codeNameDuplicate   = "plugin_name_duplicate"
	codeSetupFailed     = "plugin_setup_failed"
	codeStartFailed     = "plugin_start_failed"
	codeSinkMissing     = "plugin_provider_sink_missing"
	codeSinkUnusable    = "plugin_provider_sink_unusable"
	codeSubscribeFailed = "plugin_subscribe_failed"
	codeRouteConflict   = "plugin_route_conflict"
	codeRouteInvalid    = "plugin_route_invalid"
)

// Plugin is a plugin adding a capability to the core.
type Plugin interface {
	// Name is the plugin's unique name (e.g. "payment-stripe").
	// It is used in logs and error messages.
	Name() string

	// Setup declares the plugin's registrations through the [Host].
	//
	// CAUTION: at this stage the modules are NOT up yet. Do not try to resolve
	// a module service from the container; the [Host]'s registration methods
	// already queue the call and apply it at the right moment.
	Setup(ctx context.Context, h *Host) error
}

// paymentSink is the narrow surface of the payment provider registry this
// package needs (a consumer-side interface, ADR 0001).
type paymentSink interface {
	Register(p coreprovider.PaymentProvider) error
}

// fulfillmentSink is the narrow surface of the shipping provider registry.
type fulfillmentSink interface {
	Register(p coreprovider.FulfillmentProvider) error
}

// notificationSink is the narrow surface of the notification provider
// registry.
type notificationSink interface {
	Register(p coreprovider.NotificationProvider) error
}

// callbackSink is the narrow surface of the inbound-callback registry.
type callbackSink interface {
	Register(rt corehttp.CallbackRoute) error
}

// fileSink is the narrow surface of the file provider registry.
type fileSink interface {
	Register(p coreprovider.FileProvider) error
}

// routeRegistration is the route function a plugin wants to bind.
//
// The plugin's name travels alongside the function: if the conflict error
// cannot answer "which plugin" as well as "which path", whoever has to fix the
// installation must try every plugin one by one.
type routeRegistration struct {
	// plugin is the name of the plugin making the registration.
	plugin string
	// bind registers the routes on the given router.
	bind func(r chi.Router)
}

// Job is scheduled work a plugin brings.
//
// # Why the scheduler's own type is not repeated from it
//
// The runner, its store, its advisory-lock arithmetic and its occurrence
// election live in internal/core/job, and a published package may not import an
// internal one. That line is held for the downstream author rather than for
// tidiness: a plugin outside this module can CALL a function whose signature
// names an internal type, but it cannot declare a value of that type — so the
// wall lands in the customer's project with no explanation on this side.
//
// ADR 0019 deferred this surface with a condition rather than a refusal ("it
// arrives with the first plugin that brings a job"), and ADR 0026 anticipated
// the price as publishing the whole job package. Measured, that price buys the
// wrong thing: a plugin needs FOUR values, while publishing the package would
// freeze the runner, the store contract, the lock class and the key algorithm
// into the compatibility promise — publishing the machine in order to hand out
// a form. ADR 0026's own bias is "publish late, and publish what was measured";
// four fields were measured, the scheduler was not.
//
// What the copy costs is drift, and the cost is PAID rather than hoped away:
// internal/app/jobs_test.go reflects over the scheduler's own definition and
// fails when it grows a field this struct does not carry.
type Job struct {
	// Name identifies the job. It is the advisory lock's input, the primary key
	// of the job's history and what `gobit jobs` prints, so it is a CONTRACT:
	// changing it starts a new job with no history and lets the old one run
	// again.
	//
	// It shares ONE namespace with the jobs the core registers and with every
	// other plugin's, and a clash is refused at startup rather than resolved —
	// two jobs under one name would share an advisory lock and a history row,
	// so one of them would silently never run. Prefix it with the plugin's own
	// name.
	Name string

	// Every is the interval between occurrences.
	//
	// It is an interval and not a calendar expression, and that is the
	// scheduler's decision rather than a simplification: a cron expression
	// carries a time zone, and a time zone carries an hour that happens twice
	// and an hour that does not happen at all.
	Every time.Duration

	// MaxRun bounds one run; the context handed to Run carries it as a
	// deadline.
	//
	// It MUST NOT exceed Every. A run that outlasts its own interval is due
	// again before it finished, so it can never catch up — and it is REFUSED at
	// startup rather than left running behind forever.
	MaxRun time.Duration

	// Run is the work.
	//
	// It MUST be safe to run twice: election makes a second concurrent run
	// unlikely, not impossible — a process can be partitioned from the database
	// after taking the lock, and the row that elects an occurrence is written
	// before the work rather than after.
	//
	// Whether it may WRITE is the one question no gate here answers for the
	// author. ADR 0017 refuses running side effects unwatched, and every job the
	// core registers only reads and reports; a plugin job that acts is the
	// plugin author's decision to defend.
	Run func(ctx context.Context) error

	// plugin is the name of the plugin that registered the job, captured by
	// [Host.RegisterJob].
	//
	// It is unexported so it cannot be set from outside, and it exists for the
	// same reason [routeRegistration] carries one: the error that refuses a bad
	// definition at startup is useless if it cannot say which plugin to go and
	// fix.
	plugin string
}

// PluginName reports the plugin that registered the job.
//
// It is empty for a Job that never went through [Host.RegisterJob].
func (j Job) PluginName() string { return j.plugin }

// queuedTask represents a registration to be applied during the Start phase.
type queuedTask struct {
	// description says which task failed, in the error message.
	description string
	// apply performs the task.
	apply func(ctx context.Context, h *Host) error
}

// Host is the surface a plugin registers with the core through.
//
// It is NOT safe for concurrent use: plugins are installed in sequence.
type Host struct {
	// c is the container; plugins may put their own services in it.
	c *container.Container
	// modules is the module registry; a plugin's module is added here.
	modules *module.Registry
	// bus is the event bus; it may be nil.
	bus eventbus.EventBus
	// log is the logger tagged with the plugin's name.
	log *slog.Logger
	// settings is the plugin configuration (it comes from the environment
	// variables).
	settings map[string]string

	// active is the name of the plugin whose Setup is running; it is for the
	// error messages.
	active string
	// routes are the route registrations the plugins want to bind.
	routes []routeRegistration
	// jobs are the scheduled jobs the plugins declared.
	jobs []Job
	// queue holds the tasks to be applied at Start.
	queue []queuedTask
}

// NewHost builds the host the plugins will use.
//
// settings may be nil; [Host.Setting] then returns false for every key.
func NewHost(
	c *container.Container,
	modules *module.Registry,
	bus eventbus.EventBus,
	log *slog.Logger,
	settings map[string]string,
) *Host {
	if log == nil {
		log = slog.Default()
	}

	return &Host{c: c, modules: modules, bus: bus, log: log, settings: settings}
}

// Container returns the core container.
//
// Do NOT resolve a module service from it during Setup: the modules are not
// registered yet. Registering your own service (Provide) is safe.
func (h *Host) Container() *container.Container { return h.c }

// Logger returns the logger tagged with the plugin's name.
func (h *Host) Logger() *slog.Logger { return h.log.With("plugin", h.active) }

// Setting reads a value from the plugin configuration.
//
// When the key is absent or the value is empty the second result is false.
// Counting an empty string as "not given" is deliberate: in environment-variable
// configuration a defined but empty variable is almost always a configuration
// error, and that is better than silently starting with an empty API key.
func (h *Host) Setting(key string) (string, bool) {
	v, ok := h.settings[key]
	if !ok {
		return "", false
	}

	v = strings.TrimSpace(v)

	return v, v != ""
}

// AddModule adds the module a plugin brings to the registry.
//
// The module goes through the same lifecycle as the core modules: Register,
// migration and route binding.
func (h *Host) AddModule(m module.Module) {
	if m == nil || h.modules == nil {
		return
	}

	h.modules.Add(m)
}

// AddRoutes registers the function that will bind the plugin's routes.
//
// The function is run AFTER the modules' routes have been bound.
//
// CAUTION: the function is called TWICE. [Registry.MountRoutes] first runs it
// on an empty probe router to learn which patterns it wants, and, when there is
// no conflict, runs it again on the real router. The function must therefore
// only register routes and have no other side effect (incrementing a counter,
// opening a connection).
func (h *Host) AddRoutes(fn func(r chi.Router)) {
	if fn == nil {
		return
	}

	h.routes = append(h.routes, routeRegistration{plugin: h.active, bind: fn})
}

// RegisterJob declares scheduled work the plugin brings.
//
// # Why it is COLLECTED and not queued
//
// The provider registrations wait for [Registry.Start] because they need a
// module's registry to be in the container. A job waits for something that is
// not there at Start either: the job registry is built by the composition root
// after the modules are up, immediately before the runner is started. So this
// follows [Host.AddRoutes]'s shape rather than
// [Host.RegisterPaymentProvider]'s — collected during Setup, drained by the
// composition root at the moment the thing that can hold it exists.
//
// The consequence is worth stating because it is the useful half: the jobs are
// complete as soon as Setup has run, so `gobit jobs` lists a plugin's jobs even
// though that command never reaches the Start phase.
//
// # Nothing is validated here, and that is deliberate
//
// A job with no name, a non-positive interval, or a MaxRun longer than its
// interval IS refused — by the scheduler, at startup, under the same rule that
// refuses one of the core's own. Repeating the rule here would put "what a
// valid schedule is" in two places, and the copy that drifts is always the one
// nobody runs.
//
// A nil Run is carried through for the same reason, and it is the one place
// this method deliberately differs from every Register method above it. Those
// drop a nil provider, because a nil provider cannot be registered at all. An
// incomplete job CAN be: dropping it here would produce an installation that
// believes it has a retry pass, has none, and shows nothing missing in
// `gobit jobs` — the absence reads as "there was nothing to report". So it is
// passed on, and refused loudly, at boot.
func (h *Host) RegisterJob(j Job) {
	j.plugin = h.active

	h.jobs = append(h.jobs, j)
}

// Jobs returns the jobs the plugins declared, in registration order.
//
// It is the composition root's read and there is no consumer for it inside this
// package, because there cannot be one: the scheduler that runs these is
// internal, which is the whole reason [Job] exists as a separate type.
//
// The slice is a copy. The host hands the same value to a caller that will
// range over it while startup is still going on, and a caller that appended to
// the returned slice would either mutate the host's own or not, depending on
// capacity — the least debuggable of all possible outcomes.
func (h *Host) Jobs() []Job { return slices.Clone(h.jobs) }

// RegisterPaymentProvider adds a payment provider to the payment module.
//
// The registration happens not immediately but during [Registry.Start]: at
// Setup time the payment module may not be up yet.
//
// When the payment module is not registered at all, Start returns an ERROR.
// Ignoring it silently would mean an installation believed to have "the stripe
// plugin installed" taking no payment at all.
func (h *Host) RegisterPaymentProvider(p coreprovider.PaymentProvider) {
	if p == nil {
		return
	}

	name := h.active
	h.queue = append(h.queue, queuedTask{
		description: "the payment provider of the " + name + " plugin (" + p.ID() + ")",
		apply: func(_ context.Context, host *Host) error {
			sink, err := resolveSink[paymentSink](host, PaymentProvidersName, "payment")
			if err != nil {
				return err
			}

			return sink.Register(p)
		},
	})
}

// RegisterCallback binds a guarded inbound endpoint for this plugin.
//
// # Why a plugin cannot just bind the route itself
//
// It can — [Host.AddRoutes] and a module's own Routes both do — and that is the
// problem this method exists for. A callback bound that way lands on the root
// router outside every guard: no quota, no body limit, no replay window, and no
// enforced signature check. It is indistinguishable from a guarded one by
// reading the code, which is how the measured example stayed unguarded.
//
// Going through here, the guards are not optional: the registry refuses a route
// with no verifier at STARTUP, and it is the registry — not the plugin — that
// binds the path, so a plugin that forgot to register simply has no endpoint
// rather than an unguarded one.
//
// Like the provider registrations this is applied during [Registry.Start], and
// for the same reason: at Setup time the registry is not necessarily in the
// container yet. When it is absent, Start returns an ERROR — an installation
// believed to be taking provider callbacks and silently taking none is the
// failure this whole surface is about.
func (h *Host) RegisterCallback(rt corehttp.CallbackRoute) {
	name := h.active
	h.queue = append(h.queue, queuedTask{
		description: "the callback of the " + name + " plugin (" + rt.Path + ")",
		apply: func(_ context.Context, host *Host) error {
			sink, err := resolveSink[callbackSink](host, CallbacksName, "callback")
			if err != nil {
				return err
			}

			return sink.Register(rt)
		},
	})
}

// RegisterErrorReporter installs the plugin's error reporter.
//
// # Why this one is not queued
//
// The other four registrations wait for [Registry.Start] because they need a
// module's registry to exist. This one needs nothing: the reporter goes into
// the container under a name the CORE owns, and the core is already there.
//
// Waiting would also cost the reports worth the most. The modules come up
// between Install and Start — migrations, schema checks, provider verification
// — and a reporter bound at Start would watch every one of those failures go by
// unreported, in the one phase where a failure means the process is about to
// exit.
//
// A second reporter is a CONFLICT, not a replacement. The container refuses the
// duplicate name and the error surfaces at Start naming the plugin that lost:
// two plugins each believing they own the reporting is a misconfiguration, and
// letting the last one win would send the failures somewhere the operator who
// installed the first one is not looking.
func (h *Host) RegisterErrorReporter(r coreprovider.ErrorReporter) {
	if r == nil {
		return
	}

	name := h.active
	err := h.c.Provide(ErrorReporterName, r)
	if err == nil {
		return
	}

	// The registration already happened or already failed; the queue is used
	// only to carry the failure to a place that can return it, since a Register
	// method has no error to give back.
	h.queue = append(h.queue, queuedTask{
		description: "the error reporter of the " + name + " plugin (" + r.ID() + ")",
		apply: func(context.Context, *Host) error {
			return coreerrors.Wrap(err, coreerrors.KindConflict, codeSinkUnusable,
				"the %s plugin could not register its error reporter under %q",
				name, ErrorReporterName)
		},
	})
}

// RegisterFulfillmentProvider adds a shipping provider to the fulfillment
// module.
func (h *Host) RegisterFulfillmentProvider(p coreprovider.FulfillmentProvider) {
	if p == nil {
		return
	}

	name := h.active
	h.queue = append(h.queue, queuedTask{
		description: "the shipping provider of the " + name + " plugin (" + p.ID() + ")",
		apply: func(_ context.Context, host *Host) error {
			sink, err := resolveSink[fulfillmentSink](host, FulfillmentProvidersName, "fulfillment")
			if err != nil {
				return err
			}

			return sink.Register(p)
		},
	})
}

// RegisterNotificationProvider adds a notification provider to the
// notification module.
//
// The ordering rule is [Host.RegisterPaymentProvider]'s and is even more
// binding here: the notification module calls its provider from the
// "order.placed" subscriber, and that subscription is set up in the same Start
// round. Had the registration been attempted at Setup, the plugin's provider
// would arrive before the registry was opened and the setup would blow up.
//
// When the notification module is not registered at all, Start returns an
// ERROR; ignoring it silently would mean an installation that believes it sends
// order emails reaching no customer at all.
func (h *Host) RegisterNotificationProvider(p coreprovider.NotificationProvider) {
	if p == nil {
		return
	}

	name := h.active
	h.queue = append(h.queue, queuedTask{
		description: "the notification provider of the " + name + " plugin (" + p.ID() + ")",
		apply: func(_ context.Context, host *Host) error {
			sink, err := resolveSink[notificationSink](host, NotificationProvidersName, "notification")
			if err != nil {
				return err
			}

			return sink.Register(p)
		},
	})
}

// RegisterFileProvider adds a file provider to the file module.
//
// The ordering rule is [Host.RegisterPaymentProvider]'s: the registration is
// applied during [Registry.Start], not at Setup.
//
// When the file module is not registered at all, Start returns an ERROR. The
// silent failure here would be LONGER-LIVED than the others: an installation
// believed to have "the S3 plugin installed" keeps writing uploads to the
// container's local disk, everything looks like it works, and the loss only
// appears when the container restarts — at which point the files are gone while
// the database still holds addresses that lead nowhere. A payment failure is
// seen at the first customer attempt; this one stays invisible until the first
// restart.
func (h *Host) RegisterFileProvider(p coreprovider.FileProvider) {
	if p == nil {
		return
	}

	name := h.active
	h.queue = append(h.queue, queuedTask{
		description: "the file provider of the " + name + " plugin (" + p.ID() + ")",
		apply: func(_ context.Context, host *Host) error {
			sink, err := resolveSink[fileSink](host, FileProvidersName, "file")
			if err != nil {
				return err
			}

			return sink.Register(p)
		},
	})
}

// Subscribe subscribes the plugin to an event.
//
// The subscription is set up during [Registry.Start].
func (h *Host) Subscribe(eventName string, fn eventbus.Handler) {
	if fn == nil {
		return
	}

	name := h.active
	h.queue = append(h.queue, queuedTask{
		description: "the " + eventName + " subscription of the " + name + " plugin",
		apply: func(_ context.Context, host *Host) error {
			if host.bus == nil {
				return coreerrors.Invalid(codeSubscribeFailed,
					"there is no event bus, the %s subscription cannot be set up", eventName)
			}

			return host.bus.Subscribe(eventName, fn)
		},
	})
}

// resolveSink resolves the provider registry from the container through the
// wanted narrow surface.
//
// It is a separate generic function because methods cannot take type
// parameters.
func resolveSink[T any](h *Host, name, moduleName string) (T, error) {
	var zero T

	if h.c == nil || !h.c.Has(name) {
		return zero, coreerrors.Invalid(codeSinkMissing,
			"the %s module is not registered; %q was not found in the container", moduleName, name)
	}

	v, err := container.Resolve[T](h.c, name)
	if err != nil {
		return zero, coreerrors.Wrap(err, coreerrors.KindInternal, codeSinkUnusable,
			"the provider registry %q does not satisfy the expected surface", name)
	}

	return v, nil
}

// Registry holds the installed plugins and runs them in two phases.
type Registry struct {
	// log writes the registration events.
	log *slog.Logger
	// plugins are the installed plugins; the order is preserved.
	plugins []Plugin
}

// NewRegistry builds an empty plugin registry.
func NewRegistry(log *slog.Logger) *Registry {
	if log == nil {
		log = slog.Default()
	}

	return &Registry{log: log}
}

// Add adds a plugin to the registry. The installation order is the order of
// addition.
func (r *Registry) Add(p Plugin) {
	if p == nil {
		return
	}

	r.plugins = append(r.plugins, p)
}

// Plugins returns the names of the installed plugins.
func (r *Registry) Plugins() []string {
	names := make([]string, 0, len(r.plugins))
	for _, p := range r.plugins {
		names = append(names, p.Name())
	}

	return names
}

// Install runs every plugin's Setup.
//
// It must be called BEFORE THE MODULES COME UP: whether a module added by a
// plugin can go through the Register/migration/route cycle depends on it.
func (r *Registry) Install(ctx context.Context, h *Host) error {
	if err := r.validateNames(); err != nil {
		return err
	}

	for _, p := range r.plugins {
		h.active = p.Name()
		if err := p.Setup(ctx, h); err != nil {
			h.active = ""

			return coreerrors.Wrap(err, coreerrors.KindOf(err), codeSetupFailed,
				"the %s plugin could not be installed", p.Name())
		}

		r.log.DebugContext(ctx, "plugin installed", "plugin", p.Name())
	}

	h.active = ""

	r.log.InfoContext(ctx, "the plugins are installed", "count", len(r.plugins))

	return nil
}

// Start applies the queued provider and subscriber registrations.
//
// It must be called AFTER THE MODULES ARE UP.
func (r *Registry) Start(ctx context.Context, h *Host) error {
	for _, task := range h.queue {
		if err := task.apply(ctx, h); err != nil {
			return coreerrors.Wrap(err, coreerrors.KindOf(err), codeStartFailed,
				"%s could not be registered", task.description)
		}

		r.log.DebugContext(ctx, "plugin registration applied", "task", task.description)
	}

	h.queue = nil

	return nil
}

// MountRoutes binds the plugins' routes to the router.
//
// It must be called AFTER the module routes: a plugin shadowing a module's path
// can only be caught here, where the existing tree can be read.
//
// # Why a conflict check
//
// Saying "call it afterwards" is NOT protection. In chi, registering the same
// pattern a second time overwrites the handler SILENTLY (and mounting onto an
// existing path panics): when a plugin registers "/store/v1/products" the
// storefront's product list falls to the plugin's handler, and that is only
// noticed when a customer sees an empty list. Every plugin route is therefore
// first registered on an empty probe router and the patterns it wants are read
// with [chi.Walk]; when a pattern already exists a typed conflict error is
// returned WITHOUT the REAL router being touched at all.
//
// It stops at the first conflict and the plugins after that one are not bound
// either: a partially bound admin surface is harder to diagnose than a server
// that never opened. Even if the caller swallows the error the module route is
// preserved, because the conflicting registration was never applied.
func (r *Registry) MountRoutes(router chi.Router, h *Host) error {
	if router == nil {
		return nil
	}

	existing, err := collectPatterns(router)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindInternal, codeRouteInvalid,
			"the existing route tree could not be read")
	}

	for _, registration := range h.routes {
		wanted, err := registration.wantedPatterns()
		if err != nil {
			return err
		}

		for _, pattern := range wanted {
			if _, conflicts := existing[pattern]; !conflicts {
				continue
			}

			// The failure must stay visible even if the caller swallows the
			// error: when the server comes up without the plugin routes bound,
			// this is the only clue.
			r.log.Error("plugin route conflict", "plugin", registration.plugin, "route", pattern)

			return coreerrors.Conflict(codeRouteConflict,
				"the %s plugin tried to bind a path that is already registered: %s",
				registration.plugin, pattern)
		}

		if err := registration.run(router); err != nil {
			return err
		}

		for _, pattern := range wanted {
			existing[pattern] = struct{}{}
		}
	}

	return nil
}

// wantedPatterns collects the patterns the route function wants to bind.
//
// The function is run on an empty probe router; the real router is NOT TOUCHED
// at this stage, because the point is precisely to decide whether the
// registration happens at all. The probe router is chi's own tree: parsing the
// patterns by hand would diverge from the paths chi actually produces once
// Route/Mount/Group are nested.
func (k routeRegistration) wantedPatterns() ([]string, error) {
	probe := chi.NewRouter()
	if err := k.run(probe); err != nil {
		return nil, err
	}

	set, err := collectPatterns(probe)
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInternal, codeRouteInvalid,
			"the routes of the %s plugin could not be read", k.plugin)
	}

	patterns := make([]string, 0, len(set))
	for pattern := range set {
		patterns = append(patterns, pattern)
	}

	// Map order is random; without sorting, the same conflict could blame a
	// different path on every run.
	sort.Strings(patterns)

	return patterns, nil
}

// run executes the route function on the given router.
//
// chi PANICS on an invalid pattern (a path not starting with "/"), on
// middleware added after the routes, and on an attempt to Mount onto an
// existing path. Had the panic been left as it is, only chi's internal stack
// trace would show at startup and it would not say which plugin was to blame;
// here it is turned into a typed error.
func (k routeRegistration) run(r chi.Router) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = coreerrors.Invalid(codeRouteInvalid,
				"the route registration of the %s plugin was rejected by chi: %v", k.plugin, p)
		}
	}()

	k.bind(r)

	return nil
}

// collectPatterns collects the "METHOD pattern" keys in the router tree.
func collectPatterns(r chi.Routes) (map[string]struct{}, error) {
	set := map[string]struct{}{}

	err := chi.Walk(r, func(
		method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler,
	) error {
		set[strings.ToUpper(method)+" "+normalizePattern(route)] = struct{}{}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return set, nil
}

// normalizePattern makes the route string chi returns comparable.
//
// Mounted sub-routers leave residue such as "/store/v1/products/*/", and chi
// serves a mounted path both as "/x" and as "/x/". Without cleaning the
// residue, a plugin's "/store/v1/products" registration would NOT MATCH the
// module's identical path and the conflict would go unnoticed.
func normalizePattern(route string) string {
	pattern := strings.ReplaceAll(route, "/*/", "/")
	pattern = strings.TrimSuffix(pattern, "/*")

	if len(pattern) > 1 {
		pattern = strings.TrimSuffix(pattern, "/")
	}

	if pattern == "" {
		return "/"
	}

	return pattern
}

// validateNames checks that no plugin name is empty or repeated.
//
// A repeated name makes it impossible to follow from the log which plugin
// registered which provider; it is also the most likely symptom of a
// configuration error where the same plugin was installed twice.
func (r *Registry) validateNames() error {
	seen := make(map[string]struct{}, len(r.plugins))

	for _, p := range r.plugins {
		name := p.Name()
		if strings.TrimSpace(name) == "" {
			return coreerrors.Invalid(codeNameEmpty, "a plugin name cannot be empty")
		}

		if _, dup := seen[name]; dup {
			return coreerrors.Conflict(codeNameDuplicate, "the plugin name is repeated: %s", name)
		}

		seen[name] = struct{}{}
	}

	return nil
}
