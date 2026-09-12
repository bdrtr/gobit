package analytics

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
)

// The error codes; a caller can look at them with coreerrors.CodeOf.
const (
	codeSetupFailed   = "analytics_module_setup_failed"
	codeNotRegistered = "analytics_module_not_registered"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot is the embedded files with their "migrations/" prefix stripped:
// db.Migrate reads its source FROM THE ROOT.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// funnelModule is the module this plugin brings.
//
// Belonging to a plugin changes nothing about its behavior: the core does not
// tell it apart from the others and puts it through the same lifecycle
// (Register -> migration -> routes).
type funnelModule struct {
	// log is the logger tagged with the module's name.
	log *slog.Logger
	// store is the event table; it is built in Register and is nil before that.
	store eventStore
}

// That the module satisfies the core contract is fixed at compile time.
var _ module.Module = (*funnelModule)(nil)

// newFunnelModule produces a module ready to be registered.
//
// The pool is resolved in Register and NOT here: at Setup time the core services
// are not in the container yet.
func newFunnelModule(_ *container.Container, log *slog.Logger) *funnelModule {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &funnelModule{log: log.With("module", ModuleName)}
}

// Name returns the module's unique name.
func (m *funnelModule) Name() string { return ModuleName }

// Migrations returns the module's migration files.
func (m *funnelModule) Migrations() fs.FS { return migrationsRoot }

// Register resolves the database pool.
func (m *funnelModule) Register(_ context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, svcDB)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the database pool (%q)", ModuleName, svcDB)
	}

	m.store = newEventStore(pool.Pool())

	return nil
}

// Routes binds the module's admin endpoint.
//
// If Register did not run, NO endpoint is bound: rather than a handler with no
// table producing an error on the first request, it is better for the endpoint
// not to exist at all (searchpg's reasoning, and the product module's before it).
//
// The endpoint requires [ScopeRead]. A funnel is a business figure rather than a
// secret, but it is still the shop's own performance, and the panel's read scope
// is the narrowest thing that already exists to say so.
func (m *funnelModule) Routes(r chi.Router) {
	if m.store == nil {
		return
	}

	r.With(corehttp.RequireScope(ScopeRead)).Get(FunnelPath, m.funnel)
}

// ready reports whether the module can serve a request.
//
// Entering a handler without Register having run is impossible in the ordinary
// flow (Routes binds nothing in that case), but the event handlers run
// INDEPENDENTLY of Routes: the subscription is set up by the core, and if the
// module was not registered the store stays nil. Returning a typed error rather
// than panicking makes the fault visible in the log.
func (m *funnelModule) ready() error {
	if m.store == nil {
		return coreerrors.Unavailable(codeNotRegistered,
			"the %s module was not registered; the funnel is unavailable", ModuleName)
	}

	return nil
}

// mustSub opens the subdirectory and panics if it cannot.
//
// A panic is right here and nowhere else: the path is a compile-time constant
// beside the embed directive, so a failure means the binary was built wrongly
// rather than that an installation is misconfigured.
func mustSub(files embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(files, dir)
	if err != nil {
		panic("analytics: the embedded migration directory could not be opened: " + err.Error())
	}

	return sub
}
