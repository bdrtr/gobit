package plugin_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	coreprovider "github.com/bdrtr/gobit/core/provider"
)

// fakePaymentProvider is the smallest payment provider used in the tests.
type fakePaymentProvider struct{ id string }

// ID returns the provider's identity.
func (p fakePaymentProvider) ID() string { return p.id }

// CreateSession is never called in the tests; it exists to satisfy the interface.
func (p fakePaymentProvider) CreateSession(
	_ context.Context, _ coreprovider.CreateSessionInput,
) (coreprovider.Session, error) {
	return coreprovider.Session{}, nil
}

// Authorize is never called in the tests.
func (p fakePaymentProvider) Authorize(
	_ context.Context, _ string,
) (coreprovider.AuthResult, error) {
	return coreprovider.AuthResult{}, nil
}

// Capture is never called in the tests.
func (p fakePaymentProvider) Capture(_ context.Context, _ string, _ int64) error { return nil }

// Refund is never called in the tests.
func (p fakePaymentProvider) Refund(_ context.Context, _ string, _ int64) error { return nil }

// Cancel is never called in the tests.
func (p fakePaymentProvider) Cancel(_ context.Context, _ string) error { return nil }

// fakePaymentRegistry imitates the payment module's provider registry.
type fakePaymentRegistry struct {
	registered []string
	err        error
}

// Register registers the provider or returns the configured error.
func (k *fakePaymentRegistry) Register(p coreprovider.PaymentProvider) error {
	if k.err != nil {
		return k.err
	}

	k.registered = append(k.registered, p.ID())

	return nil
}

// fakeNotificationProvider is the smallest notification provider used in the
// tests.
type fakeNotificationProvider struct{ id string }

// ID returns the provider's identity.
func (p fakeNotificationProvider) ID() string { return p.id }

// Send is never called in the tests; it exists to satisfy the interface.
func (p fakeNotificationProvider) Send(_ context.Context, _ coreprovider.Notification) error {
	return nil
}

// fakeNotificationRegistry imitates the notification module's provider
// registry.
type fakeNotificationRegistry struct {
	registered []string
}

// Register registers the provider.
func (k *fakeNotificationRegistry) Register(p coreprovider.NotificationProvider) error {
	k.registered = append(k.registered, p.ID())

	return nil
}

// fakeFileProvider is the smallest file provider used in the tests.
type fakeFileProvider struct{ id string }

// ID returns the provider's identity.
func (p fakeFileProvider) ID() string { return p.id }

// Upload is never called in the tests; it exists to satisfy the interface.
func (p fakeFileProvider) Upload(
	_ context.Context, _ coreprovider.UploadInput,
) (coreprovider.File, error) {
	return coreprovider.File{}, nil
}

// Delete is never called in the tests.
func (p fakeFileProvider) Delete(_ context.Context, _ string) error { return nil }

// fakeFileRegistry imitates the file module's provider registry.
type fakeFileRegistry struct {
	registered []string
}

// Register registers the provider.
func (k *fakeFileRegistry) Register(p coreprovider.FileProvider) error {
	k.registered = append(k.registered, p.ID())

	return nil
}

// testPlugin is a plugin that runs the function given for Setup.
type testPlugin struct {
	name  string
	setup func(ctx context.Context, h *coreplugin.Host) error
}

// Name returns the plugin's name.
func (e testPlugin) Name() string { return e.name }

// Setup runs the configured function.
func (e testPlugin) Setup(ctx context.Context, h *coreplugin.Host) error {
	if e.setup == nil {
		return nil
	}

	return e.setup(ctx, h)
}

// newHost prepares the container, registry and host trio for a test.
func newHost(t *testing.T, settings map[string]string) (
	*container.Container, *coreplugin.Registry, *coreplugin.Host,
) {
	t.Helper()

	log := slog.New(slog.DiscardHandler)
	c := container.New(log)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	return c, coreplugin.NewRegistry(log), coreplugin.NewHost(c, nil, nil, log, settings)
}

// TestProviderRegistrationWaitsUntilStart proves the registration is applied at
// Start and NOT at Setup.
//
// Had it been applied at Setup, every provider plugin would blow up during
// installation because the payment module is not registered yet; getting the
// order right is the core's job, not the plugin's.
func TestProviderRegistrationWaitsUntilStart(t *testing.T) {
	t.Parallel()

	c, reg, h := newHost(t, nil)

	reg.Add(testPlugin{name: "stripe", setup: func(_ context.Context, h *coreplugin.Host) error {
		h.RegisterPaymentProvider(fakePaymentProvider{id: "stripe"})
		return nil
	}})

	// The payment module is NOT there yet.
	require.NoError(t, reg.Install(t.Context(), h), "the installation must pass without the module too")

	registry := &fakePaymentRegistry{}
	require.NoError(t, c.Provide(coreplugin.PaymentProvidersName, registry))

	require.NoError(t, reg.Start(t.Context(), h))
	assert.Equal(t, []string{"stripe"}, registry.registered)
}

// TestProviderRegistrationFailsWithoutTheModule proves Start does NOT STAY
// SILENT while the payment module is not registered at all.
//
// Had it stayed silent, an installation believed to have "the stripe plugin
// installed" would take no payment, and that would only be noticed at the first
// customer attempt.
func TestProviderRegistrationFailsWithoutTheModule(t *testing.T) {
	t.Parallel()

	_, reg, h := newHost(t, nil)

	reg.Add(testPlugin{name: "stripe", setup: func(_ context.Context, h *coreplugin.Host) error {
		h.RegisterPaymentProvider(fakePaymentProvider{id: "stripe"})
		return nil
	}})
	require.NoError(t, reg.Install(t.Context(), h))

	err := reg.Start(t.Context(), h)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "payment")
	assert.Contains(t, err.Error(), "stripe")
}

// TestProviderRegistrationErrorPropagates proves the module refusing the
// registration is not swallowed in silence (e.g. two providers with the same
// identity).
func TestProviderRegistrationErrorPropagates(t *testing.T) {
	t.Parallel()

	c, reg, h := newHost(t, nil)
	require.NoError(t, c.Provide(coreplugin.PaymentProvidersName,
		&fakePaymentRegistry{err: errors.New("that identity is already registered")}))

	reg.Add(testPlugin{name: "stripe", setup: func(_ context.Context, h *coreplugin.Host) error {
		h.RegisterPaymentProvider(fakePaymentProvider{id: "stripe"})
		return nil
	}})
	require.NoError(t, reg.Install(t.Context(), h))

	err := reg.Start(t.Context(), h)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "that identity is already registered")
}

// TestNotificationProviderRegistrationWaitsUntilStart proves the notification
// provider is registered at Start and NOT at Setup.
//
// It is not a copy of the payment provider's test: the queueing is written
// SEPARATELY for every provider kind, and this kind is the one most open to
// being added with a shortcut that applies the registration directly — because
// the notification module does not come up as early as payment, the error would
// only be seen in a real installation.
func TestNotificationProviderRegistrationWaitsUntilStart(t *testing.T) {
	t.Parallel()

	c, reg, h := newHost(t, nil)

	reg.Add(testPlugin{name: "mailer", setup: func(_ context.Context, h *coreplugin.Host) error {
		h.RegisterNotificationProvider(fakeNotificationProvider{id: "smtp"})
		return nil
	}})

	// The notification module is NOT there yet.
	require.NoError(t, reg.Install(t.Context(), h), "the installation must pass without the module too")

	registry := &fakeNotificationRegistry{}
	require.NoError(t, c.Provide(coreplugin.NotificationProvidersName, registry))

	require.NoError(t, reg.Start(t.Context(), h))
	assert.Equal(t, []string{"smtp"}, registry.registered)
}

// TestNotificationProviderRegistrationFailsWithoutTheModule proves Start does
// NOT STAY SILENT while the notification module is not registered at all.
//
// Had it stayed silent the failure would be noticed even later than the payment
// provider's: a notification not being sent drops no HTTP request, the customer
// simply never receives the order email.
func TestNotificationProviderRegistrationFailsWithoutTheModule(t *testing.T) {
	t.Parallel()

	_, reg, h := newHost(t, nil)

	reg.Add(testPlugin{name: "mailer", setup: func(_ context.Context, h *coreplugin.Host) error {
		h.RegisterNotificationProvider(fakeNotificationProvider{id: "smtp"})
		return nil
	}})
	require.NoError(t, reg.Install(t.Context(), h))

	err := reg.Start(t.Context(), h)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "notification")
	assert.Contains(t, err.Error(), "smtp")
}

// TestFileProviderRegistrationWaitsUntilStart proves the file provider is
// registered at Start and NOT at Setup.
//
// The proof is MORE NECESSARY for this kind than for the others: there is no
// file module yet, so there is no real installation exercising the registration
// path end to end. If the queueing breaks here, nothing warns about it today;
// the failure only appears the day the module is written and the first plugin's
// installation blows up — and the blame is then looked for in the new module
// rather than in a registration point written months earlier.
func TestFileProviderRegistrationWaitsUntilStart(t *testing.T) {
	t.Parallel()

	c, reg, h := newHost(t, nil)

	reg.Add(testPlugin{name: "storage", setup: func(_ context.Context, h *coreplugin.Host) error {
		h.RegisterFileProvider(fakeFileProvider{id: "s3"})
		return nil
	}})

	// The file module is NOT there yet.
	require.NoError(t, reg.Install(t.Context(), h), "the installation must pass without the module too")

	registry := &fakeFileRegistry{}
	require.NoError(t, c.Provide(coreplugin.FileProvidersName, registry))

	require.NoError(t, reg.Start(t.Context(), h))
	assert.Equal(t, []string{"s3"}, registry.registered)
}

// TestFileProviderRegistrationFailsWithoutTheModule proves Start does NOT STAY
// SILENT while the file module is not registered at all.
//
// Had it stayed silent the upload path would stay up and the files would pile
// onto the container's local disk without ever reaching the plugin's store; the
// failure would only be seen when that disk was wiped and nothing was left but
// addresses leading nowhere.
func TestFileProviderRegistrationFailsWithoutTheModule(t *testing.T) {
	t.Parallel()

	_, reg, h := newHost(t, nil)

	reg.Add(testPlugin{name: "storage", setup: func(_ context.Context, h *coreplugin.Host) error {
		h.RegisterFileProvider(fakeFileProvider{id: "s3"})
		return nil
	}})
	require.NoError(t, reg.Install(t.Context(), h))

	err := reg.Start(t.Context(), h)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "file")
	assert.Contains(t, err.Error(), "s3")
}

// TestASetupErrorStopsTheInstallation proves one plugin's Setup error stops the
// following ones from running and says which plugin blew up.
func TestASetupErrorStopsTheInstallation(t *testing.T) {
	t.Parallel()

	_, reg, h := newHost(t, nil)

	nextRan := false

	reg.Add(testPlugin{name: "broken", setup: func(_ context.Context, _ *coreplugin.Host) error {
		return coreerrors.Invalid("missing_setting", "STRIPE_API_KEY was not given")
	}})
	reg.Add(testPlugin{name: "next", setup: func(_ context.Context, _ *coreplugin.Host) error {
		nextRan = true
		return nil
	}})

	err := reg.Install(t.Context(), h)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "broken")
	assert.Contains(t, err.Error(), "STRIPE_API_KEY")
	assert.False(t, nextRan, "the installation must stop after the failing plugin")
}

// TestADuplicatePluginNameIsRejected proves a repeated plugin name is caught.
func TestADuplicatePluginNameIsRejected(t *testing.T) {
	t.Parallel()

	_, reg, h := newHost(t, nil)
	reg.Add(testPlugin{name: "stripe"})
	reg.Add(testPlugin{name: "stripe"})

	err := reg.Install(t.Context(), h)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repeated")
}

// TestAnEmptyPluginNameIsRejected proves a plugin with no name is caught.
func TestAnEmptyPluginNameIsRejected(t *testing.T) {
	t.Parallel()

	_, reg, h := newHost(t, nil)
	reg.Add(testPlugin{name: "   "})

	err := reg.Install(t.Context(), h)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

// TestSettingCountsAnEmptyValueAsNotGiven proves a defined but empty setting
// counts as "absent".
//
// Otherwise the process would start with an empty API key and the failure would
// only be seen at the first real call, in production.
func TestSettingCountsAnEmptyValueAsNotGiven(t *testing.T) {
	t.Parallel()

	_, _, h := newHost(t, map[string]string{
		"STRIPE_API_KEY": "sk_test_1",
		"EMPTY":          "",
		"ONLY_SPACES":    "   ",
	})

	v, ok := h.Setting("STRIPE_API_KEY")
	assert.True(t, ok)
	assert.Equal(t, "sk_test_1", v)

	for _, k := range []string{"EMPTY", "ONLY_SPACES", "ABSENT"} {
		v, ok := h.Setting(k)
		assert.False(t, ok, "%s must count as not given", k)
		assert.Empty(t, v)
	}
}

// TestSubscriptionWaitsUntilStart proves the subscription is queued too.
func TestSubscriptionWaitsUntilStart(t *testing.T) {
	t.Parallel()

	log := slog.New(slog.DiscardHandler)
	bus := eventbus.NewInMemory(log)
	t.Cleanup(func() { _ = bus.Shutdown(context.Background()) })

	c := container.New(log)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	reg := coreplugin.NewRegistry(log)
	h := coreplugin.NewHost(c, nil, bus, log, nil)

	received := make(chan string, 1)

	reg.Add(testPlugin{name: "watcher", setup: func(_ context.Context, h *coreplugin.Host) error {
		h.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
			received <- e.Name
			return nil
		})

		return nil
	}})

	require.NoError(t, reg.Install(t.Context(), h))
	require.NoError(t, reg.Start(t.Context(), h))

	require.NoError(t, bus.Publish(t.Context(), eventbus.Event{
		Name: "order.placed", Data: map[string]any{"id": "order_1"},
	}))

	assert.Equal(t, "order.placed", <-received)
}

// TestSubscribingWithoutABusFails proves subscribing without an event bus does
// NOT fail silently.
func TestSubscribingWithoutABusFails(t *testing.T) {
	t.Parallel()

	_, reg, h := newHost(t, nil)

	reg.Add(testPlugin{name: "watcher", setup: func(_ context.Context, h *coreplugin.Host) error {
		h.Subscribe("order.placed", func(_ context.Context, _ eventbus.Event) error { return nil })
		return nil
	}})
	require.NoError(t, reg.Install(t.Context(), h))

	err := reg.Start(t.Context(), h)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "order.placed")
}

// TestRouteBinding proves a plugin's route is really bound.
func TestRouteBinding(t *testing.T) {
	t.Parallel()

	_, reg, h := newHost(t, nil)

	reg.Add(testPlugin{name: "webhook", setup: func(_ context.Context, h *coreplugin.Host) error {
		h.AddRoutes(func(r chi.Router) {
			r.Post("/hooks/stripe", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusAccepted)
			})
		})

		return nil
	}})
	require.NoError(t, reg.Install(t.Context(), h))

	router := chi.NewRouter()
	require.NoError(t, reg.MountRoutes(router, h))

	rctx := chi.NewRouteContext()
	assert.True(t, router.Match(rctx, http.MethodPost, "/hooks/stripe"),
		"the plugin route must be bound")
}

// newModuleRouter builds a Mounted module surface, as in a real installation.
//
// Registering directly with router.Get would not be enough: on a Mount, chi
// stores the path as "/store/v1/products/*", and seeing that residue is the
// conflict check's real exam.
func newModuleRouter(t *testing.T, path, body string) chi.Router {
	t.Helper()

	sub := chi.NewRouter()
	sub.Get("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	})

	router := chi.NewRouter()
	router.Mount(path, sub)

	return router
}

// routePlugin produces a plugin registering the given route function.
func routePlugin(ad string, fn func(r chi.Router)) testPlugin {
	return testPlugin{name: ad, setup: func(_ context.Context, h *coreplugin.Host) error {
		h.AddRoutes(fn)

		return nil
	}}
}

// readBody asks the router for the path and returns the response body.
func readBody(t *testing.T, router chi.Router, method, path string) string {
	t.Helper()

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(method, path, http.NoBody))

	return rec.Body.String()
}

// TestAPluginCannotShadowAModulePath proves a plugin overwriting a module path
// is caught BEFOREHAND.
//
// When chi registers the same pattern a second time it overwrites the handler
// silently; the rule "bind the plugins after the modules" is no protection on
// its own. Without the check the storefront's product list falls to the
// plugin's handler and the failure is only noticed when a customer sees an
// empty list.
func TestAPluginCannotShadowAModulePath(t *testing.T) {
	t.Parallel()

	_, reg, h := newHost(t, nil)
	reg.Add(routePlugin("greedy", func(r chi.Router) {
		r.Get("/store/v1/products", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("plugin"))
		})
	}))
	require.NoError(t, reg.Install(t.Context(), h))

	router := newModuleRouter(t, "/store/v1/products", "module")

	err := reg.MountRoutes(router, h)
	require.Error(t, err)
	assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))
	assert.Contains(t, err.Error(), "greedy", "the error must say which plugin is to blame")
	assert.Contains(t, err.Error(), "/store/v1/products")

	assert.Equal(t, "module", readBody(t, router, http.MethodGet, "/store/v1/products"),
		"the module's handler must stay in place")
}

// TestAConflictAlsoStopsTheFollowingPlugin proves the installation stops on a
// conflict.
//
// A partially bound surface is harder to diagnose than a server that never
// opened: some plugin endpoints work while others return 404.
func TestAConflictAlsoStopsTheFollowingPlugin(t *testing.T) {
	t.Parallel()

	_, reg, h := newHost(t, nil)
	reg.Add(routePlugin("greedy", func(r chi.Router) {
		r.Get("/store/v1/products", func(http.ResponseWriter, *http.Request) {})
	}))
	reg.Add(routePlugin("innocent", func(r chi.Router) {
		r.Post("/hooks/innocent", func(http.ResponseWriter, *http.Request) {})
	}))
	require.NoError(t, reg.Install(t.Context(), h))

	router := newModuleRouter(t, "/store/v1/products", "module")
	require.Error(t, reg.MountRoutes(router, h))

	assert.False(t, router.Match(chi.NewRouteContext(), http.MethodPost, "/hooks/innocent"),
		"the plugin after the conflict must not be bound")
}

// TestTwoPluginsCannotBindTheSamePath proves the conflict check works BETWEEN
// plugins too.
//
// Because the first plugin's routes enter the real router, a second plugin
// could overwrite those as well; a check protecting only the module paths would
// be incomplete.
func TestTwoPluginsCannotBindTheSamePath(t *testing.T) {
	t.Parallel()

	_, reg, h := newHost(t, nil)
	samePath := func(r chi.Router) {
		r.Post("/hooks/stripe", func(http.ResponseWriter, *http.Request) {})
	}
	reg.Add(routePlugin("first", samePath))
	reg.Add(routePlugin("second", samePath))
	require.NoError(t, reg.Install(t.Context(), h))

	err := reg.MountRoutes(chi.NewRouter(), h)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "second", "the second plugin must be the one blamed")
	assert.Contains(t, err.Error(), "/hooks/stripe")
}

// TestADifferentMethodIsNotAConflict proves a DIFFERENT method on the same path
// is not blocked.
//
// chi keeps the methods apart; adding a POST does not overwrite the GET. A
// check that looked only at the path would reject a legitimate plugin for
// nothing.
func TestADifferentMethodIsNotAConflict(t *testing.T) {
	t.Parallel()

	_, reg, h := newHost(t, nil)
	reg.Add(routePlugin("subscription", func(r chi.Router) {
		r.Post("/store/v1/products", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("plugin"))
		})
	}))
	require.NoError(t, reg.Install(t.Context(), h))

	router := newModuleRouter(t, "/store/v1/products", "module")
	require.NoError(t, reg.MountRoutes(router, h))

	assert.Equal(t, "module", readBody(t, router, http.MethodGet, "/store/v1/products"))
	assert.Equal(t, "plugin", readBody(t, router, http.MethodPost, "/store/v1/products"))
}

// TestAnInvalidRouteReturnsAReadableError proves chi's panic is turned into an
// error.
//
// Had the panic been left as it is, only chi's internal stack trace would show
// at startup and it would not say which plugin was to blame.
func TestAnInvalidRouteReturnsAReadableError(t *testing.T) {
	t.Parallel()

	_, reg, h := newHost(t, nil)
	reg.Add(routePlugin("broken", func(r chi.Router) {
		// chi rejects a pattern that does not start with "/".
		r.Get("hooks/stripe", func(http.ResponseWriter, *http.Request) {})
	}))
	require.NoError(t, reg.Install(t.Context(), h))

	router := newModuleRouter(t, "/store/v1/products", "module")

	var err error
	require.NotPanics(t, func() { err = reg.MountRoutes(router, h) })
	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err))
	assert.Contains(t, err.Error(), "broken")
	assert.Equal(t, "module", readBody(t, router, http.MethodGet, "/store/v1/products"),
		"an invalid registration must not touch the real router at all")
}

// TestCallingStartTwiceDoesNotRegisterAgain proves the queue is drained.
//
// Without draining, a second Start would try to register the same provider
// again and bring the installation down with a "that identity is already
// registered" error.
func TestCallingStartTwiceDoesNotRegisterAgain(t *testing.T) {
	t.Parallel()

	c, reg, h := newHost(t, nil)
	registry := &fakePaymentRegistry{}
	require.NoError(t, c.Provide(coreplugin.PaymentProvidersName, registry))

	reg.Add(testPlugin{name: "stripe", setup: func(_ context.Context, h *coreplugin.Host) error {
		h.RegisterPaymentProvider(fakePaymentProvider{id: "stripe"})
		return nil
	}})

	require.NoError(t, reg.Install(t.Context(), h))
	require.NoError(t, reg.Start(t.Context(), h))
	require.NoError(t, reg.Start(t.Context(), h))

	assert.Equal(t, []string{"stripe"}, registry.registered, "the provider must be registered once")
}

// TestPluginsReturnsTheNames proves the installed plugins can be listed.
func TestPluginsReturnsTheNames(t *testing.T) {
	t.Parallel()

	_, reg, _ := newHost(t, nil)
	reg.Add(testPlugin{name: "stripe"})
	reg.Add(testPlugin{name: "shippo"})

	assert.Equal(t, []string{"stripe", "shippo"}, reg.Plugins())
}
