// Package loyalty is a commerce module a customer project wrote itself.
//
// It is deliberately thin — a name, a route and no schema — because what it
// demonstrates is not loyalty but REACH: a module defined outside the gobit
// repository satisfies the published contract, enters the same registry as the
// modules in the box, and its migrations and routes are handled by the same
// lifecycle.
package loyalty

import (
	"context"
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/module"
)

// Module is the customer project's own module.
type Module struct{}

// New builds the module.
func New() *Module { return &Module{} }

// Name is the module's unique name; it prefixes its container services and its
// migration version table.
func (m *Module) Name() string { return "loyalty" }

// Register would put the module's services in the container. This one has none.
func (m *Module) Register(_ context.Context, _ *container.Container) error { return nil }

// Migrations returns no schema: nil is the answer for a module that owns no
// table, and the lifecycle accepts it.
func (m *Module) Migrations() fs.FS { return nil }

// Routes binds the module's endpoints with their FULL paths, the way every
// module in the box does.
func (m *Module) Routes(r chi.Router) {
	r.Get("/store/v1/loyalty/balance", func(w http.ResponseWriter, r *http.Request) {
		corehttp.WriteJSON(r.Context(), w, http.StatusOK, map[string]int{"points": 0})
	})
}

// compile-time proof that a module written outside gobit satisfies the contract.
var _ module.Module = (*Module)(nil)
