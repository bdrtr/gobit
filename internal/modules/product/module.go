// Package product is the catalog module: products, variants, options,
// categories, collections, tags and images are this module's data.
//
// # The module's contract with the core
//
// [Module] satisfies the core's module.Module interface. Register does four
// things:
//
//  1. The service is registered in the container under the name
//     "product.service".
//  2. The PRIMITIVE cross-module read surface is registered under the name
//     "product.interop" (ADR 0006); plugins and workflows read the catalog
//     record from there.
//  3. The Query providers are registered under the names "product.query" and
//     "variant.query" (ADR 0004).
//  4. The price, stock and sales channel link definitions are declared
//     (ADR 0005).
//
// # Published events
//
// "product.created", "product.updated" and "product.deleted" — when a product
// is written, updated and deleted. For their payloads and the publication
// policy see [service.EventProductCreated] and service/events.go.
//
// # Other modules
//
// The pricing, inventory and auth packages are NOT IMPORTED (Principle 2.4,
// ADR 0001; the rule is enforced in CI by depguard inside .golangci.yml).
// Price and stock data is visible only through link names and the Query
// layer; the sales channel is visible only as a link name and as identity
// strings coming from the request's principal (see
// service.LinkProductSalesChannel).
package product

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/product/api"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// Name is the module's name: it is the prefix of the container names, of the
// migration version ledger and of the log fields.
const Name = "product"

// Names of the core services resolved from the container.
const (
	svcDB       = "core.db"
	svcLink     = "core.link"
	svcQuery    = "core.query"
	svcEventBus = "core.eventbus"
)

// ServiceName is the name of the module's service in the container.
//
// Other modules (WITHOUT importing this package, as ADR 0001 requires) reach
// the catalog service under this name.
const ServiceName = Name + ".service"

// InteropName is the name of the cross-module primitive surface in the
// container (ADR 0006).
//
// It is registered SEPARATELY from the service itself: the service speaks in
// product's rich types, this surface only in primitive and stdlib types.
// Because plugins (plugins/**) cannot import any module, they can read the
// catalog ONLY under this name and through the narrow interface they define
// themselves.
const InteropName = Name + ".interop"

// AdminName is the name of the module's ADMIN WRITE surface in the container
// (ADR 0013).
//
// It is a name SEPARATE from interop and the separation is deliberate:
// interop is the surface on which other modules, workflows and plugins READ
// the catalog, and its godoc promises to stay narrow; adding a write method
// there would give every plugin the right to rewrite the catalog. The only
// audience of this name is the admin panel, and THIS CONSTRAINT is not merely
// documented, it is checked in internal/arch.
const AdminName = Name + ".admin"

// Error codes.
const (
	codeSetupFailed = "product_module_setup_failed"
	codeLinkDefine  = "product_link_define_failed"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot is the embedded files with the "migrations/" prefix
// stripped: db.Migrate reads the source from the root.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// Options holds the setup settings of the product module.
//
// The module does NOT KNOW the internal/core/config package (Principle 2.4)
// and config is not registered in the container either; the settings come as
// a parameter from whoever wires the application (the very same pattern as in
// the auth and file modules).
type Options struct {
	// GraphQL holds the hardening limits of the storefront's GraphQL read
	// endpoint.
	//
	// The type is NOT a type of the module's own but [graph.Options]
	// directly: passing a copy in between would have meant a second
	// definition of the limits, plus mapping code that someone forgets to
	// update at every new limit.
	//
	// Its zero value gives the package defaults; it does NOT MEAN
	// "unlimited".
	GraphQL graph.Options
}

// Module is the application the product module offers to the core.
type Module struct {
	opts    Options
	svc     *service.Service
	handler *api.Handler
}

// That the core contract is satisfied is pinned at compile time.
var _ module.Module = (*Module)(nil)

// That it can describe itself in the document is pinned at compile time too.
//
// [openapi.Describer] is an OPTIONAL interface and the composition root looks
// for it with a TYPE ASSERTION; if the method name or its signature drifted,
// nothing would break at compile time, only the storefront endpoints would
// silently fall out of the document. This line closes that silence.
var _ openapi.Describer = (*Module)(nil)

// New produces a module ready to be registered.
//
// The dependencies are resolved during Register and not here: by that moment
// the container may not have set up the core services yet.
//
// [Options] may be called with its zero value; embedded use and the tests do
// exactly that, and the GraphQL endpoint opens with the package default
// limits.
func New(opts Options) *Module {
	return &Module{opts: opts}
}

// Name returns the module's unique name.
func (m *Module) Name() string { return Name }

// Migrations returns the module's migration files.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Register registers the service, the interop surface and the Query providers
// in the container, and declares the link definitions.
//
// Only CORE services are resolved; other modules' services may not be
// registered yet at this stage (see the module.Module documentation).
//
// # The event bus is MANDATORY and startup stops when it is missing
//
// Just like core.db, core.link and core.query, core.eventbus is registered in
// main.go as a ready value before the modules come up; its absence is not a
// deployment shape but a SETUP ERROR. That is why "let the events be skipped
// silently" was not chosen: that road would have made the fault invisible —
// the catalog keeps working, no error shows up, only the search index is not
// updated, and the gap would be noticed only when customers cannot find the
// new products, that is IN PRODUCTION. A setup that falls over at startup, on
// the other hand, is seen in the first second.
//
// Silent skipping is still possible, but ONLY for embedded use and tests:
// [service.Options].Events may be given as nil (see
// service.Service.publishProductEvent). That road does not pass through
// Register.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, svcDB)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the database pool (%q)", Name, svcDB)
	}
	links, err := container.Resolve[link.LinkService](c, svcLink)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the link service (%q)", Name, svcLink)
	}
	queryLayer, err := container.Resolve[query.Query](c, svcQuery)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the query layer (%q)", Name, svcQuery)
	}
	// Resolved through a narrow interface: the module only PUBLISHES, it does
	// not subscribe and does not close the bus (see service.EventPublisher).
	bus, err := container.Resolve[service.EventPublisher](c, svcEventBus)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the event bus (%q)", Name, svcEventBus)
	}

	repo := repository.New(pool.Pool())
	svc, err := service.New(service.Options{
		Repo:   repo,
		Links:  links,
		Query:  queryLayer,
		Events: bus,
		// At startup the application installs the configured logger with
		// slog.SetDefault; the module does not look for a separate logger
		// registration.
		Logger: slog.Default().With("module", Name),
	})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s service could not be set up", Name)
	}

	if err := c.Provide(ServiceName, svc); err != nil {
		return err
	}
	// The cross-module surface is registered under a SEPARATE name: the
	// service itself speaks in product's rich types, this surface only in
	// primitive ones (ADR 0006).
	if err := c.Provide(InteropName, service.NewInterop(svc)); err != nil {
		return err
	}
	// The admin write surface is registered under a SEPARATE name as well;
	// the rationale is in [AdminName] and in ADR 0013.
	if err := c.Provide(AdminName, service.NewAdminSurface(svc)); err != nil {
		return err
	}
	// Provider names have the form "<entity>.query"; Query looks them up
	// under that name and verifies with Entity() that the name matches
	// (ADR 0004).
	if err := c.Provide(service.EntityProduct+query.ProviderSuffix, service.NewProductProvider(repo)); err != nil {
		return err
	}
	if err := c.Provide(service.EntityVariant+query.ProviderSuffix, service.NewVariantProvider(repo)); err != nil {
		return err
	}

	// The link definitions are declared HERE: the schema stays in the same
	// place as the definition itself and is verified idempotently at every
	// startup (ADR 0005).
	for _, def := range service.Definitions() {
		if err := links.Define(ctx, def); err != nil {
			return errors.Wrap(err, errors.KindOf(err), codeLinkDefine,
				"the %q link definition could not be declared", def.Name)
		}
	}

	m.svc = svc
	m.handler = api.New(svc, m.opts.GraphQL)
	return nil
}

// Routes mounts the module's store and admin endpoints on the router.
//
// If Register did not run, no endpoint is mounted: rather than have a handler
// without a service panic on the first request, it is better for the endpoint
// not to exist at all.
//
// The storefront's GraphQL read endpoint (POST /store/v1/graphql) goes
// through here too; all of the endpoints stand in a single list inside
// [api.Handler.Routes]. For the scope of the GraphQL surface and the sales
// channel rule see the graph package.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
		return
	}
	m.handler.Routes(r)
}

// Describe writes the module's storefront and admin endpoints into the
// OpenAPI document.
//
// The description itself lives in [api.Describe] and it lives there for two
// reasons. The query parameters are the ones the handler actually reads, and
// that reading is in the api package; if the list stood here it would drift
// away from the reading and the two would silently diverge. The request
// bodies of the admin endpoints, in turn, are that package's UNEXPORTED DTOs;
// exporting those types merely for the sake of the document would widen the
// module's surface.
//
// Unlike [Module.Routes] there is NO Register check, and none is needed: the
// schema comes from the types, not from the service.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// Service returns the module's service; it is nil if Register was not called.
//
// It is meant for tests and embedded use; in the normal flow the service is
// resolved from the container under the name [ServiceName].
func (m *Module) Service() *service.Service { return m.svc }

// mustSub opens the subdirectory; it panics if it cannot be opened.
//
// The panic is safe here: the directory name is constant at compile time and the
// embed directive has already verified at compile time that the files exist.
func mustSub(files embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(files, dir)
	if err != nil {
		panic("product: could not open the embedded migration directory: " + err.Error())
	}
	return sub
}
