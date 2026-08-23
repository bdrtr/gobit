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

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/module"
)

// --- dummy modül (test fixture) ---

// greeter dummy modülün sunduğu servistir.
type greeter struct{ prefix string }

func (g *greeter) Greet(name string) string { return g.prefix + " " + name }

// Greeter dummy modülü TÜKETEN tarafın tanımlayacağı dar arayüzdür.
// ADR 0001'in örüntüsü: tüketici arayüzü kendi paketinde tanımlar, sağlayıcının
// somut tipi onu yapısal olarak karşılar.
type Greeter interface {
	Greet(name string) string
}

// dummyModule Module sözleşmesini karşılayan test modülüdür.
type dummyModule struct {
	name       string
	migrations fs.FS
	registerFn func(ctx context.Context, c *container.Container) error

	// çağrı izleri
	events *[]string
}

func (m *dummyModule) Name() string { return m.name }

func (m *dummyModule) Register(ctx context.Context, c *container.Container) error {
	*m.events = append(*m.events, "register:"+m.name)
	if m.registerFn != nil {
		return m.registerFn(ctx, c)
	}
	return c.Provide(m.name+".greeter", &greeter{prefix: "merhaba"})
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

// --- testler ---

// TestBootstrapResolvesModuleService Faz 1 DoD'sinin çekirdeğidir:
// dummy modül register edilip servisi container'dan çözülebiliyor mu.
func TestBootstrapResolvesModuleService(t *testing.T) {
	var events []string
	c := container.New(nil)
	reg := module.NewRegistry(nil, nil)
	reg.Add(newDummy("dummy", &events))

	router := chi.NewRouter()
	if err := reg.Bootstrap(context.Background(), c, router); err != nil {
		t.Fatalf("Bootstrap() = %v", err)
	}

	// Tüketici, kendi tanımladığı dar arayüzle çözüyor (ADR 0001).
	g, err := container.Resolve[Greeter](c, "dummy.greeter")
	if err != nil {
		t.Fatalf("Resolve[Greeter]() = %v", err)
	}
	if got, want := g.Greet("dünya"), "merhaba dünya"; got != want {
		t.Errorf("Greet() = %q, beklenen %q", got, want)
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
		t.Fatalf("status = %d, beklenen 200", rec.Code)
	}
	if got := rec.Body.String(); got != "dummy-pong" {
		t.Errorf("gövde = %q", got)
	}
}

// TestBootstrapOrdersAllRegistersBeforeAnyRoute sıranın bilinçli olduğunu sabitler:
// bir modülün handler'ı başka modülün servisini güvenle çözebilsin diye TÜM
// modüller route bağlamadan önce register olmalıdır.
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
		t.Fatalf("olaylar = %v, beklenen %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("olaylar = %v, beklenen %v", events, want)
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
		t.Fatal("Bootstrap() tekrarlanan adı kabul etti")
	}
	if !errors.IsConflict(err) {
		t.Errorf("hata sınıfı = %v, beklenen Conflict", errors.KindOf(err))
	}
	// Hiçbir modül register EDİLMEMELİ: doğrulama her şeyden önce gelir.
	if len(events) != 0 {
		t.Errorf("doğrulama başarısızken modüller çalıştı: %v", events)
	}
}

func TestBootstrapRejectsEmptyName(t *testing.T) {
	var events []string
	reg := module.NewRegistry(nil, nil)
	reg.Add(newDummy("", &events))

	err := reg.Bootstrap(context.Background(), container.New(nil), chi.NewRouter())
	if !errors.IsInvalid(err) {
		t.Errorf("hata sınıfı = %v, beklenen Invalid (%v)", errors.KindOf(err), err)
	}
}

func TestBootstrapWrapsRegisterErrorWithModuleName(t *testing.T) {
	var events []string
	boom := errors.Unavailable("dep_down", "bağımlılık erişilemez")
	m := newDummy("kirik", &events)
	m.registerFn = func(context.Context, *container.Container) error { return boom }

	reg := module.NewRegistry(nil, nil)
	reg.Add(m)

	err := reg.Bootstrap(context.Background(), container.New(nil), chi.NewRouter())
	if err == nil {
		t.Fatal("Bootstrap() hata dönmeliydi")
	}
	if !errors.Is(err, boom) {
		t.Error("özgün hata zincirde korunmadı")
	}
	// Sınıf korunmalı ki HTTP katmanı doğru status seçsin.
	if errors.KindOf(err) != errors.KindUnavailable {
		t.Errorf("sınıf = %v, beklenen Unavailable", errors.KindOf(err))
	}
	// Hangi modülün patladığı mesajda olmalı.
	if got := err.Error(); !strings.Contains(got, "kirik") {
		t.Errorf("hata mesajında modül adı yok: %q", got)
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
	beta := newDummy("beta", &events) // migration'ı yok -> atlanmalı

	reg := module.NewRegistry(nil, migrate)
	reg.Add(alpha)
	reg.Add(beta)

	if err := reg.Bootstrap(context.Background(), container.New(nil), chi.NewRouter()); err != nil {
		t.Fatalf("Bootstrap() = %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("migrate çağrı sayısı = %d, beklenen 1 (nil migration atlanmalı)", len(calls))
	}
	if calls[0].owner != "alpha" {
		t.Errorf("owner = %q, beklenen %q", calls[0].owner, "alpha")
	}
	if len(calls[0].files) != 1 || calls[0].files[0] != "0001_init.up.sql" {
		t.Errorf("migration dosyaları = %v", calls[0].files)
	}
}

func TestMigrateNilSkipsSilently(t *testing.T) {
	var events []string
	alpha := newDummy("alpha", &events)
	alpha.migrations = fstest.MapFS{"0001_init.up.sql": &fstest.MapFile{Data: []byte("SELECT 1;")}}

	reg := module.NewRegistry(nil, nil) // migrate işlevi yok
	reg.Add(alpha)

	if err := reg.Bootstrap(context.Background(), container.New(nil), chi.NewRouter()); err != nil {
		t.Fatalf("migrate nil iken Bootstrap hata verdi: %v", err)
	}
}

func TestModulesReturnsCopy(t *testing.T) {
	var events []string
	reg := module.NewRegistry(nil, nil)
	reg.Add(newDummy("alpha", &events))

	mods := reg.Modules()
	mods[0] = nil // dönen dilim iç durumu bozmamalı

	if got := reg.Modules(); got[0] == nil {
		t.Error("Modules() iç dilimi paylaşıyor")
	}
}

func TestBootstrapNilRouterIsSafe(t *testing.T) {
	var events []string
	reg := module.NewRegistry(nil, nil)
	reg.Add(newDummy("alpha", &events))

	if err := reg.Bootstrap(context.Background(), container.New(nil), nil); err != nil {
		t.Fatalf("nil router ile Bootstrap = %v", err)
	}
}
