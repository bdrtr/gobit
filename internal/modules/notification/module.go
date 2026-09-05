// Package notification is the notification module (plan Section 5.6).
//
// Its responsibility in one sentence: when an event has to be told to the
// customer, having the SELECTED provider do it and writing the attempt into a
// permanent log. The module is the SOLE writer of notification_deliveries data
// (Principle 2.3).
//
// # The provider abstraction
//
// The side that talks to the e-mail/SMS service is not the module but a
// PROVIDER that satisfies the NotificationProvider contract in
// core/provider. The module holds the providers by their ids in a
// registry ([service.ProviderRegistry]) and resolves them BY NAME at send time.
// The only provider that comes out of the box is the "log" provider, which
// sends nowhere and says so in its name
// (internal/modules/notification/logonly); the plugin system can add its own
// provider to the registry in the container without touching the core or this
// module (coreplugin.Host.RegisterNotificationProvider).
//
// Which provider will be used is chosen by NOTIFICATION_PROVIDER. Whether the
// name is REALLY registered cannot be verified here — plugin providers are
// registered AFTER the modules come up — and the check is therefore at the
// composition root (internal/app): an unknown name STOPS the startup.
//
// # The "order.placed" subscriber
//
// The module's only write trigger is an event. The subscription is set up
// during Register and the handler is [service.Service.OrderPlaced]. The e-mail
// DOES NOT COME FROM THE EVENT: the event payload carries no personal data and
// the address is read from the order itself over the "order.interop" surface
// (see service/orders.go).
//
// # What it does not know
//
// The module imports no module and does not know what an order is;
// notification_deliveries.reference is free text, it IS NOT a foreign key
// (Principle 2.2). It does not know the TEXT of the template either: how the
// notification looks is decided by the provider.
//
// # The surfaces it exposes
//
//   - "notification.service" — the rich in-module surface (with domain types).
//   - "notification.providers" — the provider registry; plugins add their
//     provider here.
//   - GET /admin/v1/notifications — the read endpoint of the delivery log.
//
// A cross-module "interop" surface and a Query provider are DELIBERATELY
// ABSENT: there is no other module that reads the delivery log, and the log is
// not a joinable entity but a ledger. Opening the two already would have
// produced two contracts without a consumer — a field that enters a contract
// can never be taken out again.
package notification

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/notification/api"
	"github.com/bdrtr/gobit/internal/modules/notification/logonly"
	"github.com/bdrtr/gobit/internal/modules/notification/repository"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

// ModuleName is the name of the module; it is the prefix of the container names
// and of the migration version ledger.
const ModuleName = "notification"

// ServiceName is the name of the module's service in the container.
const ServiceName = ModuleName + ".service"

// ProvidersName is the name of the provider registry in the container.
//
// The plugin system resolves this registry and adds its own NotificationProvider
// to it; it does not need to change the module's code. The value has to be the
// SAME as coreplugin.NotificationProvidersName and the agreement is pinned down
// by an internal/arch test.
const ProvidersName = ModuleName + ".providers"

// DefaultProviderID is the id used when no provider is selected.
//
// The value comes from the logonly package: if the config's default ("log") and
// the provider's id drifted apart, the installation would come up with a
// notification path that finds no provider at all.
const DefaultProviderID = logonly.ID

// The names of the core services resolved from the container.
const (
	svcDB       = "core.db"
	svcEventBus = "core.eventbus"
)

// Error codes.
const (
	codeSetupFailed      = "notification_module_setup_failed"
	codeProviderRegister = "notification_module_provider_register_failed"
	codeSubscribeFailed  = "notification_module_subscribe_failed"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot is the embedded files with the "migrations/" prefix stripped:
// db.Migrate reads the source from the root.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// Options are the module's setup settings.
type Options struct {
	// ProviderID is the id of the provider to be used when sending
	// (NOTIFICATION_PROVIDER). When it is given empty, [DefaultProviderID]
	// applies.
	ProviderID string
	// Logger falls back to slog.Default when it is given as nil.
	Logger *slog.Logger
}

// Module is the implementation the notification module offers to the core.
type Module struct {
	opts      Options
	svc       *service.Service
	providers *service.ProviderRegistry
	handler   *api.Handler
}

// That the core's contract is satisfied is pinned down at compile time.
var _ module.Module = (*Module)(nil)

// That it can describe the document is pinned down at compile time as well.
//
// [openapi.Describer] is an OPTIONAL interface and the composition root looks
// for it with a TYPE ASSERTION; if the method name or its signature slips,
// nothing breaks at compile time — only this module's endpoint would silently
// drop out of the document.
var _ openapi.Describer = (*Module)(nil)

// New produces a notification module that is ready to be registered.
//
// The dependencies are resolved not here but during Register: until that moment
// the container may not have set up the core services yet.
func New(opts Options) *Module {
	if opts.ProviderID == "" {
		opts.ProviderID = DefaultProviderID
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Module{opts: opts}
}

// Name returns the module's unique name.
func (m *Module) Name() string { return ModuleName }

// Migrations returns the module's migration files.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Register registers the service and the provider registry into the container
// and sets up the "order.placed" subscription.
//
// Only the CORE services are resolved; other modules' services may not be
// registered yet at this stage (see the module.Module documentation). Because
// core.db and core.eventbus are registered in main.go as ready values before
// the modules come up, resolving them here is safe and their absence is a setup
// error with which the module could not work at all — it is not deferred
// silently.
//
// THE ORDER SURFACE is not resolved here: "order.interop" may not be in the
// container at this moment, and trying to resolve it would bring the startup
// down with an error where nothing is really missing. The resolution is
// deferred to the first event (see [service.NewOrderContacts]).
//
// The default provider ([logonly.Provider]) is registered here too; even when
// it is not the selected provider it stays registered, because the registry is
// a list, not a choice.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, svcDB)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the database pool (%q)", ModuleName, svcDB)
	}
	// It is resolved through a narrow interface: the module only SUBSCRIBES, it
	// does not publish and does not close the bus (see service.EventSubscriber).
	bus, err := container.Resolve[service.EventSubscriber](c, svcEventBus)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the event bus (%q)", ModuleName, svcEventBus)
	}

	log := m.opts.Logger.With("module", ModuleName)

	providers := service.NewProviderRegistry()
	if err := providers.Register(logonly.New(log)); err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeProviderRegister,
			"the %s module could not register the default provider", ModuleName)
	}

	svc, err := service.New(service.Options{
		Store:      repository.New(pool.Pool()),
		Providers:  providers,
		ProviderID: m.opts.ProviderID,
		Contacts:   service.NewOrderContacts(c),
		Logger:     log,
	})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s service could not be set up", ModuleName)
	}

	if err := c.Provide(ServiceName, svc); err != nil {
		return err
	}
	if err := c.Provide(ProvidersName, providers); err != nil {
		return err
	}

	// The subscription is set up in Register (the place the module.Module
	// contract prescribes). Failing to set it up STOPS THE STARTUP: a
	// notification module that receives no event silently sends no e-mail at
	// all, and that is only noticed while customers are waiting for their
	// confirmation.
	if err := bus.Subscribe(service.EventOrderPlaced, svc.OrderPlaced); err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSubscribeFailed,
			"the %s module could not subscribe to the %q event", ModuleName, service.EventOrderPlaced)
	}

	m.svc = svc
	m.providers = providers
	m.handler = api.New(svc)

	log.DebugContext(ctx, "notification module registered",
		"service", ServiceName,
		"providers", providers.IDs(),
		"selected_provider", m.opts.ProviderID,
		"event", service.EventOrderPlaced,
	)
	return nil
}

// Routes mounts the module's admin endpoint on the router.
//
// If Register did not run, no endpoint is mounted: rather than a handler
// without a service panicking on the first request, it is better for the
// endpoint not to exist at all.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
		slog.Default().Warn("Routes was called on the notification module without Register, no route was mounted")
		return
	}
	m.handler.Routes(r)
}

// Describe writes the module's admin endpoint into the OpenAPI document.
//
// Unlike [Module.Routes] there is NO Register check and none is needed: the
// schema comes from the types, not from the service.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// Service returns the module's service; it is nil if Register was not called.
//
// It is meant for tests and for embedded use; in the normal flow the service is
// resolved from the container under the name [ServiceName].
func (m *Module) Service() *service.Service { return m.svc }

// Providers returns the module's provider registry; it is nil if Register was
// not called.
//
// The embedding application can add its own provider here; in the normal flow
// the registry is resolved from the container under the name [ProvidersName].
func (m *Module) Providers() *service.ProviderRegistry { return m.providers }

// mustSub opens the subdirectory; it panics if it cannot be opened.
//
// The panic is safe here: the directory name is fixed at compile time and
// The go:embed directive has already verified at compile time that the files exist. Returning
// nil silently would nevertheless have meant the module coming up without
// migrations (that is, without tables); a setup error has to blow up openly.
func mustSub(files embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(files, dir)
	if err != nil {
		panic("notification: the embedded migration directory could not be opened: " + err.Error())
	}
	return sub
}
