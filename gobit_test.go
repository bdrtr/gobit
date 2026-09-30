package gobit

import (
	"context"
	"io"
	"io/fs"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/module"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
)

// TestTheFacadeCarriesWhatItIsGiven is the test for the failure that has no
// symptom.
//
// A facade whose Add does nothing still compiles, still links, still starts the
// server and still answers every request gobit ships — the customer's module is
// simply not there. Nothing errors, nothing logs and the two example programs
// under examples/ keep building and running, so the out-of-tree proofs do not
// see it either. It is the one thing this facade does that fails SILENTLY, and
// this is where it is caught.
func TestTheFacadeCarriesWhatItIsGiven(t *testing.T) {
	t.Parallel()

	first, second := &fakeModule{name: "loyalty"}, &fakeModule{name: "gift-cards"}
	plugin := &fakePlugin{}

	app := New().Version("1.2.3").Name("shop").Add(first).Use(plugin).Add(second)

	require.Equal(t, "1.2.3", app.opts.Version, "the version did not reach the options")
	require.Equal(t, "shop", app.opts.Name, "the name did not reach the options")
	require.Equal(t, []string{"loyalty", "gift-cards"}, names(app.opts.Modules),
		"the modules did not reach the options IN ORDER; registration order decides "+
			"which of two modules claiming one name is refused")
	require.Len(t, app.opts.Plugins, 1, "the plugin did not reach the options")
}

// TestTheFacadeStartsEmpty guards the other direction.
//
// An App that arrived with something already in it would mean the framework's
// own modules were being carried as "the caller's", and the refusal to remove a
// module would then apply to them too.
func TestTheFacadeStartsEmpty(t *testing.T) {
	t.Parallel()

	app := New()

	require.Empty(t, app.opts.Modules, "a new App already carries a module")
	require.Empty(t, app.opts.Plugins, "a new App already carries a plugin")
	require.Empty(t, app.opts.Version, "a new App already carries a version")
	require.Empty(t, app.opts.Name, "a new App already carries a name")
}

// TestANamedProgramCallsItselfByItsName holds ADR 0254 at the facade: the usage
// text and a subcommand's own refusal name the program the embedder named, and
// an unnamed one is still gobit.
func TestANamedProgramCallsItselfByItsName(t *testing.T) {
	t.Parallel()

	var help strings.Builder
	require.NoError(t, New().Name("shop").Main([]string{"help"}, &help))
	require.Contains(t, help.String(), "shop migrate status")
	require.NotContains(t, help.String(), "gobit migrate", "the usage text still names gobit")

	err := New().Name("shop").Main([]string{"new"}, io.Discard)
	require.Error(t, err)
	require.Contains(t, err.Error(), "shop new <dir>", "a refusal names the command to type")

	var unnamed strings.Builder
	require.NoError(t, New().Main([]string{"help"}, &unnamed))
	require.Contains(t, unnamed.String(), "gobit migrate status")
}

// names is the module names in registration order.
func names(modules []module.Module) []string {
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

// fakePlugin is a plugin written the way a customer project would write one.
type fakePlugin struct{}

// Name identifies the plugin to the host.
func (p *fakePlugin) Name() string { return "fake" }

// Setup registers nothing.
func (p *fakePlugin) Setup(_ context.Context, _ *coreplugin.Host) error { return nil }
