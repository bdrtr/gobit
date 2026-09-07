package searchpg

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/core/query"
)

// The error codes; a caller can look at them with coreerrors.CodeOf.
const (
	codeSetupFailed   = "searchpg_module_setup_failed"
	codeNotRegistered = "searchpg_module_not_registered"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot is the embedded files with their "migrations/" prefix
// stripped: db.Migrate reads its source FROM THE ROOT.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// searchModule is the commerce module this plugin brings.
//
// Belonging to a plugin changes nothing about its behavior: the core does NOT
// tell it apart from the others and puts it through the same lifecycle
// (Register -> migration -> routes). What is interesting about it is that the
// DATA is its own while the RECORDS belong to another module; the only bond
// between the two is an identifier string and the "product.interop" surface.
type searchModule struct {
	// catalog is the LAZILY resolved read surface over the storefront records.
	catalog *catalog
	// log is the logger tagged with the module's name.
	log *slog.Logger

	// index is the search table; it is built in Register and is nil before that.
	index store
	// graph pages the product ids during a reindex. It is the core's Query layer
	// and is resolved in Register.
	graph query.Query
}

// That the module satisfies the core contract is fixed at compile time.
var _ module.Module = (*searchModule)(nil)

// newSearchModule produces a module ready to be registered.
//
// The dependencies are resolved in Register and NOT here; the container is kept
// only for the lazy resolution of the catalog surface (see [catalog]).
func newSearchModule(c *container.Container, log *slog.Logger) *searchModule {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &searchModule{catalog: newCatalog(c), log: log.With("searchModule", ModuleName)}
}

// Name returns the module's unique name.
func (m *searchModule) Name() string { return ModuleName }

// Migrations returns the module's migration files.
//
// The version ledger is separate per module name ("searchpg_schema_migrations"),
// so the plugin's schema never mixes with any module's ledger; when the plugin
// is removed what stays behind is only its own table.
func (m *searchModule) Migrations() fs.FS { return migrationsRoot }

// Register builds the index store and the Query layer.
//
// Only CORE services are resolved: "core.db" and "core.query" are put into the
// container before the modules come up. Another MODULE's services are not
// resolved here — they may not be registered at this stage (see the
// module.Module documentation), and because a plugin's module is added at the
// END of the registration order, the assumption "product is already registered"
// would be true today and silently wrong tomorrow. That is why the catalog
// surface is resolved on first use.
//
// If either is missing, startup STOPS. Skipping quietly was not chosen: a search
// endpoint running without an index would answer every query with an empty list,
// and that is noticed only when customers cannot find any product — that is, in
// production.
func (m *searchModule) Register(_ context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, svcDB)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the database pool (%q)", ModuleName, svcDB)
	}
	graph, err := container.Resolve[query.Query](c, svcQuery)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the query layer (%q)", ModuleName, svcQuery)
	}

	m.index = newIndex(pool.Pool())
	m.graph = graph

	return nil
}

// Routes binds the module's storefront and admin endpoints to the router.
//
// If Register did not run, NO endpoint is bound: rather than a handler with no
// index producing an error on the first request, it is better for the endpoint
// not to exist at all (the same reasoning as the product module's Routes).
//
// # Scopes
//
// The storefront endpoint requires NO scope: /store/v1's identity is the
// publishable key, and that key carries no scope by definition. The admin
// endpoint requires [ScopeWrite]; the identity layer (corehttp.RequireAdmin) is
// attached by whoever builds the router.
func (m *searchModule) Routes(r chi.Router) {
	if m.index == nil {
		return
	}

	r.Get(SearchPath, m.search)
	r.With(corehttp.RequireScope(ScopeWrite)).Post(ReindexPath, m.reindexEndpoint)
}

// ready reports whether the module can serve a request.
//
// Entering a handler without Register having run is impossible in the ordinary
// flow (Routes binds no endpoint in that case), but the event handlers run
// INDEPENDENTLY of Routes: the subscription is set up by the core, and if the
// module was not registered the index stays nil. Returning a typed error rather
// than panicking makes the fault visible in the log.
func (m *searchModule) ready() error {
	if m.index == nil {
		return coreerrors.Unavailable(codeNotRegistered,
			"the %s module was not registered; the search index is unavailable", ModuleName)
	}
	return nil
}

// mustSub opens the subdirectory and panics if it cannot.
//
// The panic is safe here: the directory name is fixed at compile time, and the
// embed directive has already verified at compile time that the files exist.
func mustSub(files embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(files, dir)
	if err != nil {
		panic("searchpg: the embedded migration directory could not be opened: " + err.Error())
	}
	return sub
}
