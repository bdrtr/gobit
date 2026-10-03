// Package pricing is the pricing module (plan Section 6, Phase 4).
//
// Its responsibility in one sentence: to hold the container of a variant's
// prices (PriceSet) and to select the valid price in a given context. The
// module is the SOLE writer of PriceSet, Price, PriceList and PriceRule data
// (Principle 2.3).
//
// # What it does not know
//
// pricing imports no module and is unaware that variants exist. The bond with
// product is set up with the "product_variant_price_set" link that product
// declares; the link table lives in the core and pricing never sees it
// (Principle 2.2: there is no cross-module FK).
//
// # The surfaces it exposes
//
//   - "pricing.service" — the service for cross-module calls (see
//     internal/modules/pricing/service, "The cross-module surface").
//   - "price_set.query" — the read provider opened to the Query layer
//     (ADR 0004). Records come back WITH THEIR PRICES so that product's store
//     listing can see the price in one call.
//   - /admin/v1/price-sets, /admin/v1/price-lists … — the admin API.
//   - /store/v1/price-sets/{id} — the single read endpoint.
//
// # A note for the side that declares the link
//
// Query finds an expansion's target provider FROM THE MODULE NAME AT THE END
// of the link definition (see core/query targetSide: the target name +
// ".query" is looked up). That is why the module that declares the link has to
// write the pricing end WITH THE ENTITY NAME:
//
//	link.LinkDefinition{
//	    Name:        "product_variant_price_set",
//	    From:        link.LinkSide{Module: "product_variant", Field: "variant_id"},
//	    To:          link.LinkSide{Module: "price_set", Field: "price_set_id"},
//	    Cardinality: link.OneToOne,
//	}
//
// If the end is written as "pricing", Query looks up the name "pricing.query"
// and returns errors.NotFound; the provider is registered under the name
// "price_set.query".
package pricing

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
	"github.com/bdrtr/gobit/internal/modules/pricing/api"
	"github.com/bdrtr/gobit/internal/modules/pricing/repository"
	"github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// Names in the container.
const (
	// Name is the module's unique name; it is also the prefix of the migration
	// version table.
	Name = "pricing"
	// ServiceName is the service's name in the container. Consumer modules
	// resolve it under this name and with the narrow interface THEY define
	// (ADR 0001).
	ServiceName = Name + ".service"
	// ProviderName is the query provider's name in the container (ADR 0004).
	ProviderName = service.Entity + query.ProviderSuffix
	// AdminName is the container name of the module's ADMIN WRITE surface
	// (ADR 0013). Its only audience is the admin panel, and the restriction is
	// checked in internal/arch.
	AdminName = Name + ".admin"
	// DBName is the container name of the core database pool.
	DBName = "core.db"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Module is the pricing module's [module.Module] implementation.
type Module struct {
	svc *service.Service
	api *api.API
	log *slog.Logger
}

var _ module.Module = (*Module)(nil)

// That it can describe itself in the document is pinned at compile time too.
//
// [openapi.Describer] is an OPTIONAL interface and the composition root looks
// for it with a TYPE ASSERTION; if the method name or signature drifted,
// nothing would break at compile time, the price endpoints would just silently
// drop out of the document. This line closes that silence.
var _ openapi.Describer = (*Module)(nil)

// New builds an unwired pricing module; the service is built in
// [Module.Register]. If log is nil, logs are discarded.
func New(log *slog.Logger) *Module {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Module{log: log}
}

// Name returns the module's name.
func (m *Module) Name() string { return Name }

// Register registers the service and the query provider in the container.
//
// pricing needs no MODULE's service; it only resolves the core pool. Because
// the pool is registered BEFORE Bootstrap, resolving it directly here is safe —
// the only thing that would create a dependency on module order is resolving
// another MODULE's service, and that is not done.
//
// No link definition is declared: the owner of the "product_variant_price_set"
// link is product, and pricing does not know that link.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, DBName)
	if err != nil {
		return errors.Wrap(err, errors.KindUnavailable, "pricing_db_unavailable",
			"the pricing module could not resolve the %q service", DBName)
	}

	repo := repository.New(pool.Pool())
	m.svc = service.New(repo, service.Options{Logger: m.log})
	m.api = api.New(m.svc).WithTrial(&listTrial{c: c, log: m.log})

	if err := c.Provide(ServiceName, m.svc); err != nil {
		return err
	}
	if err := c.Provide(ProviderName, service.NewQueryProvider(m.svc)); err != nil {
		return err
	}
	// The admin write surface is registered under a SEPARATE name; the reason
	// is in [AdminName] and in ADR 0013.
	if err := c.Provide(AdminName, service.NewAdminSurface(m.svc)); err != nil {
		return err
	}

	m.log.InfoContext(ctx, "pricing module registered",
		slog.String("service", ServiceName),
		slog.String("provider", ProviderName),
	)
	return nil
}

// Migrations returns the module's migration files.
//
// The root is narrowed to the "migrations" subdirectory; golang-migrate looks
// for the files at the ROOT of the source, and embed.FS would carry them
// together with the directory name.
func (m *Module) Migrations() fs.FS {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		// The embed path is fixed at compile time; landing here means the
		// migrations directory was not embedded, and that cannot pass silently.
		panic("pricing: could not open the migration source: " + err.Error())
	}
	return sub
}

// Routes binds the module's admin and store routes to the router.
//
// It is called AFTER Register (see module.Registry.Bootstrap), so api is
// already built. There is a nil check all the same: if Register fails and
// Bootstrap is cut short, Routes is never called, but if the module is used by
// hand, a silent no-op is safer than a panic.
func (m *Module) Routes(r chi.Router) {
	if m.api == nil {
		return
	}
	m.api.Routes(r)
}

// Describe writes the module's admin and storefront endpoints into the OpenAPI
// document.
//
// The description itself is in [api.Describe]: the body schemas are derived
// from that package's unexported DTOs, and exporting the types for the sake of
// the document alone would widen the module's surface.
//
// Unlike [Module.Routes] there is NO api check, and none is needed: the schema
// comes from the types, not from the service. Adding a check would silently
// empty the document of an unwired module as well.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// Service returns the built service; nil if Register has not been called.
//
// It is for tests and embedding applications that use the module directly; in
// the normal flow the service is resolved from the container under the name
// [ServiceName].
func (m *Module) Service() *service.Service { return m.svc }
