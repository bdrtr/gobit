package module_test

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/module"
)

// --- the dummy module (test fixture) ---

// greeter is the service the dummy module offers.
type greeter struct{ prefix string }

func (g *greeter) Greet(name string) string { return g.prefix + " " + name }

// Greeter is the narrow interface the side CONSUMING the dummy module would
// declare. It is ADR 0001's pattern: the consumer declares the interface in its
// own package and the provider's concrete type satisfies it structurally.
type Greeter interface {
	Greet(name string) string
}

// dummyModule is the test module satisfying the Module contract.
type dummyModule struct {
	name       string
	migrations fs.FS
	registerFn func(ctx context.Context, c *container.Container) error

	// the call trace
	events *[]string
}

func (m *dummyModule) Name() string { return m.name }

func (m *dummyModule) Register(ctx context.Context, c *container.Container) error {
	*m.events = append(*m.events, "register:"+m.name)
	if m.registerFn != nil {
		return m.registerFn(ctx, c)
	}
	return c.Provide(m.name+".greeter", &greeter{prefix: "hello"})
}

func (m *dummyModule) Migrations() fs.FS { return m.migrations }

func (m *dummyModule) Routes(r chi.Router) {
	*m.events = append(*m.events, "routes:"+m.name)
	r.Get("/"+m.name+"/ping", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(m.name + "-pong"))
	})
}

func newDummy(name string, events *[]string) *dummyModule {
	return &dummyModule{name: name, events: events}
}

// --- the tests ---

// TestBootstrapResolvesModuleService is the core of Phase 1's DoD: can a
// dummy module be registered and its service resolved from the container.
func TestBootstrapResolvesModuleService(t *testing.T) {
	var events []string
	c := container.New(nil)
	reg := module.NewRegistry(nil, nil)
	reg.Add(newDummy("dummy", &events))

	router := chi.NewRouter()
	if err := reg.Bootstrap(context.Background(), c, router); err != nil {
		t.Fatalf("Bootstrap() = %v", err)
	}

	// The consumer resolves through the narrow interface it declared itself
	// (ADR 0001).
	g, err := container.Resolve[Greeter](c, "dummy.greeter")
	if err != nil {
		t.Fatalf("Resolve[Greeter]() = %v", err)
	}
	if got, want := g.Greet("world"), "hello world"; got != want {
		t.Errorf("Greet() = %q, want %q", got, want)
	}
}

func TestBootstrapMountsRoutes(t *testing.T) {
	var events []string
	c := container.New(nil)
	reg := module.NewRegistry(nil, nil)
	reg.Add(newDummy("dummy", &events))

	router := chi.NewRouter()
	if err := reg.Bootstrap(context.Background(), c, router); err != nil {
		t.Fatalf("Bootstrap() = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/dummy/ping", http.NoBody)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "dummy-pong" {
		t.Errorf("body = %q", got)
	}
}

// TestBootstrapOrdersAllRegistersBeforeAnyRoute pins that the order is
// deliberate: so that one module's handler can safely resolve another module's
// service, ALL modules must register before any route is bound.
func TestBootstrapOrdersAllRegistersBeforeAnyRoute(t *testing.T) {
	var events []string
	c := container.New(nil)
	reg := module.NewRegistry(nil, nil)
	reg.Add(newDummy("alpha", &events))
	reg.Add(newDummy("beta", &events))

	if err := reg.Bootstrap(context.Background(), c, chi.NewRouter()); err != nil {
		t.Fatalf("Bootstrap() = %v", err)
	}

	want := []string{"register:alpha", "register:beta", "routes:alpha", "routes:beta"}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
}

func TestBootstrapRejectsDuplicateName(t *testing.T) {
	var events []string
	reg := module.NewRegistry(nil, nil)
	reg.Add(newDummy("dummy", &events))
	reg.Add(newDummy("dummy", &events))

	err := reg.Bootstrap(context.Background(), container.New(nil), chi.NewRouter())
	if err == nil {
		t.Fatal("Bootstrap() accepted a repeated name")
	}
	if !errors.IsConflict(err) {
		t.Errorf("error class = %v, want Conflict", errors.KindOf(err))
	}
	// NO module may register: validation comes before everything else.
	if len(events) != 0 {
		t.Errorf("the modules ran while validation was failing: %v", events)
	}
}

func TestBootstrapRejectsEmptyName(t *testing.T) {
	var events []string
	reg := module.NewRegistry(nil, nil)
	reg.Add(newDummy("", &events))

	err := reg.Bootstrap(context.Background(), container.New(nil), chi.NewRouter())
	if !errors.IsInvalid(err) {
		t.Errorf("error class = %v, want Invalid (%v)", errors.KindOf(err), err)
	}
}

func TestBootstrapWrapsRegisterErrorWithModuleName(t *testing.T) {
	var events []string
	boom := errors.Unavailable("dep_down", "the dependency is unreachable")
	m := newDummy("broken", &events)
	m.registerFn = func(context.Context, *container.Container) error { return boom }

	reg := module.NewRegistry(nil, nil)
	reg.Add(m)

	err := reg.Bootstrap(context.Background(), container.New(nil), chi.NewRouter())
	if err == nil {
		t.Fatal("Bootstrap() should have returned an error")
	}
	if !errors.Is(err, boom) {
		t.Error("the original error was not preserved in the chain")
	}
	// The class must be preserved so the HTTP layer picks the right status.
	if errors.KindOf(err) != errors.KindUnavailable {
		t.Errorf("class = %v, want Unavailable", errors.KindOf(err))
	}
	// Which module blew up must be in the message.
	if got := err.Error(); !strings.Contains(got, "broken") {
		t.Errorf("the module name is missing from the error message: %q", got)
	}
}

func TestMigrationsRunPerModuleWithOwner(t *testing.T) {
	var events []string
	type call struct {
		owner string
		files []string
	}
	var calls []call

	migrate := func(_ context.Context, src fs.FS, owner string) error {
		names, err := fs.Glob(src, "*")
		if err != nil {
			return err
		}
		calls = append(calls, call{owner: owner, files: names})
		return nil
	}

	alpha := newDummy("alpha", &events)
	alpha.migrations = fstest.MapFS{"0001_init.up.sql": &fstest.MapFile{Data: []byte("SELECT 1;")}}
	beta := newDummy("beta", &events) // no migrations -> must be skipped

	reg := module.NewRegistry(nil, migrate)
	reg.Add(alpha)
	reg.Add(beta)

	if err := reg.Bootstrap(context.Background(), container.New(nil), chi.NewRouter()); err != nil {
		t.Fatalf("Bootstrap() = %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("migrate call count = %d, want 1 (a nil migration must be skipped)", len(calls))
	}
	if calls[0].owner != "alpha" {
		t.Errorf("owner = %q, want %q", calls[0].owner, "alpha")
	}
	if len(calls[0].files) != 1 || calls[0].files[0] != "0001_init.up.sql" {
		t.Errorf("migration files = %v", calls[0].files)
	}
}

func TestMigrateNilSkipsSilently(t *testing.T) {
	var events []string
	alpha := newDummy("alpha", &events)
	alpha.migrations = fstest.MapFS{"0001_init.up.sql": &fstest.MapFile{Data: []byte("SELECT 1;")}}

	reg := module.NewRegistry(nil, nil) // no migrate function
	reg.Add(alpha)

	if err := reg.Bootstrap(context.Background(), container.New(nil), chi.NewRouter()); err != nil {
		t.Fatalf("Bootstrap failed with migrate nil: %v", err)
	}
}

func TestModulesReturnsCopy(t *testing.T) {
	var events []string
	reg := module.NewRegistry(nil, nil)
	reg.Add(newDummy("alpha", &events))

	mods := reg.Modules()
	mods[0] = nil // the returned slice must not corrupt the internal state

	if got := reg.Modules(); got[0] == nil {
		t.Error("Modules() shares the internal slice")
	}
}

func TestBootstrapNilRouterIsSafe(t *testing.T) {
	var events []string
	reg := module.NewRegistry(nil, nil)
	reg.Add(newDummy("alpha", &events))

	if err := reg.Bootstrap(context.Background(), container.New(nil), nil); err != nil {
		t.Fatalf("Bootstrap with a nil router = %v", err)
	}
}
