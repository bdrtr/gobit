// Package module defines the contract commerce modules implement and the
// registry that brings them up in order.
//
// A module owns its own models, tables and service; it does NOT import another
// module's package (plan Sections 2.1/2.4, ADR 0001). Access between modules
// goes through service interfaces resolved from the container by name.
package module

import (
	"context"
	"io/fs"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
)

// Module is the contract a commerce module offers the core (plan Section 5.1).
type Module interface {
	// Name is the module's unique name (e.g. "product"). It is used as a
	// prefix in the container's service names and in the migration version
	// table.
	Name() string

	// Register registers the module's services in the container and declares
	// its link definitions and event subscribers.
	//
	// CAUTION: at this stage OTHER modules' services may not be registered
	// yet. Another module's service must therefore NOT be resolved here; give
	// the container a lazy constructor and resolve on first use.
	Register(ctx context.Context, c *container.Container) error

	// Migrations returns the module's migration files (usually an embed.FS).
	// A module with no migrations may return nil.
	Migrations() fs.FS

	// Routes binds the module's store and admin routes to the given router.
	Routes(r chi.Router)
}
