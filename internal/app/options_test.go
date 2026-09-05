package app

import (
	"context"
	"io/fs"
	"log/slog"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/internal/core/config"
)

// TestTheCallersModulesReachTheRegistry closes the other half of the silent
// failure the facade's own test names.
//
// [github.com/bdrtr/gobit.App.Add] putting a module into Options proves nothing
// on its own: the composition root has to take them out again. If this loop
// were dropped the installation would start, serve every request gobit ships
// and answer none of the caller's — with no error anywhere, and both
// out-of-tree examples still compiling and running.
func TestTheCallersModulesReachTheRegistry(t *testing.T) {
	t.Parallel()

	registry := module.NewRegistry(slog.Default(), nil)

	registerModules(registry, config.Config{}, slog.Default(),
		[]module.Module{&fakeModule{name: "loyalty"}, &fakeModule{name: "gift-cards"}})

	all := registry.Modules()
	require.GreaterOrEqual(t, len(all), 2, "the registry did not take the caller's modules at all")
	require.Equal(t, []string{"loyalty", "gift-cards"}, moduleNames(all[len(all)-2:]),
		"the caller's modules did not reach the registry, or lost their order")
}

// TestTheCallersModulesComeLast keeps the registration order the ADR states.
//
// A name collision is refused rather than replaced, so ORDER decides which of
// the two survives. The modules in the box go in first, which means a caller
// cannot take a shipped name by accident and find its own module winning.
func TestTheCallersModulesComeLast(t *testing.T) {
	t.Parallel()

	registry := module.NewRegistry(slog.Default(), nil)
	registerModules(registry, config.Config{}, slog.Default(),
		[]module.Module{&fakeModule{name: "loyalty"}})

	all := moduleNames(registry.Modules())
	require.Positive(t, len(all)-1,
		"no module in the box was registered, so this test proved nothing about order")
	require.Equal(t, "loyalty", all[len(all)-1],
		"the caller's module is not last; a shipped module registered after it would "+
			"decide a name collision the other way round")
}

// moduleNames is the registered names in order.
func moduleNames(modules []module.Module) []string {
	out := make([]string, 0, len(modules))
	for _, m := range modules {
		out = append(out, m.Name())
	}

	return out
}

// fakeModule is a module written the way a customer project would write one.
type fakeModule struct{ name string }

// Name is the module's unique name.
func (m *fakeModule) Name() string { return m.name }

// Register puts nothing in the container.
func (m *fakeModule) Register(_ context.Context, _ *container.Container) error { return nil }

// Migrations returns no schema.
func (m *fakeModule) Migrations() fs.FS { return nil }

// Routes binds nothing.
func (m *fakeModule) Routes(_ chi.Router) {}
