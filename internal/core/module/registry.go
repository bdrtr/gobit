package module

import (
	"context"
	"io/fs"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
)

// MigrateFunc is the function that applies a module's migrations.
//
// It is handed in from outside as a function so the Registry does not bind
// directly to the internal/core/db package; that also makes the registry
// testable without a database. owner is the module name used to keep the
// version tables apart.
type MigrateFunc func(ctx context.Context, src fs.FS, owner string) error

// Registry holds every module and calls Register/Migrate/Routes in order (plan
// Section 5.1).
type Registry struct {
	modules []Module
	migrate MigrateFunc
	log     *slog.Logger
}

// NewRegistry creates an empty module registry.
// With migrate nil the migration step is skipped (useful in tests).
func NewRegistry(log *slog.Logger, migrate MigrateFunc) *Registry {
	if log == nil {
		log = slog.Default()
	}
	return &Registry{migrate: migrate, log: log}
}

// Add adds a module to the registry. Modules are processed in the order they
// were added.
func (r *Registry) Add(mod Module) {
	r.modules = append(r.modules, mod)
}

// Modules returns the registered modules in the order they were added.
func (r *Registry) Modules() []Module {
	return append([]Module(nil), r.modules...)
}

// Bootstrap brings the modules up in order: it validates the names first, then
// runs every module's Register, then their migrations, and finally their
// routes.
//
// The order is deliberate: none of them binds a route before ALL of them have
// registered, so one module's handler can safely resolve another module's
// service.
func (r *Registry) Bootstrap(ctx context.Context, c *container.Container, router chi.Router) error {
	if err := r.validateNames(); err != nil {
		return err
	}
	if err := r.registerAll(ctx, c); err != nil {
		return err
	}
	if err := r.migrateAll(ctx); err != nil {
		return err
	}
	r.mountRoutes(router)

	r.log.InfoContext(ctx, "the modules are up", "count", len(r.modules))
	return nil
}

// validateNames checks that no module name is empty or repeated.
func (r *Registry) validateNames() error {
	seen := make(map[string]struct{}, len(r.modules))
	for _, mod := range r.modules {
		name := mod.Name()
		if name == "" {
			return errors.Invalid("module_name_empty", "a module name cannot be empty")
		}
		if _, dup := seen[name]; dup {
			return errors.Conflict("module_name_duplicate", "the module name is repeated: %s", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

// registerAll registers every module's services in the container.
func (r *Registry) registerAll(ctx context.Context, c *container.Container) error {
	for _, mod := range r.modules {
		if err := mod.Register(ctx, c); err != nil {
			return errors.Wrap(err, errors.KindOf(err), "module_register_failed",
				"the %s module could not be registered", mod.Name())
		}
		r.log.DebugContext(ctx, "module registered", "module", mod.Name())
	}
	return nil
}

// migrateAll applies every module's migrations to its own version table.
func (r *Registry) migrateAll(ctx context.Context) error {
	if r.migrate == nil {
		r.log.DebugContext(ctx, "no migration function was given, skipping migrations")
		return nil
	}
	for _, mod := range r.modules {
		src := mod.Migrations()
		if src == nil {
			continue
		}
		if err := r.migrate(ctx, src, mod.Name()); err != nil {
			return errors.Wrap(err, errors.KindOf(err), "module_migrate_failed",
				"the %s module's migrations could not be applied", mod.Name())
		}
		r.log.DebugContext(ctx, "module migrations applied", "module", mod.Name())
	}
	return nil
}

// mountRoutes binds every module's routes to the router.
func (r *Registry) mountRoutes(router chi.Router) {
	if router == nil {
		return
	}
	for _, mod := range r.modules {
		mod.Routes(router)
	}
}
