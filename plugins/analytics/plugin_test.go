package analytics_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/core/openapi"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	"github.com/bdrtr/gobit/plugins/analytics"
)

// This file tests the plugin FROM OUTSIDE, through the surface the core sees:
// what it registers, what it subscribes to, and what Setup does NOT resolve.
//
// The plugin cannot import any module (internal/arch TestPluginsDoNotImportModules)
// and the ban covers the test files too, so the topic names are written out here
// as strings — which is also the only honest way to check them: a constant
// compared against itself proves nothing about the name the cart module actually
// publishes. That end is proven end to end, in internal/e2e.

// fakeBus is an event bus that records the subscriptions.
type fakeBus struct {
	mu         sync.Mutex
	subscribed []string
}

var _ eventbus.EventBus = (*fakeBus)(nil)

// Publish does nothing.
func (b *fakeBus) Publish(context.Context, eventbus.Event) error { return nil }

// Subscribe records the subscribed event name.
func (b *fakeBus) Subscribe(eventName string, _ eventbus.Handler) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subscribed = append(b.subscribed, eventName)

	return nil
}

// Shutdown does nothing.
func (b *fakeBus) Shutdown(context.Context) error { return nil }

// setUp takes the plugin as far as Start over the given container.
func setUp(t *testing.T, c *container.Container) (*module.Registry, *fakeBus, error) {
	t.Helper()

	modules, bus, _, err := setUpWithHost(t, c)

	return modules, bus, err
}

// setUpWithHost is the same flow and also returns the host.
//
// It is separate for the reason the payment module keeps a second constructor:
// most tests do not look at the host and a fourth return value would put "_" in
// every one of them.
func setUpWithHost(
	t *testing.T, c *container.Container,
) (*module.Registry, *fakeBus, *coreplugin.Host, error) {
	t.Helper()

	log := slog.New(slog.DiscardHandler)
	modules := module.NewRegistry(log, nil)
	bus := &fakeBus{}

	reg := coreplugin.NewRegistry(log)
	reg.Add(analytics.New())

	h := coreplugin.NewHost(c, modules, bus, log, nil)
	if err := reg.Install(t.Context(), h); err != nil {
		return modules, bus, h, err
	}

	return modules, bus, h, reg.Start(t.Context(), h)
}

// TestSetupRegistersTheFunnelScreen is the screen's registration (ADR 0155).
//
// It is asserted here, on the plugin's side of the boundary, because this is
// where the decision lives: the plugin chooses to have a screen, and the panel
// only renders what it is handed. Without this, removing the registration is a
// change nothing in the repository notices — the funnel becomes a table only
// somebody with a terminal can read, which is the opposite of the plugin's point.
func TestSetupRegistersTheFunnelScreen(t *testing.T) {
	t.Parallel()

	c := container.New(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	_, _, host, err := setUpWithHost(t, c)
	require.NoError(t, err)

	pages := host.AdminPages()
	require.Len(t, pages, 1, "the plugin must put exactly one screen in the panel")
	assert.Equal(t, analytics.PageLabel, pages[0].Label)
	assert.Equal(t, analytics.PagePath, pages[0].Path)
	assert.True(t, strings.HasPrefix(pages[0].Path, "/admin/ui/"),
		"the screen must sit under the panel's prefix; a path outside it would be bound "+
			"where the panel's session ring never runs")
	assert.NotEmpty(t, pages[0].Script,
		"the panel serves these BYTES from its own origin, which is what lets its "+
			"content policy stay script-src 'self'")
	// The script reads the prefix out of the shell and appends the rest, so what
	// it carries is the endpoint MINUS the admin prefix. Asserting the whole path
	// would be asserting a string the script deliberately does not hold.
	assert.Contains(t, string(pages[0].Script),
		strings.TrimPrefix(analytics.FunnelPath, "/admin/v1"),
		"the screen's client must read THIS plugin's endpoint; a script that fetched "+
			"something else would render somebody else's numbers")
}

// TestSetupRegistersTheModuleAndItsThreeSubscriptions is the plugin's contract
// with the core.
func TestSetupRegistersTheModuleAndItsThreeSubscriptions(t *testing.T) {
	t.Parallel()

	c := container.New(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	modules, bus, err := setUp(t, c)
	require.NoError(t, err)

	registered := modules.Modules()
	require.Len(t, registered, 1, "the plugin has to add its OWN module to the registry")
	assert.Equal(t, analytics.ModuleName, registered[0].Name())
	assert.NotNil(t, registered[0].Migrations(), "the module has to bring its own migration")

	assert.Equal(t,
		[]string{"cart.created", "cart.completed", "order.placed"},
		bus.subscribed,
		"the funnel is three moments; the names are a contract between modules and a "+
			"renamed one makes this plugin count nothing while looking installed")
}

// TestSetupRunsOnAnEmptyContainer verifies that Setup resolves NOTHING.
//
// Setup runs before the modules come up, so the database pool is not in the
// container at that moment. Had the plugin tried to take it there, startup would
// fail with an error where nothing is actually missing.
func TestSetupRunsOnAnEmptyContainer(t *testing.T) {
	t.Parallel()

	c := container.New(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	modules, _, err := setUp(t, c)

	require.NoError(t, err, "setup has to complete on an empty container too")
	assert.Len(t, modules.Modules(), 1)
}

// TestTheFunnelEndpointIsNotBoundBeforeRegister keeps a handler with no table
// from existing.
//
// It is searchpg's rule and the product module's before it: an endpoint that
// answers 503 is worse than one that is not there, because a client cannot tell
// it from a broken installation. Register never runs here — there is no pool —
// so the module's Routes must bind nothing at all.
func TestTheFunnelEndpointIsNotBoundBeforeRegister(t *testing.T) {
	t.Parallel()

	c := container.New(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	modules, _, err := setUp(t, c)
	require.NoError(t, err)
	require.Len(t, modules.Modules(), 1)

	router, ok := modules.Modules()[0].(interface{ Routes(chi.Router) })
	require.True(t, ok, "the module must offer Routes; the core finds it by type assertion")

	r := chi.NewRouter()
	router.Routes(r)

	req := httptest.NewRequest(http.MethodGet, analytics.FunnelPath, http.NoBody)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code,
		"an unregistered module must bind NO endpoint: a funnel handler with no table "+
			"would answer a server error that reads as a broken installation")
}

// TestTheModuleDescribesItsEndpoint keeps the endpoint out of the debt ledger.
//
// An endpoint that enters /openapi.json with a path, a method and no body is a
// valid OpenAPI model, which is exactly why nothing else notices (ADR 0035).
func TestTheModuleDescribesItsEndpoint(t *testing.T) {
	t.Parallel()

	c := container.New(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	modules, _, err := setUp(t, c)
	require.NoError(t, err)

	describer, ok := modules.Modules()[0].(openapi.Describer)
	require.True(t, ok, "the module must describe its endpoint the way a core module does")

	doc := openapi.New("test", "0")
	describer.Describe(doc)
	r := chi.NewRouter()
	r.Get(analytics.FunnelPath, func(http.ResponseWriter, *http.Request) {})
	built, err := doc.Build(r)
	require.NoError(t, err)

	// The document goes through a JSON round-trip, as every other description
	// test in this tree does: Build returns typed Go values and what a client
	// reads is the ENCODED form, so asserting on the Go side can pass while the
	// served document differs.
	encoded, err := json.Marshal(built)
	require.NoError(t, err)
	var served map[string]any
	require.NoError(t, json.Unmarshal(encoded, &served))

	paths, ok := served["paths"].(map[string]any)
	require.True(t, ok)
	entry, found := paths[analytics.FunnelPath]
	require.True(t, found, "the funnel endpoint must be in the document")

	methods, ok := entry.(map[string]any)
	require.True(t, ok)
	operation, ok := methods["get"].(map[string]any)
	require.True(t, ok)
	assert.NotEmpty(t, operation["summary"])
	assert.NotEmpty(t, operation["responses"], "a described endpoint says what it RETURNS")
}
