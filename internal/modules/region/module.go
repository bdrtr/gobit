// Package region is the region and currency module (plan Section 6, Phase 5).
//
// Its responsibility in one sentence: to define in which currency and in which
// tax region a sale is made. The module is the SOLE writer of Region, Currency
// and Country data (Principle 2.3).
//
// # The foundation of the cart flow
//
// The cart takes its currency and tax region from here: the region is found
// from the customer's country ([service.Service.RegionIDForCountry]), the
// region's currency is written onto the cart ([service.Service.RegionCurrency])
// and the tax line is calculated with the region's FALLBACK rate
// ([service.Service.RegionTax]). These three methods are written with primitive
// types so that a consuming module can define its own narrow interface without
// importing region (ADR 0001).
//
// # Reference data
//
// Currency and Country are reference data and are seeded by a migration
// (000002_region_seed): 41 currencies and the 249 countries of ISO 3166-1.
// Every installation cannot be expected to enter them by hand; a single
// country left out means a cart cannot be opened for a customer in that
// country.
//
// # What it does not know
//
// region imports no module and is unaware that carts and orders exist. The cart
// and the order carry the region IN THEIR OWN COLUMNS; mirroring that with a
// link was tried and removed because no reader turned up (see CHANGELOG,
// "cart_region"). Today there is NO link pointing at region — should the need
// arise, the declaring side has to read the note below.
//
// # The surfaces it exposes
//
//   - "region.service" — the service for cross-module calls (see
//     internal/modules/region/service, "Cross-module surface").
//   - "region.query" — the read provider opened to the Query layer (ADR 0004).
//     Records come back with their currency and countries.
//   - /admin/v1/regions, /admin/v1/currencies, /admin/v1/countries — the admin API.
//   - /store/v1/regions — the storefront's currency/region choice.
//
// # A note for the side that declares the link
//
// Query finds an expansion's target provider FROM THE MODULE NAME AT THE END
// of the link definition (see core/query targetSide: the target name +
// ".query" is looked up). For region the entity name and the module name are
// THE SAME ("region"), so the module declaring the link can write the end
// naturally. The definition below is HYPOTHETICAL; no such link exists today,
// and adding one is right only if it has a reader that TRAVERSES it (see
// internal/arch TestTheLinkDefinitionsAreTraversed):
//
//	link.LinkDefinition{
//	    Name:        "order_region",
//	    From:        link.LinkSide{Module: "order", Field: "order_id"},
//	    To:          link.LinkSide{Module: "region", Field: "region_id"},
//	    Cardinality: link.OneToOne,
//	}
package region

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
	"github.com/bdrtr/gobit/core/openapi"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/region/api"
	"github.com/bdrtr/gobit/internal/modules/region/repository"
	"github.com/bdrtr/gobit/internal/modules/region/service"
)

// Names in the container.
const (
	// ModuleName is the module's unique name; it is also the prefix of the
	// migration version table.
	ModuleName = "region"
	// ServiceName is the service's name in the container. Consuming modules
	// resolve it by this name and through a narrow interface they define
	// THEMSELVES (ADR 0001).
	ServiceName = ModuleName + ".service"
	// ProviderName is the query provider's name in the container (ADR 0004).
	ProviderName = service.Entity + query.ProviderSuffix
	// dbServiceName is the core database pool's name in the container.
	dbServiceName = "core.db"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationsRoot is the root directory of the migration files.
//
// The golang-migrate source looks at the root (iofs.New(src, ".")), whereas the
// embed.FS keeps the files under "migrations/"; that is why the subtree is
// opened once, here.
var migrationsRoot = mustSub(migrationsFS, "migrations")

// Module is the region module's [module.Module] implementation.
type Module struct {
	svc *service.Service
	api *api.API
	log *slog.Logger
}

var _ module.Module = (*Module)(nil)

// That it can describe itself for the document is pinned down at compile time
// too.
//
// [openapi.Describer] is an OPTIONAL interface and the composition root looks
// for it with a TYPE ASSERTION; should the method name or signature drift,
// nothing would break at compile time — only the region endpoints would
// silently fall out of the document. This line closes that silence.
var _ openapi.Describer = (*Module)(nil)

// New produces a region module that is not yet set up; the service is built in
// [Module.Register]. If log is nil, logs are discarded.
func New(log *slog.Logger) *Module {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Module{log: log}
}

// Name returns the module's name.
func (m *Module) Name() string { return ModuleName }

// Register registers the service and the query provider in the container.
//
// region needs no MODULE's service; it resolves only the core pool. Because the
// pool is registered BEFORE Bootstrap, resolving it directly here is safe — the
// only thing that would create a dependency on module order is resolving
// another MODULE's service, and that is not done.
//
// No link definition is declared, and there is no link pointing at region
// either: the sides that carry a region id keep it in their own columns.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, dbServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindUnavailable, "region_db_unavailable",
			"the region module could not resolve the %q service", dbServiceName)
	}

	repo := repository.New(pool.Pool())
	m.svc = service.New(repo, service.Options{Logger: m.log})
	m.api = api.New(m.svc)

	if err := c.Provide(ServiceName, m.svc); err != nil {
		return err
	}
	if err := c.Provide(ProviderName, service.NewQueryProvider(m.svc)); err != nil {
		return err
	}

	m.log.InfoContext(ctx, "region module registered",
		slog.String("service", ServiceName),
		slog.String("provider", ProviderName),
	)
	return nil
}

// Migrations returns the module's migration files.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Routes mounts the module's admin and store routes on the router.
//
// It is called AFTER Register (see module.Registry.Bootstrap), so api is set up
// by then. There is a nil check all the same: if Register fails and Bootstrap
// is cut short, Routes is never called, but if the module is used by hand a
// quiet no-op is safer than a panic.
func (m *Module) Routes(r chi.Router) {
	if m.api == nil {
		m.log.Warn("Routes was called on the region module without Register, no route was mounted")
		return
	}
	m.api.Routes(r)
}

// Describe writes the module's endpoints into the OpenAPI document.
//
// The description itself lives in [api.Describe]: the body schemas are derived
// from that package's unexported DTOs, and exporting the types only for the
// sake of the document would widen the module's surface.
//
// Unlike [Module.Routes] there is NO api check, and none is needed: the schema
// comes from the types, not from the service. Adding a check would silently
// empty the document of a module that is not set up, too.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// Service returns the built service; nil if Register has not been called.
//
// It is for tests and embedding applications that use the module directly; in
// the normal flow the service is resolved from the container under the name
// [ServiceName].
func (m *Module) Service() *service.Service { return m.svc }

// mustSub opens the sub file system; it panics if it cannot be opened.
//
// The error path is unreachable, because //go:embed guarantees at compile time
// that the directory exists. Returning nil silently would nevertheless mean the
// module coming up without migrations (that is, without tables); a setup error
// must blow up openly.
func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("region: the migration directory could not be opened: " + err.Error())
	}
	return sub
}
