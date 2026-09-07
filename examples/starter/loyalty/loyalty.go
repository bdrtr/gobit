// Package loyalty is a commerce module a customer project wrote itself.
//
// It is deliberately thin — a name, a route and no schema — because what it
// demonstrates is not loyalty but REACH: a module defined outside the gobit
// repository satisfies the published contract, enters the same registry as the
// modules in the box, and its migrations and routes are handled by the same
// lifecycle.
//
// Since ADR 0035 it also DESCRIBES its endpoint, and that method is the point of
// the ADR rather than a decoration. Until then the schema vocabulary lived under
// internal/, so this file could not name it: the route below went into
// /openapi.json with a path, a method and no body, and no audit anywhere said
// so. The proof that the impossibility is gone is that [Module.Describe]
// compiles HERE, in a separate Go module, and not that an in-tree module can
// still do what it always could.
package loyalty

import (
	"context"
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/core/openapi"
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

// Describe writes the module's endpoint into the OpenAPI document.
//
// The path and method have to be spelled exactly as [Module.Routes] binds them;
// a description that matches no route does not silently vanish, the core reports
// it through [openapi.Doc.UnmatchedDescriptions]. The reverse direction — a
// route no description matched — is [openapi.Doc.UndescribedRoutes], and it is
// what an embedder who forgets this method will see.
func (m *Module) Describe(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/store/v1/loyalty/balance", openapi.Operation{
		Summary:     "The customer's loyalty point balance",
		Description: "Always answers zero; the example module keeps no state.",
		Tags:        []string{"Loyalty"},
	})
}

// compile-time proof that a module written outside gobit satisfies the contract,
// and that the OPTIONAL schema capability is reachable from outside it too.
var (
	_ module.Module     = (*Module)(nil)
	_ openapi.Describer = (*Module)(nil)
)
