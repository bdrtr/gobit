package searchpg_test

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/module"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	"github.com/bdrtr/gobit/plugins/searchpg"
)

// This file tests the plugin FROM OUTSIDE, through the surface the core sees:
// the registration points, the names, and what setup does NOT resolve.
//
// The plugin CANNOT import any module (internal/arch
// TestPluginsDoNotImportModules) and the ban covers the test files too, so there
// is NO real product module here. The catalog is represented by a fake surface
// placed in the container under the name "product.interop" — which is exactly
// how the core sees product as well.

// fakeBus is an event bus that records the subscriptions.
type fakeBus struct {
	mu         sync.Mutex
	subscribed []string
	published  []eventbus.Event
}

var _ eventbus.EventBus = (*fakeBus)(nil)

// Publish adds the event to the list.
func (b *fakeBus) Publish(_ context.Context, e eventbus.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.published = append(b.published, e)

	return nil
}

// Subscribe records the subscribed event name.
func (b *fakeBus) Subscribe(eventName string, _ eventbus.Handler) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.subscribed = append(b.subscribed, eventName)

	return nil
}

// Shutdown does nothing.
func (b *fakeBus) Shutdown(_ context.Context) error { return nil }

// setUp takes the plugin as far as Start over the given container.
func setUp(t *testing.T, c *container.Container) (*module.Registry, *fakeBus, error) {
	t.Helper()

	log := slog.New(slog.DiscardHandler)
	modules := module.NewRegistry(log, nil)
	bus := &fakeBus{}

	reg := coreplugin.NewRegistry(log)
	reg.Add(searchpg.New())

	h := coreplugin.NewHost(c, modules, bus, log, nil)
	if err := reg.Install(t.Context(), h); err != nil {
		return modules, bus, err
	}

	return modules, bus, reg.Start(t.Context(), h)
}

// TestSetupRegistersTheModuleAndItsSubscriptions verifies that the plugin uses
// two of its three extension points at setup.
func TestSetupRegistersTheModuleAndItsSubscriptions(t *testing.T) {
	t.Parallel()

	c := container.New(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	modules, bus, err := setUp(t, c)
	require.NoError(t, err)

	registered := modules.Modules()
	require.Len(t, registered, 1, "the plugin has to add its OWN module to the registry")
	assert.Equal(t, searchpg.ModuleName, registered[0].Name())
	assert.NotNil(t, registered[0].Migrations(), "the module has to bring its own migration")

	assert.Equal(t,
		[]string{"product.created", "product.updated", "product.deleted"},
		bus.subscribed,
		"the index is kept fresh by three catalog events; the names are a contract between modules")
}

// TestSetupRunsOnAnEmptyContainer verifies that Setup resolves NOTHING from the
// container.
//
// During setup the modules have not come up yet: "product.interop" is NOT in the
// container at that moment. Had the plugin tried to resolve it in Setup, startup
// would fail even with product installed — with an error where nothing is
// actually missing.
func TestSetupRunsOnAnEmptyContainer(t *testing.T) {
	t.Parallel()

	c := container.New(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	modules, _, err := setUp(t, c)

	require.NoError(t, err, "setup has to complete on an empty container too")
	assert.Len(t, modules.Modules(), 1)
	assert.False(t, c.Has("product.interop"), "setup must NOT look for the catalog registration, nor create it")
}

// TestSetupAsksForNoConfiguration verifies that the plugin is set up without any
// configuration.
//
// Unlike paymentstripe there is no setting here that would stop startup when it
// is missing; the index table is created by the migration and the search engine
// is the PostgreSQL that is already there.
func TestSetupAsksForNoConfiguration(t *testing.T) {
	t.Parallel()

	c := container.New(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	_, _, err := setUp(t, c)

	assert.NoError(t, err)
}

// TestThePluginUsesNoRouteHook verifies that the endpoints come from the MODULE
// lifecycle.
//
// Routes bound through coreplugin.Host.AddRoutes are added AFTER the module
// routes and go through a separate conflict check. The search endpoints belong
// to the module's Routes and not there: they depend on the module's service, and
// if the module was not registered they must not exist at all.
func TestThePluginUsesNoRouteHook(t *testing.T) {
	t.Parallel()

	c := container.New(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	log := slog.New(slog.DiscardHandler)
	reg := coreplugin.NewRegistry(log)
	reg.Add(searchpg.New())
	h := coreplugin.NewHost(c, module.NewRegistry(log, nil), &fakeBus{}, log, nil)
	require.NoError(t, reg.Install(t.Context(), h))

	router := chi.NewRouter()
	require.NoError(t, reg.MountRoutes(router, h))

	var patterns []string
	require.NoError(t, chi.Walk(router,
		func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			patterns = append(patterns, method+" "+route)

			return nil
		}))
	assert.Empty(t, patterns, "the plugin must bind no endpoint to the route hook")
}

// TestTheNamesAreAContract fixes that the externally visible names are
// deliberate choices.
//
// The plugin's name goes into the PLUGINS list; the module's name turns directly
// into an SQL table name ("searchpg_schema_migrations") and into a scope prefix.
// That is why the two differ: a module name cannot carry a hyphen (see
// core/db.MigrationsTable).
func TestTheNamesAreAContract(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "search-pg", searchpg.Name)
	assert.Equal(t, "searchpg", searchpg.ModuleName)
	assert.Equal(t, "searchpg:write", searchpg.ScopeWrite,
		"the scope vocabulary has to have the same shape as the modules': <module>:write")
	assert.Equal(t, "/store/v1/sales-channels/{sales_channel_id}/search", searchpg.SearchPath,
		"the search endpoint carries its sales channel in the PATH (ADR 0044): the body "+
			"varies by channel, so the channel has to be in the cache key every shared "+
			"cache already sees")
	assert.Equal(t, "/admin/v1/search/reindex", searchpg.ReindexPath)
	assert.NotEqual(t, searchpg.Name, searchpg.ModuleName,
		"the module name turns into the migration version table and cannot carry a hyphen")
}

// TestTheMigrationsCanBeRolledBack verifies that every up file has a down pair.
//
// The gate of the same name in internal/arch scans ONLY under internal/modules;
// the plugins/ tree is in no architecture test's scope. A migration that cannot
// be rolled back makes a schema applied at startup impossible to roll back.
func TestTheMigrationsCanBeRolledBack(t *testing.T) {
	t.Parallel()

	modules, _, err := setUp(t, container.New(slog.New(slog.DiscardHandler)))
	require.NoError(t, err)
	require.Len(t, modules.Modules(), 1)

	src := modules.Modules()[0].Migrations()
	require.NotNil(t, src)

	entries, err := fs.ReadDir(src, ".")
	require.NoError(t, err)

	var ups []string
	present := map[string]struct{}{}
	for _, entry := range entries {
		present[entry.Name()] = struct{}{}
		if strings.HasSuffix(entry.Name(), ".up.sql") {
			ups = append(ups, entry.Name())
		}
	}

	require.NotEmpty(t, ups, "the plugin has to bring its own schema")
	for _, up := range ups {
		down := strings.TrimSuffix(up, ".up.sql") + ".down.sql"
		assert.Contains(t, present, down, "%s has to have a down pair", up)
	}
}
