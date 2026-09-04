// Package fulfillment is the shipping/delivery module (plan Section 6, Phase
// 7).
//
// Its responsibility in one sentence: to know how far an order has come
// PHYSICALLY — which shipping option costs how much, whether a shipment was
// opened, whether it left, whether it was delivered — and to rank WHICH
// warehouse an order line will ship from. The module is the SOLE writer of
// ShippingProfile, ShippingOption, ShippingOptionRule, Fulfillment,
// FulfillmentItem and ShippingLocation (warehouse shipping policy with its
// region bindings) data (Principle 2.3).
//
// # Provider abstraction
//
// The side that talks to the carrier is not the module but a PROVIDER that
// satisfies the FulfillmentProvider contract in internal/core/provider. The
// module keeps providers in a registry by their ids
// ([service.ProviderRegistry]) and resolves them BY NAME during the flow. The
// only provider that comes in the box is the manual/test provider
// (internal/modules/fulfillment/manual); the plugin system of Phase 9 can add
// its own provider to the registry in the container without touching the core
// or this module.
//
// # Saga compensation
//
// An order flow undoes the shipping step with
// [service.Service.CancelFulfillment], and that method is IDEMPOTENT: called
// twice, the second call does not return an error. That the compensation can be
// run again is not a preference but the saga's working condition (plan Section
// 5.5). The single exception is a DELIVERED shipment: delivery is a physical
// fact that cannot be undone, and canceling it returns errors.Conflict — the
// same rule as a captured session in the payment module, which cannot be
// canceled and is refunded instead.
//
// # What it does not know
//
// The module imports no module and does not know WHICH order a shipment belongs
// to. reference is free text, NOT a foreign key (Principle 2.2), and its
// existence is not verified here; the same holds for a shipping option's
// region_id, a fulfillment item's line_item_id, and a warehouse shipping
// policy's location_id and region_id. The binding is made with the link the
// order declares. This is why this module declares NO link definition: the
// owner of the binding is not shipping, but the side that needs shipping.
//
// The warehouses THEMSELVES are not this module's data either: their names,
// addresses and stock live in the inventory module. The policy row here carries
// only that warehouse's shipping quality, and it can be written even for a
// warehouse that does NOT exist — there is nobody to verify it. Such a row can
// never be selected, because selection only filters and ranks the candidates
// the inventory module produces.
//
// For the same reason the module does not know which shipping profiles the
// cart's products are bound to either; the profile ids are given to the
// eligibility query FROM OUTSIDE.
//
// # The surfaces it exposes
//
//   - "fulfillment.service" — the rich in-module surface (with domain types).
//   - "fulfillment.interop" — the PRIMITIVE cross-module surface (ADR
//     0001/0006); order flows drive the shipping steps through it.
//   - "fulfillment.providers" — the provider registry; plugins add their
//     providers here.
//   - "shipping_option.query" — the read provider opened to the Query layer
//     (ADR 0004).
//   - /admin/v1/shipping-profiles, /admin/v1/shipping-options,
//     /admin/v1/shipping-locations, /admin/v1/fulfillments … — the admin API.
//     The warehouse policy endpoint is the module's only write surface that can
//     stop the ORDER PATH (rationale and authorization decision: the
//     fulfillment/api package godoc).
//   - /store/v1/shipping-options — the eligible options for a cart. The cart
//     facts (subtotal, quantity, weight) are the CLIENT'S CLAIM at this
//     endpoint and cannot be verified; this is why options carrying a rule
//     bound to those facts are NEVER returned from this endpoint and are shown
//     to the customer over the cart flow (interop) instead (rationale:
//     [service.Service.ListShippingOptionsFor]).
package fulfillment

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/module"
	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/api"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/manual"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/repository"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// ModuleName is the module's name; it is the prefix of the container names and
// of the migration version ledger.
const ModuleName = "fulfillment"

// ServiceName is the module service's name in the container.
//
// Other modules and workflows reach the service under this name (WITHOUT
// importing this package, as ADR 0001/0006 requires) and use it through a
// narrow interface they define in THEIR OWN packages.
const ServiceName = ModuleName + ".service"

// InteropName is the cross-module primitive surface's name in the container
// (ADR 0006).
//
// It is registered SEPARATELY from the service itself: the service speaks in
// fulfillment's rich types, this surface only in primitive and stdlib types.
// The cart and order flows resolve it with their own narrow interfaces.
const InteropName = ModuleName + ".interop"

// ProvidersName is the provider registry's name in the container.
//
// The plugin system of Phase 9 resolves this registry and adds its own
// FulfillmentProvider to it; it does not need to change the module's code.
const ProvidersName = ModuleName + ".providers"

// ProviderName is the Query provider's name in the container (ADR 0004).
const ProviderName = service.EntityName + query.ProviderSuffix

// dbServiceName is the core database pool's name in the container.
const dbServiceName = "core.db"

// Error codes.
const (
	codeSetupFailed      = "fulfillment_module_setup_failed"
	codeProviderRegister = "fulfillment_module_provider_register_failed"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot is the embedded files with the "migrations/" prefix stripped:
// db.Migrate reads the source from the root.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// Module is the application the fulfillment module offers the core.
type Module struct {
	svc       *service.Service
	providers *service.ProviderRegistry
	handler   *api.Handler
}

// That the core's contract is satisfied is pinned down at compile time.
var _ module.Module = (*Module)(nil)

// That it can describe itself for the document is pinned down at compile time
// too.
//
// [openapi.Describer] is an OPTIONAL interface and the composition root looks
// for it with a TYPE ASSERTION; should the method name or signature drift,
// nothing would break at compile time — only the shipping endpoints would
// silently fall out of the document. This line closes that silence.
var _ openapi.Describer = (*Module)(nil)

// New produces a fulfillment module ready to be registered.
//
// Dependencies are resolved during Register, not here: until that moment the
// container may not have set up the core services.
func New() *Module { return &Module{} }

// Name returns the module's unique name.
func (m *Module) Name() string { return ModuleName }

// Migrations returns the module's migration files.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Register registers the service, the cross-module surface, the provider
// registry and the Query provider with the container.
//
// Only CORE services are resolved; other modules' services may not be
// registered yet at this stage (see the module.Module documentation). Because
// core.db is registered in main.go as a ready value before the modules come up,
// resolving it here is safe, and its absence is a setup error that makes the
// module unable to run at all — it is not silently deferred.
//
// The default provider ([manual.Provider]) is registered here. It uses the same
// repository instance but writes to a SEPARATE table; the service's
// [service.Store] interface has no methods for that table, so the module cannot
// reach the provider's ledger at the type level.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, dbServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the database pool (%q)", ModuleName, dbServiceName)
	}

	log := slog.Default().With("module", ModuleName)
	repo := repository.New(pool.Pool())

	providers := service.NewProviderRegistry()
	if err := providers.Register(manual.New(repo, log)); err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeProviderRegister,
			"the %s module could not register the default provider", ModuleName)
	}

	svc, err := service.New(service.Options{
		Store:     repo,
		Providers: providers,
		Logger:    log,
	})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s service could not be set up", ModuleName)
	}

	if err := c.Provide(ServiceName, svc); err != nil {
		return err
	}
	if err := c.Provide(InteropName, service.NewInterop(svc)); err != nil {
		return err
	}
	if err := c.Provide(ProvidersName, providers); err != nil {
		return err
	}
	// The provider name has the form "<entity>.query"; Query looks it up under
	// that name and verifies with Entity() that the name matches (ADR 0004).
	if err := c.Provide(ProviderName, service.NewQueryProvider(svc)); err != nil {
		return err
	}

	m.svc = svc
	m.providers = providers
	m.handler = api.New(svc)

	log.DebugContext(ctx, "fulfillment module registered",
		"service", ServiceName,
		"interop", InteropName,
		"providers", providers.IDs(),
		"query", ProviderName,
	)
	return nil
}

// Routes mounts the module's store and admin endpoints on the router.
//
// If Register did not run, no endpoint is mounted: rather than a handler
// without a service panicking on the first request, it is better for the
// endpoint not to exist at all.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
		slog.Default().Warn("Routes was called on the fulfillment module without Register, no route was mounted")
		return
	}
	m.handler.Routes(r)
}

// Describe writes the module's endpoints into the OpenAPI document.
//
// The description itself lives in [api.Describe]: the body schemas are derived
// from that package's unexported DTOs, and exporting those types only for the
// sake of the document would widen the module's surface.
//
// Unlike [Module.Routes] there is NO Register check, and none is needed: the
// schema comes from the types, not from the service. Putting a check there
// would silently empty the document of an unregistered module too.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// Service returns the module's service; it is nil if Register was not called.
//
// It is meant for tests and embedded use; in the normal flow the service is
// resolved from the container under the name [ServiceName].
func (m *Module) Service() *service.Service { return m.svc }

// Providers returns the module's provider registry; it is nil if Register was
// not called.
//
// An embedding application can add its own provider here; in the normal flow
// the registry is resolved from the container under the name [ProvidersName].
func (m *Module) Providers() *service.ProviderRegistry { return m.providers }

// mustSub opens the subdirectory; it panics if it cannot be opened.
//
// The panic is safe here: the directory name is constant at compile time and the
// embed directive has already verified at compile time that the files exist. Returning
// nil silently would nevertheless mean the module coming up without migrations
// (that is, without tables); a setup error must blow up openly.
func mustSub(files embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(files, dir)
	if err != nil {
		panic("fulfillment: could not open the embedded migrations directory: " + err.Error())
	}
	return sub
}
